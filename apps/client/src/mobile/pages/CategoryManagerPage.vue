<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessageBox,
  ElSkeleton,
} from 'element-plus'
import { ArrowLeft, Plus, Tag, Trash2 } from '@lucide/vue'
import { computed, onMounted, onUnmounted } from 'vue'
import { VueDraggable } from 'vue-draggable-plus'
import { onBeforeRouteLeave } from 'vue-router'

import CategoryIconPicker from '@/desktop/components/CategoryIconPicker.vue'
import ResponsiveEditorShell from '@/desktop/components/ResponsiveEditorShell.vue'
import type { ExpenseCategory } from '@/shared/api/categories'
import { useMetadataStore } from '@/shared/stores/metadata'
import { categoryIconComponent, categoryIconLabel } from '@/shared/travel/categoryIconVisuals'
import { useCategoryCards, useCategoryManager } from '@/shared/travel/useCategoryManager'

const metadata = useMetadataStore()
const manager = useCategoryManager()
const {
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
const { cards, dragging, onStart, onEnd, move } = useCategoryCards(manager)
const busy = computed(() => saving.value || !!deleting.value || reordering.value)
const locked = computed(() => busy.value || uncertainCreate.value)
const cardsDisabled = computed(() => saving.value || !!deleting.value || uncertainCreate.value)
const saveDisabled = computed(
  () =>
    busy.value ||
    (!uncertainCreate.value && metadata.status !== 'ready') ||
    (!!baseline.value && !dirty.value) ||
    (conflict.value && (!latest.value || loadingLatest.value)),
)

onMounted(() => void manager.open())
onUnmounted(() => manager.close(true))
onBeforeRouteLeave(async () => {
  if (busy.value) return false
  return discardChanges()
})

async function discardChanges() {
  if (!dirty.value && !uncertainCreate.value) return true
  try {
    await ElMessageBox.confirm(
      uncertainCreate.value
        ? '创建结果尚未确认，分类可能已经保存。离开后请先刷新分类列表核对，避免重复创建。'
        : '尚有未保存的分类修改，是否放弃？',
      '离开编辑',
      {
        confirmButtonText: uncertainCreate.value ? '离开并核对' : '放弃修改',
        cancelButtonText: '继续编辑',
        type: 'warning',
      },
    )
    return true
  } catch {
    return false
  }
}

async function closeEditor(done?: () => void) {
  if (busy.value || !(await discardChanges())) return
  if (uncertainCreate.value) {
    manager.close(true)
    await manager.open()
  } else {
    active.value = false
    error.value = null
  }
  done?.()
}

function start(category?: ExpenseCategory) {
  if (loading.value || locked.value) return
  feedback.value = null
  manager.start(category)
}

async function remove() {
  const category = baseline.value
  if (!category || locked.value) return
  try {
    await ElMessageBox.confirm(
      `删除“${category.name}”？分类在所有旅行中共用，已被账目使用的分类不能删除。${dirty.value ? '当前未保存的修改也将放弃。' : ''}`,
      '删除分类',
      { confirmButtonText: '删除分类', cancelButtonText: '保留分类', type: 'warning' },
    )
  } catch {
    return
  }
  await manager.remove(category, true)
}

async function adoptLatest() {
  if (!busy.value && (await discardChanges())) manager.adoptLatest()
}
</script>

<template>
  <div class="mobile-categories-page">
    <RouterLink class="category-back" :to="{ name: 'account' }">
      <ArrowLeft aria-hidden="true" />返回我的
    </RouterLink>
    <header class="category-heading">
      <h1>账单分类管理</h1>
      <p>所有旅行共用。点击分类可编辑，按住拖动可调整记账时的顺序。</p>
    </header>

    <ElAlert v-if="feedback" :title="feedback" type="success" show-icon @close="feedback = null" />
    <ElAlert v-if="error && !active" :title="error" type="error" :closable="false" show-icon />

    <div class="category-toolbar">
      <span>{{
        loading ? '正在加载分类…' : reordering ? '正在保存顺序…' : `共 ${items.length} 个分类`
      }}</span>
      <ElButton type="primary" :disabled="loading || locked" @click="start()">
        <Plus aria-hidden="true" />新增分类
      </ElButton>
    </div>

    <ElAlert v-if="loadError" type="error" :closable="false" show-icon>
      <template #title>{{ loadError }}</template>
      <ElButton :loading="loading" :disabled="locked" @click="manager.load">重新加载</ElButton>
    </ElAlert>
    <div
      v-if="loading && !items.length"
      class="category-loading tf-surface"
      aria-label="正在加载分类"
    >
      <ElSkeleton :rows="6" animated />
    </div>
    <section v-else-if="!loading && !loadError && !items.length" class="category-empty tf-surface">
      <Tag aria-hidden="true" />
      <h2>还没有账单分类</h2>
      <p>点击「新增分类」，添加第一个常用分类。</p>
    </section>
    <VueDraggable
      v-if="cards.length"
      v-model="cards"
      tag="ul"
      class="category-grid tf-surface"
      :class="{ 'category-grid--busy': reordering }"
      ghost-class="category-card--ghost"
      chosen-class="category-card--chosen"
      drag-class="category-card--drag"
      :animation="150"
      :force-fallback="true"
      :delay="150"
      :touch-start-threshold="3"
      :disabled="loading || locked"
      :aria-busy="loading || reordering"
      aria-label="账单分类，按住卡片拖动可调整顺序"
      @start="onStart"
      @end="onEnd"
    >
      <li v-for="(category, index) in cards" :key="category.id" class="category-card">
        <button
          type="button"
          :aria-label="`编辑分类：${category.name}${category.is_preset ? '（预设）' : ''}，按 Shift+方向键调整顺序`"
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

    <ResponsiveEditorShell
      :model-value="active"
      :title="baseline ? '编辑分类' : '新增分类'"
      :before-close="closeEditor"
      :close-on-press-escape="!busy"
      class="mobile-category-editor"
      desktop-width="min(480px, calc(100vw - 32px))"
    >
      <ElAlert
        v-if="error"
        :title="error"
        :type="uncertainCreate ? 'warning' : 'error'"
        :closable="false"
        show-icon
        class="category-editor-alert"
      />
      <ElAlert
        v-if="metadata.status !== 'ready' && !uncertainCreate"
        title="分类图标尚未加载"
        type="warning"
        :closable="false"
        class="category-editor-alert"
      >
        <ElButton :loading="metadata.status === 'loading'" @click="metadata.load"
          >加载图标</ElButton
        >
      </ElAlert>
      <p v-if="baseline?.is_preset" class="category-editor-hint">
        预设分类，改名、换图标与删除规则和自定义分类相同。
      </p>
      <ElForm label-position="top" :disabled="locked" @submit.prevent="manager.save(conflict)">
        <ElFormItem label="分类名称" required :error="errors.name">
          <ElInput
            v-model="draft.name"
            maxlength="40"
            show-word-limit
            placeholder="例如：咖啡"
            aria-label="分类名称"
          />
        </ElFormItem>
        <ElFormItem label="分类图标" :error="errors.icon">
          <CategoryIconPicker
            v-model="draft.icon"
            :icons="metadata.metadata?.expense_category_icons ?? []"
            :disabled="locked"
          />
        </ElFormItem>
      </ElForm>
      <section v-if="conflict" class="category-conflict" aria-live="polite">
        <p>分类已在其他地方修改，你的输入已保留。</p>
        <ElSkeleton v-if="loadingLatest" :rows="2" animated />
        <p v-if="latest">最新内容：{{ latest.name }} · {{ categoryIconLabel(latest.icon) }}</p>
        <ElButton
          v-if="!latest"
          :loading="loadingLatest"
          :disabled="busy"
          @click="manager.loadLatest"
          >重新加载最新分类</ElButton
        >
        <ElButton v-else :disabled="busy" @click="adoptLatest">放弃输入并载入最新分类</ElButton>
      </section>
      <template #footer>
        <div class="category-editor-actions">
          <ElButton
            v-if="baseline"
            type="danger"
            plain
            :loading="!!deleting"
            :disabled="saving || reordering || uncertainCreate"
            @click="remove"
            ><Trash2 aria-hidden="true" />删除</ElButton
          >
          <ElButton
            type="primary"
            :loading="saving"
            :disabled="saveDisabled"
            @click="manager.save(conflict)"
          >
            {{ uncertainCreate ? '重试创建' : conflict ? '确认保存我的修改' : '保存分类' }}
          </ElButton>
        </div>
      </template>
    </ResponsiveEditorShell>
  </div>
</template>

<style scoped>
.mobile-categories-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 640px;
  margin: 0 auto;
  padding: 16px 16px 32px;
}
.category-back {
  display: inline-flex;
  align-items: center;
  align-self: flex-start;
  gap: 7px;
  min-height: 44px;
  color: var(--tf-text-2);
  font-size: 14px;
  font-weight: 600;
  text-decoration: none;
}
.category-back svg {
  width: 20px;
  height: 20px;
}
.category-heading h1 {
  margin: 0;
  font-size: 25px;
  line-height: 1.3;
}
.category-heading p {
  margin: 8px 0 0;
  color: var(--tf-text-2);
  font-size: 14px;
  line-height: 1.6;
}
.category-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}
.category-toolbar > span {
  color: var(--tf-text-2);
  font-size: 13px;
}
.category-toolbar .el-button {
  min-height: 44px;
  border-radius: var(--tf-radius-control);
}
.category-toolbar svg,
.category-editor-actions svg {
  width: 18px;
  height: 18px;
  margin-right: 6px;
}
.category-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 18px 8px;
  margin: 0;
  padding: 20px 12px;
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
  gap: 7px;
  width: 100%;
  min-width: 0;
  padding: 4px 0;
  border: 0;
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
    transform var(--tf-duration-fast) var(--tf-ease),
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
.category-card > button:active:not(:disabled) .category-card__symbol {
  background: var(--tf-accent-soft);
}
.category-card > button:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 4px;
  border-radius: 8px;
}
.category-card > button:disabled {
  cursor: default;
  opacity: 0.5;
}
.category-card--chosen .category-card__symbol {
  transform: scale(1.1);
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
.category-loading {
  padding: 24px;
}
.category-empty {
  padding: 36px 16px;
  text-align: center;
}
.category-empty > svg {
  width: 32px;
  height: 32px;
  color: var(--tf-accent);
}
.category-empty h2 {
  margin: 16px 0 8px;
  font-size: 18px;
}
.category-empty p {
  margin: 0;
  font-size: 13px;
  color: var(--tf-text-2);
  line-height: 1.7;
}
.category-editor-alert {
  margin-bottom: 16px;
}
.category-editor-hint {
  margin: 0 0 14px;
  color: var(--tf-text-3);
  font-size: 12px;
  line-height: 1.7;
}
:deep(.el-input__wrapper) {
  min-height: 42px;
}
:deep(.el-input__inner) {
  font-size: 16px;
}
.category-conflict {
  padding: 12px;
  margin-bottom: 12px;
  border-radius: var(--tf-radius-control);
  background: var(--tf-warning-soft);
  font-size: 13px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.category-conflict p {
  margin: 0 0 12px;
}
.category-editor-actions {
  display: flex;
  gap: 12px;
  width: 100%;
}
.category-editor-actions .el-button {
  min-height: 46px;
  margin: 0;
}
.category-editor-actions .el-button--primary {
  flex: 1;
}
</style>

<style>
.tf-editor-shell.mobile-category-editor.el-drawer {
  height: auto !important;
  max-height: min(92dvh, 760px);
}
.tf-editor-shell.mobile-category-editor .el-drawer__footer {
  padding: 12px 16px max(16px, env(safe-area-inset-bottom));
}
</style>
