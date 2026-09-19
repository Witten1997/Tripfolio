<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElDialog,
  ElEmpty,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessageBox,
  ElSkeleton,
} from 'element-plus'
import { computed } from 'vue'
import { VueDraggable } from 'vue-draggable-plus'

import CategoryIconPicker from '@/desktop/components/CategoryIconPicker.vue'
import type { ExpenseCategory } from '@/shared/api/categories'
import { useMetadataStore } from '@/shared/stores/metadata'
import { categoryIconComponent, categoryIconLabel } from '@/shared/travel/categoryIconVisuals'
import { useCategoryCards, useCategoryManager } from '@/shared/travel/useCategoryManager'

const metadata = useMetadataStore()
const manager = useCategoryManager()
const {
  opened,
  loading,
  saving,
  uncertainCreate,
  deleting,
  reordering,
  items,
  loadError,
  error,
  feedback,
  errors,
  active,
  baseline,
  latest,
  conflict,
  loadingLatest,
  draft,
  dirty,
} = manager
const busy = computed(() => saving.value || !!deleting.value || reordering.value)
const locked = computed(() => busy.value || uncertainCreate.value)
const cardsDisabled = computed(() => saving.value || !!deleting.value || uncertainCreate.value)

const { cards, dragging, onStart, onEnd, move } = useCategoryCards(manager)

async function discardChanges(closing = false) {
  if (uncertainCreate.value && !closing) return false
  if (!dirty.value && !uncertainCreate.value) return true
  try {
    await ElMessageBox.confirm(
      uncertainCreate.value
        ? '创建结果尚未确认，分类可能已经保存。关闭后请先重新打开分类管理并刷新列表核对，避免重复创建。'
        : '尚有未保存的分类输入，是否放弃？',
      uncertainCreate.value ? '关闭分类管理' : '放弃分类输入',
      {
        confirmButtonText: uncertainCreate.value ? '关闭并核对列表' : '放弃输入',
        cancelButtonText: uncertainCreate.value ? '返回重试' : '继续编辑',
        type: 'warning',
      },
    )
    return true
  } catch {
    return false
  }
}

async function requestClose(done?: () => void) {
  if (busy.value || !(await discardChanges(true))) return
  manager.close(true)
  done?.()
}

async function start(category?: ExpenseCategory) {
  if (locked.value) return
  if (category && active.value && baseline.value?.id === category.id) return
  if (await discardChanges()) manager.start(category)
}

async function remove() {
  const category = baseline.value
  if (!category || locked.value) return
  try {
    await ElMessageBox.confirm(
      `删除账号共用的“${category.name}”分类？被账目使用的分类不能删除，预设分类也遵循此规则。${dirty.value ? '当前未保存的修改也将放弃。' : ''}`,
      '删除账单分类',
      {
        confirmButtonText: '确认删除分类',
        cancelButtonText: '取消',
        type: 'warning',
      },
    )
  } catch {
    return
  }
  await manager.remove(category, true)
}

async function adoptLatest() {
  if (await discardChanges()) manager.adoptLatest()
}

defineExpose({ open: manager.open })
</script>

