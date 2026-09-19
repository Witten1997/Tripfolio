<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCheckbox,
  ElCheckboxGroup,
  ElDatePicker,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSkeleton,
} from 'element-plus'
import { computed, ref, watch } from 'vue'

import ResponsiveEditorShell from '@/desktop/components/ResponsiveEditorShell.vue'
import SlidingSegmented from '@/desktop/components/SlidingSegmented.vue'
import type { ExpenseCategory } from '@/shared/api/categories'
import {
  createLedgerEntry,
  getLedgerEntry,
  ledgerKindLabels,
  listAllLedgerEntries,
  updateLedgerEntry,
  type LedgerEntry,
  type LedgerKind,
} from '@/shared/api/ledger'
import { listTripMembers, memberName, type TripMember } from '@/shared/api/members'
import { type WriteOutcome } from '@/shared/api/writes'
import {
  changedLedgerFields,
  defaultMemberDraft,
  emptyLedgerDraft,
  ledgerDraftFrom,
  ledgerFieldLabels,
  splitModeLabels,
  splitPreview,
  validateLedgerDraft,
  type LedgerDraft,
  type SplitMode,
} from '@/shared/travel/ledgerDraft'
import { formatMoney } from '@/shared/travel/statisticsView'
import { useTripContext } from '@/shared/travel/tripContext'
import { useItemEditor } from '@/shared/travel/useItemEditor'

const props = defineProps<{ categories: ExpenseCategory[] }>()
const emit = defineEmits<{
  saved: [outcome: WriteOutcome<LedgerEntry>]
  'update:opened': [opened: boolean]
}>()
const context = useTripContext()
const currency = computed(() => context.trip.value?.currency_code ?? 'CNY')

/** 旅行成员：打开表单时拉取一次；成员管理保存后由页面调用 refreshMembers。 */
const members = ref<TripMember[]>([])
const loadingMembers = ref(false)
const membersError = ref<string | null>(null)
async function refreshMembers() {
  loadingMembers.value = true
  membersError.value = null
  try {
    members.value = await listTripMembers(context.tripId)
  } catch {
    membersError.value = '无法加载旅行成员，付款人与参与人暂不可选'
  } finally {
    loadingMembers.value = false
  }
}
const splitModeOptions = (Object.keys(splitModeLabels) as SplitMode[]).map((value) => ({
  value,
  label: splitModeLabels[value],
}))
function setSplitMode(value: string) {
  if (value === 'even' || value === 'ratio') draft.split_mode = value
}
const participants = computed(() =>
  draft.participant_member_ids
    .map((id) => members.value.find((m) => m.id === id))
    .filter((m): m is TripMember => !!m),
)
const preview = computed(() =>
  splitPreview(draft.amount, draft.split_mode, participants.value, context.minorUnits.value),
)
const previewRows = computed(() =>
  participants.value.map((m) => ({
    id: m.id,
    name: m.name,
    amount: preview.value?.get(m.id) ?? null,
  })),
)
const ratioUnavailable = computed(
  () =>
    draft.split_mode === 'ratio' &&
    participants.value.length > 0 &&
    participants.value.every((m) => Number(m.share_percent) === 0),
)
function toggleAllParticipants() {
  draft.participant_member_ids =
    draft.participant_member_ids.length === members.value.length
      ? []
      : members.value.map((m) => m.id)
}

const editor = useItemEditor<
  LedgerEntry,
  LedgerDraft,
  ReturnType<typeof validateLedgerDraft>,
  object
