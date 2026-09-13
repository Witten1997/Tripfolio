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
  ElRadioButton,
  ElRadioGroup,
  ElSelect,
  ElSkeleton,
} from 'element-plus'
import { computed } from 'vue'

import {
  createItineraryItem,
  getItineraryItem,
  itineraryKindLabels,
  itineraryStatusLabels,
  updateItineraryItem,
  type ItineraryItem,
  type ItineraryKind,
  type ItineraryStatus,
} from '@/shared/api/itinerary'
import type { WriteOutcome } from '@/shared/api/writes'
import {
  changedItineraryFields,
  emptyItineraryDraft,
  itineraryDraftFrom,
  itineraryFieldLabels,
  validateItineraryDraft,
  type ItineraryDraft,
} from '@/shared/travel/itineraryDraft'
import { useTripContext } from '@/shared/travel/tripContext'
import { dayTitle } from '@/shared/travel/tripDays'
import { useItemEditor } from '@/shared/travel/useItemEditor'

const emit = defineEmits<{ saved: [outcome: WriteOutcome<ItineraryItem>] }>()
const context = useTripContext()
const currency = computed(() => context.trip.value?.currency_code ?? 'CNY')

const editor = useItemEditor<
  ItineraryItem,
  ItineraryDraft,
  ReturnType<typeof validateItineraryDraft>,
  object
