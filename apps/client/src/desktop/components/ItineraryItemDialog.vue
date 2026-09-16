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
import { computed, nextTick, onScopeDispose, ref, useId, watch } from 'vue'

import ActionIcon from '@/desktop/components/ActionIcon.vue'
import IconAction from '@/desktop/components/IconAction.vue'
import LedgerEntryDialog from '@/desktop/components/LedgerEntryDialog.vue'
import PlacePicker from '@/desktop/components/PlacePicker.vue'
import { listCategories, type ExpenseCategory } from '@/shared/api/categories'
import type { GeoPlace } from '@/shared/api/geo'
import type { LedgerEntry } from '@/shared/api/ledger'

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
import { actionError, writeWarnings, type WriteOutcome } from '@/shared/api/writes'
import {
  applyItineraryPlace,
  changedItineraryFields,
  clearItineraryPlace,
  emptyItineraryDraft,
  itineraryDraftFrom,
  itineraryExpensePreset,
  itineraryFieldLabels,
  validateItineraryDraft,
  type ItineraryDraft,
} from '@/shared/travel/itineraryDraft'
import { DraftError } from '@/shared/travel/tripDraft'
import { useTripContext } from '@/shared/travel/tripContext'
import { dayTitle } from '@/shared/travel/tripDays'
import { useItemEditor, type ItemEditor } from '@/shared/travel/useItemEditor'

const emit = defineEmits<{ saved: [outcome: WriteOutcome<ItineraryItem>] }>()
const context = useTripContext()
const currency = computed(() => context.trip.value?.currency_code ?? 'CNY')
const locatingPlace = ref(false)
const detailsOpened = ref(false)
const detailsId = `${useId()}-details`
const form = ref<InstanceType<typeof ElForm>>()
const placePicker = ref<InstanceType<typeof PlacePicker>>()
const ledgerDialog = ref<InstanceType<typeof LedgerEntryDialog>>()
const ledgerOpened = ref(false)
const loadingExpense = ref(false)
const expenseCategories = ref<ExpenseCategory[]>([])
const expenseError = ref('')
const expenseNotice = ref('')
let expenseGeneration = 0

const editor: ItemEditor<ItineraryItem, ItineraryDraft> = useItemEditor<
  ItineraryItem,
  ItineraryDraft,
  ReturnType<typeof validateItineraryDraft>,
  object
