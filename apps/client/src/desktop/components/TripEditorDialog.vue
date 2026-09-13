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
  ElOption,
  ElSelect,
  ElSkeleton,
} from 'element-plus'
import { computed } from 'vue'

import type { Trip } from '@/shared/api/trips'
import type { WriteOutcome } from '@/shared/api/writes'
import { useMetadataStore } from '@/shared/stores/metadata'
import { draftFromTrip, tripFieldLabels, type TripField } from '@/shared/travel/tripDraft'
import { useTripEditor } from '@/shared/travel/useTripEditor'

const emit = defineEmits<{ saved: [outcome: WriteOutcome<Trip>] }>()
const metadata = useMetadataStore()
const editor = useTripEditor()
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
  const original = draftFromTrip(baseline.value)
  return (Object.keys(tripFieldLabels) as TripField[])
    .filter((field) => draft[field] !== original[field])
    .map((field) => ({
      field,
      label: tripFieldLabels[field],
      mine: draft[field] || (field === 'budget_amount' ? '未设置' : '（空）'),
      theirs: latest.value?.[field] ?? (field === 'budget_amount' ? '未设置' : '（空）'),
    }))
})

function setDate(field: 'start_date' | 'end_date', value: unknown) {
  draft[field] = typeof value === 'string' ? value : ''
}

async function requestClose(done?: () => void) {
  if (saving.value) return
  if (dirty.value || uncertainCreate.value) {
    try {
      await ElMessageBox.confirm(
        uncertainCreate.value
          ? '创建结果尚未确认，这趟旅行可能已经保存。关闭后请先刷新旅行列表核对，避免重复创建。'
          : '尚有未保存的输入，关闭后将放弃这些输入。',
        '关闭旅行编辑',
        {
          confirmButtonText: uncertainCreate.value ? '关闭并核对列表' : '放弃输入并关闭',
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

defineExpose({ open: editor.open })
</script>

<template>
  <ElDialog
    :model-value="opened"
    :title="isEditing ? '编辑旅行' : '新建旅行'"
    width="min(680px, calc(100vw - 32px))"
    class="trip-editor"
    :close-on-click-modal="false"
    :close-on-press-escape="!saving"
    :before-close="requestClose"
    destroy-on-close
  >
    <ElSkeleton v-if="loading" :rows="7" animated />
    <template v-else>
      <ElAlert
        v-if="error"
        :title="error"
        :type="uncertainCreate ? 'warning' : 'error'"
        :closable="false"
        show-icon
        class="editor-alert"
      />
      <ElButton v-if="isEditing && !baseline" @click="editor.load">重新加载旅行</ElButton>
      <template v-else>
        <ElAlert
          v-if="metadata.status !== 'ready' && !uncertainCreate"
          type="warning"
          :closable="false"
          class="editor-alert"
        >
          <template #title>正在等待币种信息，加载完成后才能保存。</template>
          <p v-if="metadata.error">{{ metadata.error }}</p>
          <ElButton :loading="metadata.status === 'loading'" @click="metadata.load"
            >重新加载币种</ElButton
          >
        </ElAlert>
        <ElAlert
          v-if="baseline?.archived_at"
          title="这趟旅行已归档，仍可编辑和修正记录。"
          type="info"
          :closable="false"
          class="editor-alert"
        />
        <ElForm label-position="top" :disabled="saving || uncertainCreate" @submit.prevent="save()">
          <ElFormItem label="旅行名称" required :error="errors.name">
            <ElInput
              v-model="draft.name"
              maxlength="120"
              autofocus
              placeholder="例如：秋日京都之旅"
            />
          </ElFormItem>
          <div class="editor-columns">
            <ElFormItem label="开始日期" required :error="errors.start_date">
              <ElDatePicker
                :model-value="draft.start_date"
                type="date"
                value-format="YYYY-MM-DD"
                format="YYYY-MM-DD"
                placeholder="选择开始日期"
                @update:model-value="setDate('start_date', $event)"
              />
            </ElFormItem>
            <ElFormItem label="结束日期" required :error="errors.end_date">
              <ElDatePicker
                :model-value="draft.end_date"
                type="date"
                value-format="YYYY-MM-DD"
                format="YYYY-MM-DD"
                placeholder="选择结束日期"
                @update:model-value="setDate('end_date', $event)"
              />
            </ElFormItem>
          </div>
          <p v-if="isEditing" class="editor-hint">
            修改日期会保留已有行程和记录，超出新日期的安排将在保存后提示。
          </p>
          <ElFormItem label="目的地" :error="errors.destination">
            <ElInput
              v-model="draft.destination"
              maxlength="300"
              placeholder="城市、地区或多个目的地"
            />
          </ElFormItem>
          <ElFormItem label="旅行时区" required :error="errors.timezone">
            <ElInput v-model="draft.timezone" maxlength="64" placeholder="Asia/Shanghai" />
            <span class="editor-hint"
              >使用 IANA 时区，例如 Asia/Shanghai、Asia/Tokyo；旅行阶段按该时区计算。</span
            >
          </ElFormItem>
          <div class="editor-columns">
            <ElFormItem label="记账币种" required :error="errors.currency_code">
              <ElSelect
                v-model="draft.currency_code"
                filterable
                :disabled="!!baseline?.currency_locked_at"
                aria-label="记账币种"
              >
                <ElOption
                  v-for="currency in metadata.metadata?.currencies ?? []"
                  :key="currency.code"
                  :label="currency.code"
                  :value="currency.code"
                />
              </ElSelect>
            </ElFormItem>
            <ElFormItem label="总预算" :error="errors.budget_amount">
              <ElInput
                v-model="draft.budget_amount"
                inputmode="decimal"
                placeholder="留空表示未设置"
                clearable
              >
                <template #append>{{ draft.currency_code }}</template>
              </ElInput>
            </ElFormItem>
          </div>
          <p class="editor-hint">预算留空表示未设置，输入 0 表示零预算。金额不会随币种自动换算。</p>
          <ElAlert
            v-if="baseline?.currency_locked_at"
            title="已有有效账目，币种已锁定；删除全部有效账目后可更改。"
            type="info"
            :closable="false"
            class="editor-alert"
          />
          <ElFormItem label="备注" :error="errors.notes">
            <ElInput
              v-model="draft.notes"
              type="textarea"
              :rows="3"
              maxlength="10000"
              show-word-limit
              placeholder="记录这趟旅行的想法或注意事项"
            />
          </ElFormItem>
          <button type="submit" class="visually-hidden" tabindex="-1" aria-hidden="true">
            保存
          </button>
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
    </template>
    <template #footer>
      <ElButton :disabled="saving" @click="requestClose()">取消</ElButton>
      <ElButton
        v-if="conflict"
        type="primary"
        :loading="saving"
        :disabled="!latest || loadingLatest || metadata.status !== 'ready'"
        @click="save(true)"
        >确认用我的改动更新最新版本</ElButton
      >
      <ElButton
        v-else
        type="primary"
        :loading="saving"
        :disabled="
          loading ||
          (!uncertainCreate && metadata.status !== 'ready') ||
          (isEditing && (!baseline || !dirty))
        "
        @click="save()"
        >{{ uncertainCreate ? '重试创建' : isEditing ? '保存修改' : '创建旅行' }}</ElButton
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
.editor-columns :deep(.el-date-editor),
.editor-columns :deep(.el-select) {
  width: 100%;
}
.editor-hint {
  color: var(--tf-text-3);
  font-size: 12px;
  line-height: 1.6;
  margin: 0 0 14px;
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
