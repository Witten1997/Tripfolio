<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElDialog,
  ElEmpty,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSkeleton,
  ElTag,
} from 'element-plus'

import type { ExpenseCategory } from '@/shared/api/categories'
import { useMetadataStore } from '@/shared/stores/metadata'
import {
  categoryIconLabel,
  categoryIcons,
  useCategoryManager,
} from '@/shared/travel/useCategoryManager'

const metadata = useMetadataStore()
const manager = useCategoryManager()
const {
  opened,
  loading,
  saving,
  uncertainCreate,
  deleting,
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
  if (saving.value || deleting.value || !(await discardChanges(true))) return
  manager.close(true)
  done?.()
}

async function start(category?: ExpenseCategory) {
  if (saving.value || deleting.value || uncertainCreate.value) return
  if (await discardChanges()) manager.start(category)
}

async function remove(category: ExpenseCategory) {
  if (saving.value || deleting.value || uncertainCreate.value) return
  try {
    await ElMessageBox.confirm(
      `删除账号共用的“${category.name}”分类？被账目使用的分类不能删除，预设分类也遵循此规则。`,
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
    :close-on-press-escape="!saving && !deleting"
    :before-close="requestClose"
    destroy-on-close
  >
    <p class="category-intro">
      分类在本账号的所有旅行中共用。预设分类也可以改名、调整图标与排序，未被账目使用时可删除。
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
      v-if="error"
      :title="error"
      :type="uncertainCreate ? 'warning' : 'error'"
      :closable="false"
      show-icon
      class="category-alert"
    />
    <div class="category-toolbar">
      <span>{{ items.length }} 个分类</span>
      <div>
        <ElButton
          :loading="loading"
          :disabled="saving || !!deleting || uncertainCreate"
          @click="manager.load"
          >刷新</ElButton
        ><ElButton
          type="primary"
          :disabled="saving || !!deleting || loading || uncertainCreate"
          @click="start()"
          >新增分类</ElButton
        >
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
        <div
          v-for="category in items"
          :key="category.id"
          class="category-row"
          :class="{ selected: active && baseline?.id === category.id }"
        >
          <span class="category-symbol" aria-hidden="true">{{
            category.icon ? (categoryIcons[category.icon]?.symbol ?? '●') : '○'
          }}</span>
          <div class="category-copy">
            <strong>{{ category.name }}</strong
            ><span>排序 {{ category.sort_order }} · {{ categoryIconLabel(category.icon) }}</span>
          </div>
          <ElTag v-if="category.is_preset" size="small" type="info">预设</ElTag>
          <div class="category-actions">
            <ElButton
              size="small"
              link
              type="primary"
              :disabled="saving || !!deleting || uncertainCreate"
              :aria-label="`编辑分类${category.name}`"
              @click="start(category)"
              >编辑</ElButton
            ><ElButton
              size="small"
              link
              type="danger"
              :loading="deleting === category.id"
              :disabled="saving || (!!deleting && deleting !== category.id) || uncertainCreate"
              :aria-label="`删除分类${category.name}`"
              @click="remove(category)"
              >删除</ElButton
            >
          </div>
        </div>
      </div>
      <section v-if="active" class="category-editor">
        <h3>{{ baseline ? '编辑分类' : '新增分类' }}</h3>
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
          :disabled="saving || uncertainCreate"
          @submit.prevent="manager.save()"
        >
          <ElFormItem label="分类名称" required :error="errors.name"
            ><ElInput v-model="draft.name" maxlength="40" placeholder="例如：咖啡"
          /></ElFormItem>
          <ElFormItem label="分类图标" :error="errors.icon"
            ><ElSelect v-model="draft.icon" clearable placeholder="无图标" aria-label="分类图标"
              ><ElOption label="无图标" value="" /><ElOption
                v-for="icon in metadata.metadata?.expense_category_icons ?? []"
                :key="icon"
                :value="icon"
                :label="`${categoryIcons[icon]?.symbol ?? '●'} ${categoryIconLabel(icon)}`" /></ElSelect
          ></ElFormItem>
          <ElFormItem label="排序值" required :error="errors.sort_order"
            ><ElInputNumber
              v-model="draft.sort_order"
              :min="0"
              :max="2147483647"
              :precision="0"
              :step="1"
              controls-position="right"
              aria-label="排序值"
          /></ElFormItem>
          <p class="category-hint">数字越小越靠前，相同数值保持固定顺序。</p>
          <div v-if="conflict" class="category-conflict">
            <p>你的输入已保留。请检查最新分类后决定如何保存。</p>
            <ElSkeleton v-if="loadingLatest" :rows="2" animated />
            <p v-if="latest">
              最新内容：{{ latest.name }} · {{ categoryIconLabel(latest.icon) }} · 排序
              {{ latest.sort_order }}
            </p>
            <ElButton size="small" :loading="loadingLatest" @click="manager.loadLatest"
              >刷新最新内容</ElButton
            >
            <ElButton size="small" :disabled="!latest" @click="adoptLatest"
              >放弃输入并载入</ElButton
            >
          </div>
        </ElForm>
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
            saving ||
            (!uncertainCreate && metadata.status !== 'ready') ||
            (baseline !== null && !dirty)
          "
          >{{ uncertainCreate ? '重试创建' : '保存分类' }}</ElButton
        >
      </section>
    </div>
    <template #footer
      ><ElButton :disabled="saving || !!deleting" @click="requestClose()">关闭</ElButton></template
    >
  </ElDialog>
</template>

<style scoped>
.category-intro {
  margin: 0 0 20px;
  color: var(--el-text-color-secondary);
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
  color: var(--el-text-color-secondary);
}
.category-layout.has-editor {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 280px;
  gap: 24px;
}
.category-list {
  min-width: 0;
}
.category-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 8px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.category-row.selected {
  background: var(--el-color-primary-light-9);
}
.category-symbol {
  width: 28px;
  font-size: 22px;
  text-align: center;
  flex-shrink: 0;
}
.category-copy {
  display: flex;
  flex: 1;
  min-width: 0;
  flex-direction: column;
  gap: 5px;
}
.category-copy strong {
  overflow-wrap: anywhere;
}
.category-copy > span {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.category-actions {
  display: flex;
  flex-shrink: 0;
}
.category-editor {
  background: var(--el-fill-color-extra-light);
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
  padding: 16px;
  align-self: start;
}
.category-editor h3 {
  margin: 0 0 18px;
  font-size: 16px;
}
.category-editor :deep(.el-input-number),
.category-editor :deep(.el-select) {
  width: 100%;
}
.category-hint {
  font-size: 12px;
  line-height: 1.7;
  color: var(--el-text-color-secondary);
}
.category-conflict {
  font-size: 12px;
  line-height: 1.7;
  margin-bottom: 16px;
}
.category-conflict .el-button {
  margin: 4px 4px 0 0;
}
@media (max-width: 700px) {
  .category-layout.has-editor {
    grid-template-columns: 1fr;
  }
}
</style>