<template>
  <ElDialog
    :model-value="opened"
    title="账单分类管理"
    width="min(840px, calc(100vw - 32px))"
    :close-on-click-modal="false"
    :close-on-press-escape="!busy"
    :before-close="requestClose"
    destroy-on-close
  >
    <p class="category-intro">
      分类在本账号的所有旅行中共用。点击分类可改名、换图标或删除，按住卡片拖动可调整记账时的顺序。
    </p>
    <ElAlert
      v-if="feedback"
      :title="feedback"
      type="success"
      show-icon
      @close="feedback = null"
      class="category-alert"
    />
    <ElAlert
      v-if="error && !active"
      :title="error"
      :type="uncertainCreate ? 'warning' : 'error'"
      :closable="false"
      show-icon
      class="category-alert"
    />
    <div class="category-toolbar">
      <span>{{ reordering ? '正在保存顺序…' : `${items.length} 个分类` }}</span>
      <div class="tf-actions">
        <ElButton type="primary" :disabled="locked || loading" @click="start()">新增分类</ElButton>
      </div>
    </div>
    <ElSkeleton v-if="loading && !items.length" :rows="5" animated />
    <ElAlert
      v-if="loadError"
      :title="loadError"
      type="error"
      :closable="false"
      show-icon
      class="category-alert"
    />
    <div class="category-layout" :class="{ 'has-editor': active }">
      <div class="category-list">
        <ElEmpty
          v-if="!loading && !items.length && !loadError"
          description="暂无分类，添加一个常用分类吧"
        />
        <VueDraggable
          v-if="cards.length"
          v-model="cards"
          tag="ul"
          class="category-grid"
          :class="{ 'category-grid--busy': reordering }"
          ghost-class="category-card--ghost"
          chosen-class="category-card--chosen"
          drag-class="category-card--drag"
          :animation="150"
          :force-fallback="true"
          :delay="150"
          :touch-start-threshold="3"
          :disabled="locked || loading"
          :aria-busy="reordering"
          aria-label="账单分类，按住卡片拖动可调整顺序"
          @start="onStart"
          @end="onEnd"
        >
          <li v-for="(category, index) in cards" :key="category.id" class="category-card">
            <button
              type="button"
              :class="{ selected: active && baseline?.id === category.id }"
              :aria-label="`编辑分类：${category.name}${category.is_preset ? '（预设）' : ''}，按 Shift+方向键调整顺序`"
              :aria-pressed="active && baseline?.id === category.id"
              :disabled="cardsDisabled"
              @click="!dragging && start(category)"
              @keydown.shift.left.prevent="move(index, -1)"
              @keydown.shift.right.prevent="move(index, 1)"
            >
              <span class="category-card__symbol">
                <component :is="categoryIconComponent(category.icon)" aria-hidden="true" />
              </span>
              <span class="category-card__name">{{ category.name }}</span>
            </button>
          </li>
        </VueDraggable>
      </div>
      <section v-if="active" class="category-editor" aria-live="polite">
        <h3>{{ baseline ? '编辑分类' : '新增分类' }}</h3>
        <p v-if="baseline?.is_preset" class="category-hint">
          预设分类，改名、换图标与删除规则相同。
        </p>
        <ElAlert
          v-if="error"
          :title="error"
          :type="uncertainCreate ? 'warning' : 'error'"
          :closable="false"
          show-icon
          class="category-alert"
        />
        <ElAlert
          v-if="metadata.status !== 'ready' && !uncertainCreate"
          type="warning"
          :closable="false"
          class="category-alert"
          ><template #title>分类图标尚未加载</template
          ><ElButton :loading="metadata.status === 'loading'" @click="metadata.load"
            >加载图标</ElButton
          ></ElAlert
        >
        <ElForm
          id="category-editor-form"
          label-position="top"
          :disabled="locked"
          @submit.prevent="manager.save()"
        >
          <ElFormItem label="分类名称" required :error="errors.name"
            ><ElInput v-model="draft.name" maxlength="40" placeholder="例如：咖啡"
          /></ElFormItem>
          <ElFormItem label="分类图标" :error="errors.icon">
            <CategoryIconPicker
              v-model="draft.icon"
              :icons="metadata.metadata?.expense_category_icons ?? []"
              :disabled="locked"
            />
          </ElFormItem>
          <div v-if="conflict" class="category-conflict">
            <p>你的输入已保留。请检查最新分类后决定如何保存。</p>
            <ElSkeleton v-if="loadingLatest" :rows="2" animated />
            <p v-if="latest">最新内容：{{ latest.name }} · {{ categoryIconLabel(latest.icon) }}</p>
            <div class="tf-actions">
              <ElButton size="small" :disabled="!latest" @click="adoptLatest"
                >放弃输入并载入</ElButton
              >
            </div>
          </div>
        </ElForm>
        <div class="category-editor-actions tf-actions">
          <ElButton
            v-if="baseline"
            type="danger"
            plain
            :loading="deleting === baseline.id"
            :disabled="saving || reordering || uncertainCreate"
            @click="remove"
            >删除分类</ElButton
          >
          <ElButton
            v-if="conflict"
            type="primary"
            :loading="saving"
            :disabled="!latest || loadingLatest || metadata.status !== 'ready'"
            @click="manager.save(true)"
            >确认用我的改动更新</ElButton
          >
          <ElButton
            v-else
            type="primary"
            native-type="submit"
            form="category-editor-form"
            :loading="saving"
            :disabled="
              busy ||
              (!uncertainCreate && metadata.status !== 'ready') ||
              (baseline !== null && !dirty)
            "
            >{{ uncertainCreate ? '重试创建' : '保存分类' }}</ElButton
          >
        </div>
      </section>
    </div>
    <template #footer><ElButton :disabled="busy" @click="requestClose()">关闭</ElButton></template>
  </ElDialog>
