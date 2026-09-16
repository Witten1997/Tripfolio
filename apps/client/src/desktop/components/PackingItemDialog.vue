<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessageBox,
  ElOption,
  ElRadioButton,
  ElRadioGroup,
  ElSelect,
  ElSkeleton,
} from 'element-plus'
import { computed } from 'vue'

import {
  createPackingItem,
  getPackingItem,
  packingCategoryLabels,
  packingCategoryOrder,
  packingStatusLabels,
  packingStatusOrder,
  updatePackingItem,
  type PackingCategory,
  type PackingItem,
} from '@/shared/api/packing'
import type { WriteOutcome } from '@/shared/api/writes'
import {
  changedPackingFields,
  emptyPackingDraft,
  packingDraftFrom,
  packingFieldLabels,
  validatePackingDraft,
  type PackingDraft,
} from '@/shared/travel/packingDraft'
import { useTripContext } from '@/shared/travel/tripContext'
import { useItemEditor } from '@/shared/travel/useItemEditor'

const emit = defineEmits<{ saved: [outcome: WriteOutcome<PackingItem>] }>()
const context = useTripContext()

const editor = useItemEditor<
  PackingItem,
  PackingDraft,
  ReturnType<typeof validatePackingDraft>,
  object
>({
  emptyDraft: () => emptyPackingDraft(),
  draftFrom: packingDraftFrom,
  validate: validatePackingDraft,
  diff: changedPackingFields,
  get: (id) => getPackingItem(context.tripId, id),
  create: (body, operationId) => createPackingItem(context.tripId, body, operationId),
  update: (id, version, patch, operationId) =>
    updatePackingItem(context.tripId, id, version, patch, operationId),
})
const {
  opened,
  loading,
  saving,
  uncertainCreate,
  loadingLatest,
  baseline,
  latest,
  conflict,
  error,
  latestError,
  errors,
  draft,
  dirty,
  isEditing,
} = editor

const conflictRows = computed(() => {
  if (!baseline.value || !latest.value) return []
  const original = packingDraftFrom(baseline.value)
  const theirs = packingDraftFrom(latest.value)
  return (Object.keys(original) as Array<keyof PackingDraft>)
    .filter((field) => draft[field] !== original[field])
    .map((field) => ({
      field,
      label: packingFieldLabels[field] ?? field,
      mine: String(draft[field] || '（空）'),
      theirs: String(theirs[field] || '（空）'),
    }))
})

async function requestClose(done?: () => void) {
  if (saving.value) return
  if (dirty.value || uncertainCreate.value) {
    try {
      await ElMessageBox.confirm(
        uncertainCreate.value
          ? '创建结果尚未确认，这件物品可能已经保存。关闭后请刷新清单核对。'
          : '尚有未保存的输入，关闭后将放弃这些输入。',
        '关闭物品编辑',
        {
          confirmButtonText: uncertainCreate.value ? '关闭并核对' : '放弃输入并关闭',
          cancelButtonText: uncertainCreate.value ? '返回重试' : '继续编辑',
          type: 'warning',
        },
      )
    } catch {
      return
    }
  }
  editor.close(true)
  done?.()
}

async function adoptLatest() {
  try {
    await ElMessageBox.confirm('本次输入将被最新保存的内容替换。', '载入最新版本', {
      confirmButtonText: '放弃输入并载入',
      cancelButtonText: '保留输入',
      type: 'warning',
    })
    editor.adoptLatest()
  } catch {
    /* 用户保留草稿。 */
  }
}

async function save(againstLatest = false) {
  const outcome = await editor.save(againstLatest)
  if (outcome) emit('saved', outcome)
}

function open(item?: PackingItem, category?: PackingCategory) {
  return editor.open(item, category ? { category } : {})
}

defineExpose({ open })
</script>