>({
  emptyDraft: () => emptyItineraryDraft(),
  draftFrom: itineraryDraftFrom,
  validate: (draft) =>
    validateItineraryDraft(draft, {
      currency: currency.value,
      minorUnits: context.minorUnits.value,
    }),
  diff: changedItineraryFields,
  get: (id) => getItineraryItem(context.tripId, id),
  create: (body, operationId) => createItineraryItem(context.tripId, body, operationId),
  update: (id, version, patch, operationId) =>
    updateItineraryItem(context.tripId, id, version, patch, operationId),
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

const kinds = Object.keys(itineraryKindLabels) as ItineraryKind[]
const statuses = Object.keys(itineraryStatusLabels) as ItineraryStatus[]

const conflictRows = computed(() => {
  if (!baseline.value || !latest.value) return []
  const mine = draft
  const original = itineraryDraftFrom(baseline.value)
  const theirs = itineraryDraftFrom(latest.value)
  return (Object.keys(original) as Array<keyof ItineraryDraft>)
    .filter((field) => mine[field] !== original[field])
    .map((field) => ({
      field,
      label: itineraryFieldLabels[draftFieldToContract(field)] ?? field,
      mine: String(mine[field] || '（空）'),
      theirs: String(theirs[field] || '（空）'),
    }))
})

function draftFieldToContract(field: keyof ItineraryDraft): string {
  const map: Partial<Record<keyof ItineraryDraft, string>> = {
    planned_start: 'planned_start_local',
    planned_end: 'planned_end_local',
    planned_mode: 'planned_duration_minutes',
    actual_start: 'actual_start_local',
    actual_end: 'actual_end_local',
  }
  return map[field] ?? field
}

type TextField = 'scheduled_on' | 'planned_start' | 'planned_end' | 'actual_start' | 'actual_end'
function setText(field: TextField, value: unknown) {
  draft[field] = typeof value === 'string' ? value : ''
}

async function requestClose(done?: () => void) {
  if (saving.value) return
  if (dirty.value || uncertainCreate.value) {
    try {
      await ElMessageBox.confirm(
        uncertainCreate.value
          ? '创建结果尚未确认，这条行程可能已经保存。关闭后请刷新行程核对，避免重复创建。'
          : '尚有未保存的输入，关闭后将放弃这些输入。',
        '关闭行程编辑',
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

/** 新建时可传入所属日期（从某一天的“添加”按钮进入）。 */
function open(item?: ItineraryItem, scheduledOn?: string) {
  return editor.open(item, scheduledOn ? { scheduled_on: scheduledOn } : {})
}

defineExpose({ open })
</script>

<template>
  <ElDialog
    :model-value="opened"
    :title="isEditing ? '编辑行程' : '新建行程'"
    width="min(720px, calc(100vw - 32px))"
    :close-on-click-modal="false"
    :close-on-press-escape="!saving"
    :before-close="requestClose"
    destroy-on-close
  >
    <ElSkeleton v-if="loading" :rows="8" animated />
    <template v-else>
      <ElAlert
        v-if="error"
        :title="error"
        :type="uncertainCreate ? 'warning' : 'error'"
        :closable="false"
        show-icon
        class="editor-alert"
      />
      <ElButton v-if="isEditing && !baseline" @click="editor.load">重新加载行程</ElButton>
      <ElForm
        v-else
        label-position="top"
        :disabled="saving || uncertainCreate"
        @submit.prevent="save()"
      >
        <ElFormItem label="标题" required :error="errors.title">
          <ElInput v-model="draft.title" maxlength="200" autofocus placeholder="例如：浅草寺" />
        </ElFormItem>
        <div class="editor-columns">
          <ElFormItem label="类型" required :error="errors.kind">
            <ElSelect v-model="draft.kind" aria-label="类型">
              <ElOption
                v-for="kind in kinds"
                :key="kind"
                :label="itineraryKindLabels[kind]"
                :value="kind"
              />
            </ElSelect>
          </ElFormItem>
          <ElFormItem label="所属日期" required :error="errors.scheduled_on">
            <ElDatePicker
              :model-value="draft.scheduled_on"
              type="date"
              value-format="YYYY-MM-DD"
              format="YYYY-MM-DD"
              :disabled="isEditing"
              placeholder="选择日期"
              @update:model-value="setText('scheduled_on', $event)"
            />
            <span v-if="isEditing" class="editor-hint"
              >已保存的行程在行程页拖动到其他日期；这里不修改日期。</span
            >
            <span v-else-if="draft.scheduled_on" class="editor-hint">{{
              dayTitle(draft.scheduled_on)
            }}</span>
          </ElFormItem>
        </div>
        <fieldset class="editor-group">
          <legend>计划时间</legend>
          <div class="editor-columns">
            <ElFormItem label="计划开始" :error="errors.planned_start_local">
              <ElDatePicker
                :model-value="draft.planned_start"
                type="datetime"
                value-format="YYYY-MM-DD HH:mm"
                format="YYYY-MM-DD HH:mm"
                placeholder="可留空"
                @update:model-value="setText('planned_start', $event)"
              />
            </ElFormItem>
            <ElFormItem label="结束方式">
              <ElRadioGroup v-model="draft.planned_mode" aria-label="结束方式">
                <ElRadioButton value="none">不设置</ElRadioButton>
                <ElRadioButton value="end">结束时间</ElRadioButton>
                <ElRadioButton value="duration">停留时长</ElRadioButton>
              </ElRadioGroup>
            </ElFormItem>
          </div>
          <ElFormItem
            v-if="draft.planned_mode === 'end'"
            label="计划结束"
            :error="errors.planned_end_local"
          >
            <ElDatePicker
              :model-value="draft.planned_end"
              type="datetime"
              value-format="YYYY-MM-DD HH:mm"
              format="YYYY-MM-DD HH:mm"
              placeholder="可跨日"
              @update:model-value="setText('planned_end', $event)"
            />
          </ElFormItem>
          <ElFormItem
            v-else-if="draft.planned_mode === 'duration'"
            label="停留时长（分钟）"
            :error="errors.planned_duration_minutes"
          >
            <ElInput
              v-model="draft.planned_duration_minutes"
              inputmode="numeric"
              placeholder="例如 90"
            />
          </ElFormItem>
          <p class="editor-hint">时间按旅行时区 {{ context.trip.value?.timezone }} 解释。</p>
        </fieldset>
        <div class="editor-columns">
          <ElFormItem label="地点" :error="errors.place_name">
            <ElInput v-model="draft.place_name" maxlength="200" placeholder="地点名称" />
          </ElFormItem>
          <ElFormItem label="地址" :error="errors.address">
            <ElInput v-model="draft.address" maxlength="500" placeholder="手工填写地址" />
          </ElFormItem>
        </div>
        <ElFormItem label="预计费用" :error="errors.estimated_amount">
          <ElInput
            v-model="draft.estimated_amount"
            inputmode="decimal"
            placeholder="留空表示未估算"
            clearable
          >
            <template #append>{{ currency }}</template>
          </ElInput>
          <span class="editor-hint">预计费用不进入实际开支统计，币种与旅行一致。</span>
        </ElFormItem>
        <ElFormItem label="备注" :error="errors.notes">
          <ElInput
            v-model="draft.notes"
            type="textarea"
            :rows="2"
            maxlength="10000"
            show-word-limit
          />
        </ElFormItem>
        <fieldset class="editor-group">
          <legend>实际情况</legend>
          <ElFormItem label="状态" :error="errors.status">
            <ElRadioGroup v-model="draft.status" aria-label="状态">
              <ElRadioButton v-for="status in statuses" :key="status" :value="status">{{
                itineraryStatusLabels[status]
              }}</ElRadioButton>
            </ElRadioGroup>
          </ElFormItem>
          <div class="editor-columns">
            <ElFormItem label="实际开始" :error="errors.actual_start_local">
              <ElDatePicker
                :model-value="draft.actual_start"
                type="datetime"
                value-format="YYYY-MM-DD HH:mm"
                format="YYYY-MM-DD HH:mm"
                placeholder="可留空"
                @update:model-value="setText('actual_start', $event)"
              />
            </ElFormItem>
            <ElFormItem label="实际结束" :error="errors.actual_end_local">
              <ElDatePicker
                :model-value="draft.actual_end"
                type="datetime"
                value-format="YYYY-MM-DD HH:mm"
                format="YYYY-MM-DD HH:mm"
                placeholder="可留空"
                @update:model-value="setText('actual_end', $event)"
              />
            </ElFormItem>
          </div>
          <ElFormItem label="实际记录" :error="errors.actual_notes">
            <ElInput
              v-model="draft.actual_notes"
              type="textarea"
              :rows="2"
              maxlength="10000"
              show-word-limit
              placeholder="与计划的差异、当时的感受"
            />
          </ElFormItem>
        </fieldset>
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
        >{{ uncertainCreate ? '重试创建' : isEditing ? '保存修改' : '添加行程' }}</ElButton
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
.editor-group {
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  padding: 12px 16px 0;
  margin: 0 0 18px;
}
.editor-group legend {
  padding: 0 6px;
  font-size: 13px;
  color: var(--tf-text-2);
}
.editor-hint {
  display: block;
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