</template>

<style scoped>
.category-intro {
  margin: 0 0 20px;
  color: var(--tf-text-3);
  line-height: 1.7;
}
.category-alert {
  margin-bottom: 16px;
}
.category-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  margin-bottom: 18px;
}
.category-toolbar > span {
  font-size: 13px;
  color: var(--tf-text-3);
}
.category-toolbar > div {
  display: flex;
  align-items: center;
  gap: 8px;
}
.category-toolbar .el-button {
  margin-left: 0;
}
.category-layout.has-editor {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 320px;
  gap: 24px;
}
.category-list {
  min-width: 0;
}
.category-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(84px, 1fr));
  gap: 10px 8px;
  margin: 0;
  padding: 4px;
  list-style: none;
}
.category-grid--busy {
  opacity: 0.7;
}
.category-card {
  min-width: 0;
  border-radius: var(--tf-radius-control);
}
.category-card > button {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-width: 0;
  padding: 12px 4px 10px;
  border: 0;
  border-radius: var(--tf-radius-control);
  background: transparent;
  color: var(--tf-text-2);
  font: inherit;
  font-size: 12px;
  line-height: 1.4;
  cursor: pointer;
  touch-action: manipulation;
  user-select: none;
  -webkit-user-select: none;
  -webkit-touch-callout: none;
}
.category-card__symbol {
  display: grid;
  width: 48px;
  height: 48px;
  place-items: center;
  border-radius: 18px;
  background: var(--tf-surface-sunken);
  color: var(--tf-text-1);
  transition:
    background-color var(--tf-duration-fast) var(--tf-ease),
    box-shadow var(--tf-duration-fast) var(--tf-ease);
}
.category-card__symbol svg {
  width: 23px;
  height: 23px;
  stroke-width: 1.6;
}
.category-card__name {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.category-card > button.selected {
  color: var(--tf-accent);
  font-weight: 600;
}
.category-card > button.selected .category-card__symbol {
  background: var(--tf-accent-soft);
  box-shadow: inset 0 0 0 1.5px var(--tf-accent);
}
.category-card > button:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 2px;
}
.category-card > button:disabled {
  cursor: default;
  opacity: 0.5;
}
.category-card--chosen > button {
  cursor: grabbing;
}
.category-card--chosen .category-card__symbol {
  transform: scale(1.08);
  box-shadow: var(--tf-shadow-2);
}
.category-card--ghost {
  background: var(--tf-accent-soft);
  outline: 1px dashed var(--tf-accent);
  outline-offset: -1px;
  opacity: 0.6;
}
.category-card--drag {
  background: var(--tf-surface-raised);
  box-shadow: var(--tf-shadow-2);
}
@media (hover: hover) {
  .category-card > button:hover:not(:disabled) .category-card__symbol {
    background: var(--tf-accent-soft);
  }
}
.category-editor {
  background: var(--tf-surface-sunken);
  border: var(--tf-surface-border);
  border-radius: var(--tf-radius-control);
  padding: 16px;
  align-self: start;
}
.category-editor h3 {
  margin: 0 0 12px;
  font-size: 16px;
}
.category-hint {
  margin: 0 0 14px;
  font-size: 12px;
  line-height: 1.7;
  color: var(--tf-text-3);
}
.category-conflict {
  font-size: 12px;
  line-height: 1.7;
  margin-bottom: 16px;
}
.category-conflict .tf-actions {
  margin-top: 8px;
}
.category-editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 4px;
}
.category-editor-actions .el-button {
  margin-left: 0;
}
@media (max-width: 700px) {
  .category-layout.has-editor {
    grid-template-columns: 1fr;
  }
}
</style>