<template>
  <ElDialog
    :model-value="opened"
    :title="isEditing ? '编辑物品' : '添加物品'"
    width="min(560px, calc(100vw - 32px))"
    :close-on-click-modal="false"
    :close-on-press-escape="!saving"
    :before-close="requestClose"
    destroy-on-close
  >
    <ElSkeleton v-if="loading" :rows="5" animated />
    <template v-else>
      <ElAlert
        v-if="error"
        :title="error"
        :type="uncertainCreate ? 'warning' : 'error'"
        :closable="false"
        show-icon
        class="editor-alert"
      />
      <ElButton v-if="isEditing && !baseline" @click="editor.load">重新加载物品</ElButton>
      <ElForm
        v-else
        label-position="top"
        :disabled="saving || uncertainCreate"
        @submit.prevent="save()"
      >
        <ElFormItem label="物品名称" required :error="errors.name">
          <ElInput v-model="draft.name" maxlength="120" autofocus placeholder="例如：护照" />
          <span class="editor-hint">同一分类下名称唯一，不区分大小写与首尾空格。</span>
        </ElFormItem>
        <div class="editor-columns">
          <ElFormItem label="分类" required :error="errors.category">
            <ElSelect v-model="draft.category" aria-label="分类">
              <ElOption
                v-for="category in packingCategoryOrder"
                :key="category"
                :label="packingCategoryLabels[category]"
                :value="category"
              />
            </ElSelect>
          </ElFormItem>
          <ElFormItem label="数量" required :error="errors.quantity">
            <ElInput v-model="draft.quantity" inputmode="numeric" placeholder="1" />
          </ElFormItem>
        </div>
        <ElFormItem label="状态" :error="errors.status">
          <ElRadioGroup v-model="draft.status" aria-label="状态">
            <ElRadioButton v-for="status in packingStatusOrder" :key="status" :value="status">{{
              packingStatusLabels[status]
            }}</ElRadioButton>
          </ElRadioGroup>
        </ElFormItem>
        <ElFormItem label="备注" :error="errors.notes">
          <ElInput
            v-model="draft.notes"
            type="textarea"
            :rows="2"
            maxlength="2000"
            show-word-limit
          />
        </ElFormItem>
        <button type="submit" class="visually-hidden" tabindex="-1" aria-hidden="true">保存</button>
      </ElForm>
      <section v-if="conflict" class="conflict-panel" aria-live="polite">
        <h3>检查版本冲突</h3>
        <p>你的输入已保留。下方对比仅列出你修改过的字段；重新提交时只保存这些改动。</p>
        <ElSkeleton v-if="loadingLatest" :rows="2" animated />
        <ElAlert v-if="latestError" :title="latestError" type="error" :closable="false" />
        <table v-if="latest" class="conflict-table">
          <thead>
            <tr>
              <th>字段</th>
              <th>我的输入</th>
              <th>最新保存内容</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in conflictRows" :key="row.field">
              <th>{{ row.label }}</th>
              <td>{{ row.mine }}</td>
              <td>{{ row.theirs }}</td>
            </tr>
          </tbody>
        </table>
        <div class="conflict-actions tf-actions">
          <ElButton :disabled="!latest || saving" @click="adoptLatest"
            >放弃输入，载入最新版本</ElButton
          >
        </div>
      </section>
    </template>
    <template #footer>
      <ElButton :disabled="saving" @click="requestClose()">取消</ElButton>
      <ElButton
        v-if="conflict"
        type="primary"
        :loading="saving"
        :disabled="!latest || loadingLatest"
        @click="save(true)"
        >确认用我的改动更新最新版本</ElButton
      >
      <ElButton
        v-else
        type="primary"
        :loading="saving"
        :disabled="loading || (isEditing && (!baseline || !dirty))"
        @click="save()"
        >{{ uncertainCreate ? '重试添加' : isEditing ? '保存修改' : '添加物品' }}</ElButton
      >
    </template>
  </ElDialog>
</template>

<style scoped>
.editor-alert {
  margin-bottom: 16px;
}
.editor-columns {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 20px;
}
.editor-columns :deep(.el-select) {
  width: 100%;
}
.editor-hint {
  display: block;
  color: var(--tf-text-3);
  font-size: 12px;
  line-height: 1.6;
}
.conflict-panel {
  background: var(--tf-warning-soft);
  border: 1px solid var(--tf-warning);
  border-radius: var(--tf-radius-control);
  padding: 16px;
}
.conflict-panel h3 {
  margin: 0 0 8px;
  font-size: 15px;
}
.conflict-panel p {
  line-height: 1.6;
  margin: 0 0 12px;
}
.conflict-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
  font-size: 12px;
}
.conflict-table th,
.conflict-table td {
  text-align: left;
  vertical-align: top;
  border-bottom: 1px solid var(--tf-line);
  padding: 8px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.conflict-table th:first-child {
  width: 68px;
}
.conflict-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
}
.conflict-actions .el-button {
  margin-left: 0;
}
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}
@media (max-width: 600px) {
  .editor-columns {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>
