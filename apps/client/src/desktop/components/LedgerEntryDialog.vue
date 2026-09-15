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
import { computed, ref, watch } from 'vue'

import IconAction from '@/desktop/components/IconAction.vue'
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
import { type WriteOutcome } from '@/shared/api/writes'
import {
  changedLedgerFields,
  emptyLedgerDraft,
  ledgerDraftFrom,
  ledgerFieldLabels,
  validateLedgerDraft,
  type LedgerDraft,
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
    if (origin) draft.category_id = origin.category_id
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

/** 地点入口可以预填日期与备注；编辑既有账目时不覆盖原值。 */
function open(
  entry?: LedgerEntry,
  kind?: LedgerKind,
  presets: Partial<Pick<LedgerDraft, 'occurred_on' | 'notes'>> = {},
) {
  return editor.open(
    entry,
    entry ? {} : { occurred_on: context.today.value, kind: kind ?? 'expense', ...presets },
  )
}

defineExpose({ open })
</script>

<template>
  <ElDialog
    :model-value="opened"
    :title="isEditing ? '编辑账目' : draft.kind === 'refund' ? '记录退款' : '记一笔'"
    width="min(560px, calc(100vw - 32px))"
    :close-on-click-modal="false"
    :close-on-press-escape="!saving"
    :before-close="requestClose"
    append-to-body
    destroy-on-close
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
          <ElRadioGroup v-model="draft.kind" :disabled="isEditing" aria-label="类型">
            <ElRadioButton v-for="kind in kinds" :key="kind" :value="kind">{{
              ledgerKindLabels[kind]
            }}</ElRadioButton>
          </ElRadioGroup>
          <span v-if="isEditing" class="editor-hint">账目类型保存后不可更改。</span>
        </ElFormItem>
        <div class="editor-columns">
          <ElFormItem label="金额" required :error="errors.amount">
            <ElInput v-model="draft.amount" inputmode="decimal" placeholder="0.00" autofocus>
              <template #append>{{ currency }}</template>
            </ElInput>
          </ElFormItem>
          <ElFormItem label="实际日期" required :error="errors.occurred_on">
            <ElDatePicker
              :model-value="draft.occurred_on"
              type="date"
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
          <IconAction
            icon="refresh"
            label="刷新账目最新内容"
            :loading="loadingLatest"
            :disabled="saving"
            @click="editor.loadLatest"
          />
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
        >{{ uncertainCreate ? '重试保存' : isEditing ? '保存修改' : '保存' }}</ElButton
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
}
</style>