>({
  emptyDraft: () => emptyItineraryDraft(),
  draftFrom: itineraryDraftFrom,
  validate: (draft) =>
    validateItineraryDraft(
      draft,
      {
        currency: currency.value,
        minorUnits: context.minorUnits.value,
      },
      !editor.isEditing.value,
    ),
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
const locationError = computed(
  () =>
    errors.value.place_name ||
    errors.value.address ||
    errors.value.latitude ||
    errors.value.longitude ||
    (errors.value.title ? '请重新选择地点，行程名称将自动填写' : ''),
)
const detailFields = ['notes', 'status', 'actual_start_local', 'actual_end_local', 'actual_notes']
watch(errors, (value) => {
  if (detailFields.some((field) => value[field])) detailsOpened.value = true
})
watch(opened, (value) => {
  if (!value) expenseGeneration++
})
onScopeDispose(() => expenseGeneration++)

function choosePlace(place: GeoPlace) {
  applyItineraryPlace(draft, place)
  for (const field of ['title', 'place_name', 'address', 'latitude', 'longitude'])
    delete errors.value[field]
  expenseError.value = ''
}

function clearPlace() {
  clearItineraryPlace(draft)
  expenseError.value = ''
}

async function recordExpense() {
  if (
    saving.value ||
    uncertainCreate.value ||
    locatingPlace.value ||
    loadingExpense.value ||
    ledgerOpened.value
  )
    return
  expenseError.value = ''
  let presets: ReturnType<typeof itineraryExpensePreset>
  try {
    presets = itineraryExpensePreset(draft)
  } catch (cause) {
    if (cause instanceof DraftError) {
      errors.value = { ...errors.value, ...cause.fields }
      expenseError.value = Object.values(cause.fields).join('；')
      await focusFirstError()
    }
    return
  }
  const request = ++expenseGeneration
  loadingExpense.value = true
  try {
    const categories = await listCategories()
    if (!opened.value || request !== expenseGeneration) return
    if (!categories.length) {
      expenseError.value = '还没有可用账单分类，请先在账单页添加分类。'
      return
    }
    expenseCategories.value = categories
    await nextTick()
    if (opened.value && request === expenseGeneration)
      await ledgerDialog.value?.open(undefined, 'expense', presets)
  } catch (cause) {
    if (opened.value && request === expenseGeneration)
      expenseError.value = actionError(cause, '无法加载账单分类，请再次点击加号重试。')
  } finally {
    if (request === expenseGeneration) loadingExpense.value = false
  }
}

async function expenseSaved(outcome: WriteOutcome<LedgerEntry>) {
  expenseNotice.value = ['账目已保存，可在账单页查看。', ...writeWarnings(outcome.result)].join(' ')
  // 第一笔账目会锁定币种；保留行程草稿，仅更新旅行上下文。
  await context.reload()
}

async function focusFirstError() {
  await nextTick()
  if (locationError.value) placePicker.value?.focus()
  else
    (form.value?.$el as HTMLElement | undefined)
      ?.querySelector<HTMLElement>(
        '.el-form-item.is-error input, .el-form-item.is-error textarea, .el-form-item.is-error [tabindex="0"]',
      )
      ?.focus()
}

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
  if (saving.value || ledgerOpened.value) return
  // 即使关闭确认尚未结束，也不能让迟到的分类请求突然打开第二个弹窗。
  expenseGeneration++
  loadingExpense.value = false
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
  if (locatingPlace.value || loadingExpense.value || ledgerOpened.value) return
  const outcome = await editor.save(againstLatest)
  if (outcome) emit('saved', outcome)
  else await focusFirstError()
}

/** 新建时可传入所属日期（从某一天的“添加”按钮进入）。 */
function open(item?: ItineraryItem, scheduledOn?: string) {
  if (saving.value || uncertainCreate.value || ledgerOpened.value) return
  expenseGeneration++
  detailsOpened.value = false
  loadingExpense.value = false
  expenseError.value = expenseNotice.value = ''
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
    :close-on-press-escape="!saving && !ledgerOpened"
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
        ref="form"
        label-position="top"
        :disabled="saving || uncertainCreate || loadingExpense || ledgerOpened"
        @submit.prevent="save()"
      >
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
        <PlacePicker
          ref="placePicker"
          :value="draft"
          :city="context.trip.value?.destination?.slice(0, 40) ?? ''"
          :disabled="saving || uncertainCreate || loadingExpense || ledgerOpened"
          :validation-error="locationError"
          @select="choosePlace"
          @clear="clearPlace"
          @locating="locatingPlace = $event"
        />
        <p v-if="isEditing && !draft.place_name && draft.title" class="editor-hint">
          原有行程：{{ draft.title }}。可继续编辑，或选点补充地图位置。
        </p>
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
        <div class="expense-action tf-actions">
          <span>记录该地点花费</span>
          <IconAction
            icon="plus"
            label="记录该地点花费"
            :loading="loadingExpense"
            :disabled="saving || uncertainCreate || locatingPlace || ledgerOpened"
            @click="recordExpense"
          />
        </div>
        <p class="editor-hint">
          自动带入地点和所属日期；账单单独保存，取消行程不会撤销已保存的账单。
        </p>
        <p v-if="expenseError" class="coordinate-error" role="alert">{{ expenseError }}</p>
        <p class="expense-notice" role="status">{{ expenseNotice }}</p>
        <div class="details-action tf-actions">
          <ElButton
            :aria-expanded="detailsOpened"
            :aria-controls="detailsId"
            @click="detailsOpened = !detailsOpened"
          >
            <ActionIcon :name="detailsOpened ? 'chevron-up' : 'chevron-down'" />
            <span>{{ detailsOpened ? '收起详情' : '展开详情' }}</span>
          </ElButton>
        </div>
        <div v-show="detailsOpened" :id="detailsId">
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
        </div>
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
      <ElButton :disabled="saving || ledgerOpened" @click="requestClose()">取消</ElButton>
      <ElButton
        v-if="conflict"
        type="primary"
        :loading="saving || locatingPlace"
        :disabled="!latest || loadingLatest || locatingPlace || loadingExpense || ledgerOpened"
        @click="save(true)"
        >确认用我的改动更新最新版本</ElButton
      >
      <ElButton
        v-else
        type="primary"
        :loading="saving || locatingPlace"
        :disabled="
          loading ||
          locatingPlace ||
          loadingExpense ||
          ledgerOpened ||
          (isEditing && (!baseline || !dirty))
        "
        @click="save()"
        >{{ uncertainCreate ? '重试创建' : isEditing ? '保存修改' : '添加行程' }}</ElButton
      >
    </template>
  </ElDialog>
  <LedgerEntryDialog
    ref="ledgerDialog"
    :categories="expenseCategories"
    @update:opened="ledgerOpened = $event"
    @saved="expenseSaved"
  />
</template>

<style scoped>
.editor-alert {
  margin-bottom: 16px;
}
.coordinate-error {
  color: var(--tf-danger);
  font-size: 13px;
}
.expense-action {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 6px;
  color: var(--tf-text-1);
}
.expense-notice {
  color: var(--tf-text-2);
  font-size: 13px;
  line-height: 1.6;
}
.expense-notice:empty {
  margin: 0;
}
.details-action {
  margin: 16px 0;
}
.details-action .el-button :deep(> span) {
  gap: 6px;
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