>({
  emptyDraft: () => emptyLedgerDraft(),
  draftFrom: ledgerDraftFrom,
  validate: (draft) =>
    validateLedgerDraft(draft, { currency: currency.value, minorUnits: context.minorUnits.value }),
  diff: changedLedgerFields,
  get: (id) => getLedgerEntry(context.tripId, id),
  create: (body, operationId) => createLedgerEntry(context.tripId, body, operationId),
  update: (id, version, patch, operationId) =>
    updateLedgerEntry(context.tripId, id, version, patch, operationId),
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

const kinds = Object.keys(ledgerKindLabels) as LedgerKind[]
const kindOptions = kinds.map((value) => ({ value, label: ledgerKindLabels[value] }))
watch(opened, (value) => emit('update:opened', value), { flush: 'sync' })

/** 可关联的原支出：同旅行有效支出，退款时按需拉取一次。 */
const expenses = ref<LedgerEntry[]>([])
const loadingExpenses = ref(false)
async function ensureExpenses() {
  if (expenses.value.length || loadingExpenses.value) return
  loadingExpenses.value = true
  try {
    expenses.value = await listAllLedgerEntries(context.tripId, { kind: 'expense' })
  } catch {
    /* 关联可选，加载失败不阻塞记账 */
  } finally {
    loadingExpenses.value = false
  }
}
watch(
  () => draft.kind,
  (kind) => {
    if (kind === 'refund') void ensureExpenses()
    else draft.refunded_entry_id = ''
  },
)

const categoryName = (id: string) =>
  props.categories.find((c) => c.id === id)?.name ?? '（已删除分类）'

/** 编辑退款关联的原支出中，排除自己；分类须与原支出一致，选后自动带出并锁定分类。 */
const linkableExpenses = computed(() => expenses.value.filter((e) => e.id !== baseline.value?.id))
const categoryLocked = computed(() => draft.kind === 'refund' && !!draft.refunded_entry_id)
watch(
  () => draft.refunded_entry_id,
  (id) => {
    if (!id) return
    const origin = expenses.value.find((e) => e.id === id)
    if (origin) {
      draft.category_id = origin.category_id
      // 关联原支出时默认继承其付款人与参与人，与服务端缺省一致
      if (!isEditing.value) {
        draft.payer_member_id = origin.payer_member_id
        draft.split_mode = origin.split_mode
        draft.participant_member_ids = origin.splits.map((s) => s.member_id)
      }
    }
  },
)

/** 分类下拉：活跃分类 + 当前记录引用的已删除分类（避免编辑时丢失）。 */
const categoryOptions = computed(() => {
  const options = props.categories.map((c) => ({ id: c.id, name: c.name, disabled: false }))
  const current = draft.category_id
  if (current && !options.some((o) => o.id === current)) {
    options.push({ id: current, name: `${categoryName(current)}`, disabled: true })
  }
  return options
})

const conflictRows = computed(() => {
  if (!baseline.value || !latest.value) return []
  const mine = draft
  const original = ledgerDraftFrom(baseline.value)
  const theirs = ledgerDraftFrom(latest.value)
  const display = (field: keyof LedgerDraft, value: string) => {
    if (field === 'kind') return ledgerKindLabels[value as LedgerKind] ?? value
    if (field === 'category_id') return value ? categoryName(value) : '（空）'
    if (field === 'refunded_entry_id') return value ? '已关联原支出' : '未关联'
    if (field === 'amount') return value ? `${formatMoney(value)} ${currency.value}` : '（空）'
    if (field === 'payer_member_id') return memberName(members.value, value) || '（空）'
    if (field === 'split_mode') return splitModeLabels[value as SplitMode] ?? value
    if (field === 'participant_member_ids')
      return (
        value
          .split(',')
          .filter(Boolean)
          .map((id) => memberName(members.value, id))
          .join('、') || '（空）'
      )
    return value || '（空）'
  }
  return (Object.keys(original) as Array<keyof LedgerDraft>)
    .filter((field) => mine[field] !== original[field])
    .map((field) => ({
      field,
      label: ledgerFieldLabels[field] ?? field,
      mine: display(field, String(mine[field])),
      theirs: display(field, String(theirs[field])),
    }))
})

function setText(field: 'occurred_on', value: unknown) {
  draft[field] = typeof value === 'string' ? value : ''
}

async function requestClose(done?: () => void) {
  if (saving.value) return
  if (dirty.value || uncertainCreate.value) {
    try {
      await ElMessageBox.confirm(
        uncertainCreate.value
          ? '创建结果尚未确认，这笔账目可能已经保存。关闭后请刷新账单核对，避免重复记账。'
          : '尚有未保存的输入，关闭后将放弃这些输入。',
        '关闭记账',
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
    /* 用户保留草稿 */
  }
}

async function save(againstLatest = false) {
  const outcome = await editor.save(againstLatest)
  if (outcome) emit('saved', outcome)
}

/** 地点入口可以预填日期与备注；编辑既有账目时不覆盖原值。新建时付款人默认「我」、参与人默认全员。 */
async function open(
  entry?: LedgerEntry,
  kind?: LedgerKind,
  presets: Partial<Pick<LedgerDraft, 'occurred_on' | 'notes'>> = {},
) {
  if (!members.value.length) await refreshMembers()
  return editor.open(
    entry,
    entry
      ? {}
      : {
          occurred_on: context.today.value,
          kind: kind ?? 'expense',
          ...defaultMemberDraft(members.value),
          ...presets,
        },
  )
}

defineExpose({ open, refreshMembers })
</script>

<template>
  <ResponsiveEditorShell
    :model-value="opened"
    :title="isEditing ? '编辑账目' : draft.kind === 'refund' ? '记录退款' : '记一笔'"
    desktop-width="min(560px, calc(100vw - 32px))"
    :close-on-click-modal="false"
    :close-on-press-escape="!saving"
    :before-close="requestClose"
  >
    <ElSkeleton v-if="loading" :rows="6" animated />
    <template v-else>
      <ElAlert
        v-if="error"
        :title="error"
        :type="uncertainCreate ? 'warning' : 'error'"
        :closable="false"
        show-icon
        class="editor-alert"
      />
      <ElButton v-if="isEditing && !baseline" @click="editor.load">重新加载账目</ElButton>
      <ElForm
        v-else
        label-position="top"
        :disabled="saving || uncertainCreate"
        @submit.prevent="save()"
      >
        <ElFormItem label="类型" :error="errors.kind">
          <SlidingSegmented
            v-model="draft.kind"
            :options="kindOptions"
            :disabled="isEditing"
            label="类型"
          />
          <span v-if="isEditing" class="editor-hint">账目类型保存后不可更改。</span>
        </ElFormItem>
        <div class="amount-row">
          <ElFormItem label="金额" required :error="errors.amount">
            <ElInput
              v-model="draft.amount"
              class="amount-input"
              inputmode="decimal"
              placeholder="0.00"
              autofocus
            >
              <template #suffix
                ><span class="amount-currency">{{ currency }}</span></template
              >
            </ElInput>
            <span v-if="draft.participant_member_ids.length > 1" class="editor-hint">
              此处填写整笔总金额。
            </span>
          </ElFormItem>
          <ElFormItem
            :label="draft.kind === 'refund' ? '收款人' : '付款人'"
            required
            :error="errors.payer_member_id"
          >
            <ElSelect
              v-model="draft.payer_member_id"
              :loading="loadingMembers"
              :placeholder="draft.kind === 'refund' ? '谁收到退款' : '谁付的钱'"
              :aria-label="draft.kind === 'refund' ? '收款人' : '付款人'"
            >
              <ElOption v-for="m in members" :key="m.id" :value="m.id" :label="m.name" />
            </ElSelect>
          </ElFormItem>
        </div>
        <ElAlert
          v-if="membersError"
          :title="membersError"
          type="warning"
          :closable="false"
          show-icon
          class="editor-alert"
        >
          <ElButton size="small" :loading="loadingMembers" @click="refreshMembers">重试</ElButton>
        </ElAlert>
        <ElFormItem label="分摊模式" :error="errors.split_mode">
          <SlidingSegmented
            :model-value="draft.split_mode"
            :options="splitModeOptions"
            label="分摊模式"
            @update:model-value="setSplitMode"
          />
          <span v-if="ratioUnavailable" class="editor-hint editor-hint--warn">
            所选参与人的百分比之和为 0，无法按比例分摊；请在成员管理中设置比例或改用均摊。
          </span>
        </ElFormItem>
        <ElFormItem :error="errors.participant_member_ids">
          <template #label>
            <span class="participants-label">
              参与人
              <ElButton
                link
                size="small"
                :disabled="!members.length"
                @click="toggleAllParticipants"
                >{{
                  draft.participant_member_ids.length === members.length ? '清空' : '全选'
                }}</ElButton
              >
            </span>
          </template>
          <ElCheckboxGroup
            v-model="draft.participant_member_ids"
            class="participants"
            aria-label="参与人"
          >
            <ElCheckbox v-for="m in members" :key="m.id" :value="m.id" :label="m.id">
              {{ m.name }}
              <span v-if="draft.split_mode === 'ratio'" class="participant-percent"
                >{{ m.share_percent }}%</span
              >
            </ElCheckbox>
          </ElCheckboxGroup>
          <ul v-if="preview && previewRows.length > 1" class="split-preview" aria-label="分摊预览">
            <li v-for="row in previewRows" :key="row.id">
              <span>{{ row.name }}</span
              ><strong v-if="row.amount">{{ formatMoney(row.amount) }} {{ currency }}</strong>
            </li>
          </ul>
        </ElFormItem>
        <div class="editor-columns editor-columns--single">
          <ElFormItem label="实际日期" required :error="errors.occurred_on">
            <ElDatePicker
              :model-value="draft.occurred_on"
              type="date"
              :editable="false"
              value-format="YYYY-MM-DD"
              format="YYYY-MM-DD"
              placeholder="选择日期"
              @update:model-value="setText('occurred_on', $event)"
            />
          </ElFormItem>
        </div>
        <ElFormItem
          v-if="draft.kind === 'refund'"
          label="关联原支出（可选）"
          :error="errors.refunded_entry_id"
        >
          <ElSelect
            v-model="draft.refunded_entry_id"
            clearable
            filterable
            :loading="loadingExpenses"
            placeholder="独立退款可不关联"
            aria-label="关联原支出"
          >
            <ElOption
              v-for="expense in linkableExpenses"
              :key="expense.id"
              :value="expense.id"
              :label="`${expense.occurred_on} · ${formatMoney(expense.amount)} ${currency} · ${categoryName(expense.category_id)}${expense.notes ? ' · ' + expense.notes : ''}`"
            />
          </ElSelect>
          <span class="editor-hint">关联后分类与原支出一致，退款冲减该分类的净支出。</span>
        </ElFormItem>
        <ElFormItem label="账单分类" required :error="errors.category_id">
          <ElSelect
            v-model="draft.category_id"
            filterable
            :disabled="categoryLocked"
            placeholder="选择分类"
            aria-label="账单分类"
          >
            <ElOption
              v-for="option in categoryOptions"
              :key="option.id"
              :value="option.id"
              :label="option.name"
              :disabled="option.disabled"
            />
          </ElSelect>
          <span v-if="categoryLocked" class="editor-hint">已按关联的原支出锁定分类。</span>
        </ElFormItem>
        <ElFormItem label="备注" :error="errors.notes">
          <ElInput
            v-model="draft.notes"
            type="textarea"
            :rows="2"
            maxlength="4000"
            show-word-limit
            placeholder="可选"
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
        >{{ uncertainCreate ? '重试保存' : isEditing ? '保存修改' : '保存' }}</ElButton
      >
    </template>
  </ResponsiveEditorShell>
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
.editor-columns--single {
  grid-template-columns: minmax(0, 1fr);
}
.editor-columns :deep(.el-date-editor),
.editor-columns :deep(.el-select) {
  width: 100%;
}
.amount-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 148px;
  gap: 20px;
}
.amount-currency {
  color: var(--tf-text-3);
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
}
.editor-hint--warn {
  color: var(--tf-warning);
}
.participants-label {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.participants {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
}
.participant-percent {
  margin-left: 4px;
  color: var(--tf-text-3);
  font-size: 12px;
}
.split-preview {
  list-style: none;
  margin: 8px 0 0;
  padding: 8px 12px;
  width: 100%;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: 4px 16px;
  background: var(--tf-surface-sunken);
  border-radius: var(--tf-radius-control);
  font-size: 12px;
}
.split-preview li {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  line-height: 1.8;
}
.editor-hint {
  display: block;
  color: var(--tf-text-3);
  font-size: 12px;
  line-height: 1.6;
  margin: 2px 0 0;
}
.conflict-panel {
  background: var(--tf-warning-soft);
  border: 1px solid var(--tf-warning);
  border-radius: var(--tf-radius-control);
  padding: 16px;
  margin-top: 8px;
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
  width: 84px;
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
@media (max-width: 560px) {
  .editor-columns {
    grid-template-columns: 1fr;
    gap: 0;
  }
  .amount-row {
    grid-template-columns: minmax(0, 1fr) 116px;
    gap: 12px;
  }
}
</style>
