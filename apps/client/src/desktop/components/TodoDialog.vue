<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElDatePicker,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessageBox,
  ElSkeleton,
  ElSwitch,
} from 'element-plus'
import { computed } from 'vue'

import { createTodo, getTodo, updateTodo, type Todo } from '@/shared/api/todos'
import type { WriteOutcome } from '@/shared/api/writes'
import {
  changedTodoFields,
  emptyTodoDraft,
  todoDraftFrom,
  todoFieldLabels,
  validateTodoDraft,
  type TodoDraft,
} from '@/shared/travel/todoDraft'
import { useTripContext } from '@/shared/travel/tripContext'
import { useItemEditor } from '@/shared/travel/useItemEditor'

const emit = defineEmits<{ saved: [outcome: WriteOutcome<Todo>] }>()
const context = useTripContext()

const editor = useItemEditor<Todo, TodoDraft, ReturnType<typeof validateTodoDraft>, object>({
  emptyDraft: emptyTodoDraft,
  draftFrom: todoDraftFrom,
  validate: validateTodoDraft,
  diff: changedTodoFields,
  get: (id) => getTodo(context.tripId, id),
  create: (body, operationId) => createTodo(context.tripId, body, operationId),
  update: (id, version, patch, operationId) =>
    updateTodo(context.tripId, id, version, patch, operationId),
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
  const original = todoDraftFrom(baseline.value)
  const theirs = todoDraftFrom(latest.value)
  const show = (v: unknown) =>
    typeof v === 'boolean' ? (v ? '已完成' : '未完成') : String(v || '（空）')
  return (Object.keys(original) as Array<keyof TodoDraft>)
    .filter((field) => draft[field] !== original[field])
    .map((field) => ({
      field,
      label: todoFieldLabels[field] ?? field,
      mine: show(draft[field]),
      theirs: show(theirs[field]),
    }))
})

function setDue(value: unknown) {
  draft.due_on = typeof value === 'string' ? value : ''
}

async function requestClose(done?: () => void) {
  if (saving.value) return
  if (dirty.value || uncertainCreate.value) {
    try {
      await ElMessageBox.confirm(
        uncertainCreate.value
          ? '创建结果尚未确认，这条待办可能已经保存。关闭后请刷新列表核对。'
          : '尚有未保存的输入，关闭后将放弃这些输入。',
        '关闭待办编辑',
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

defineExpose({ open: (todo?: Todo) => editor.open(todo) })
</script>

<template>
  <ElDialog
    :model-value="opened"
    :title="isEditing ? '编辑待办' : '新建待办'"
    width="min(520px, calc(100vw - 32px))"
    :close-on-click-modal="false"
    :close-on-press-escape="!saving"
    :before-close="requestClose"
    destroy-on-close
  >
    <ElSkeleton v-if="loading" :rows="4" animated />
    <template v-else>
      <ElAlert
        v-if="error"
        :title="error"
        :type="uncertainCreate ? 'warning' : 'error'"
        :closable="false"
        show-icon
        class="editor-alert"
      />
      <ElButton v-if="isEditing && !baseline" @click="editor.load">重新加载待办</ElButton>
      <ElForm
        v-else
        label-position="top"
        :disabled="saving || uncertainCreate"
        @submit.prevent="save()"
      >
        <ElFormItem label="标题" required :error="errors.title">
          <ElInput
            v-model="draft.title"
            maxlength="200"
            autofocus
            placeholder="例如：购买旅行保险"
          />
        </ElFormItem>
        <div class="editor-columns">
          <ElFormItem label="截止日期" :error="errors.due_on">
            <ElDatePicker
              :model-value="draft.due_on"
              type="date"
              value-format="YYYY-MM-DD"
              format="YYYY-MM-DD"
              placeholder="可留空"
              clearable
              @update:model-value="setDue"
            />
          </ElFormItem>
          <ElFormItem label="完成状态">
            <ElSwitch v-model="draft.completed" active-text="已完成" inactive-text="未完成" />
          </ElFormItem>
        </div>
        <ElFormItem label="备注" :error="errors.notes">
          <ElInput
            v-model="draft.notes"
            type="textarea"
            :rows="3"
            maxlength="4000"
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
        <div class="conflict-actions">
          <ElButton :loading="loadingLatest" :disabled="saving" @click="editor.loadLatest"
            >刷新最新内容</ElButton
          >
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
        >{{ uncertainCreate ? '重试创建' : isEditing ? '保存修改' : '添加待办' }}</ElButton
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
.editor-columns :deep(.el-date-editor) {
  width: 100%;
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
