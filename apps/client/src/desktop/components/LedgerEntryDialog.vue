<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCheckbox,
  ElCheckboxGroup,
  ElDatePicker,
  ElDrawer,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSkeleton,
} from 'element-plus'
import { NotebookPen, Plus, Users, ChevronDown, X } from '@lucide/vue'
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'

import LedgerAmountKeypad from '@/desktop/components/LedgerAmountKeypad.vue'
import CategoryCreateDrawer from '@/desktop/components/CategoryCreateDrawer.vue'
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
import { formatMoney, sumMoney } from '@/shared/travel/statisticsView'
import { categoryIconComponent } from '@/shared/travel/categoryIconVisuals'
import { useTripContext } from '@/shared/travel/tripContext'
import { useItemEditor } from '@/shared/travel/useItemEditor'

const props = defineProps<{ categories: ExpenseCategory[] }>()
const emit = defineEmits<{
  saved: [outcome: WriteOutcome<LedgerEntry>]
  'update:opened': [opened: boolean]
  'update:categories': [categories: ExpenseCategory[]]
}>()
const categoryCreator = ref<InstanceType<typeof CategoryCreateDrawer>>()
const categoryCreatorMounted = ref(false)
async function openCategoryCreator() {
  categoryCreatorMounted.value = true
  await nextTick()
  await categoryCreator.value?.open()
}
const availableCategories = shallowRef(props.categories)
watch(
  () => props.categories,
  (categories) => {
    availableCategories.value = categories
  },
)
function categoriesCreated(categories: ExpenseCategory[], selectedId: string | undefined) {
  availableCategories.value = categories
  if (selectedId && !categoryLocked.value) draft.category_id = selectedId
  emit('update:categories', categories)
}
const context = useTripContext()
const currency = computed(() => context.trip.value?.currency_code ?? 'CNY')
const isMobile = ref(false)
const notesFocused = ref(false)
const splitSettingsOpened = ref(false)
const refundOrigin = shallowRef<LedgerEntry | null>(null)
let mobileMediaQuery: MediaQueryList | undefined
function syncMobile() {
  isMobile.value = mobileMediaQuery?.matches ?? false
}
onMounted(() => {
  if (!window.matchMedia) return
  mobileMediaQuery = window.matchMedia('(max-width: 767px)')
  syncMobile()
  mobileMediaQuery.addEventListener('change', syncMobile)
})
onUnmounted(() => mobileMediaQuery?.removeEventListener('change', syncMobile))
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
  if (value !== 'even' && value !== 'ratio' && value !== 'personal') return
  if (value === draft.split_mode) return
  if (value === 'personal') {
    draft.payer_member_id = members.value.find((m) => m.is_self)?.id ?? ''
    draft.participant_member_ids = draft.payer_member_id ? [draft.payer_member_id] : []
  } else if (draft.split_mode === 'personal') {
    draft.participant_member_ids = members.value.map((m) => m.id)
  }
  draft.split_mode = value
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
    validateLedgerDraft(
      draft.split_mode === 'personal'
        ? { ...draft, payer_member_id: members.value.find((m) => m.is_self)?.id ?? '' }
        : draft,
      { currency: currency.value, minorUnits: context.minorUnits.value },
    ),
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

watch(
  opened,
  (value) => {
    notesFocused.value = false
    if (!value) splitSettingsOpened.value = false
    emit('update:opened', value)
  },
  { flush: 'sync' },
)

const categoryName = (id: string) =>
  availableCategories.value.find((c) => c.id === id)?.name ?? '（已删除分类）'

const categoryLocked = computed(() => draft.kind === 'refund' && !!draft.refunded_entry_id)
const submitDisabled = computed(
  () =>
    loading.value ||
    (isEditing.value && (!baseline.value || !dirty.value)) ||
    (conflict.value && (!latest.value || loadingLatest.value)),
)
const submitLabel = computed(() =>
  uncertainCreate.value ? '重试保存' : conflict.value ? '确认保存' : '保存',
)
const amountDisplay = computed(
  () =>
    draft.amount || (context.minorUnits.value ? `0.${'0'.repeat(context.minorUnits.value)}` : '0'),
)

/** 分类下拉：活跃分类 + 当前记录引用的已删除分类（避免编辑时丢失）。 */
const categoryOptions = computed(() => {
  const options = availableCategories.value.map((c) => ({
    id: c.id,
    name: c.name,
    icon: c.icon,
    disabled: false,
  }))
  const current = draft.category_id
  if (current && !options.some((o) => o.id === current)) {
    options.push({ id: current, name: `${categoryName(current)}`, icon: null, disabled: true })
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
  if (isMobile.value && !uncertainCreate.value && draft.amount.endsWith('.'))
    draft.amount = draft.amount.slice(0, -1)
  const outcome = await editor.save(againstLatest)
  if (outcome) emit('saved', outcome)
  else if (
    errors.value.participant_member_ids ||
    errors.value.payer_member_id ||
    errors.value.split_mode
  )
    splitSettingsOpened.value = true
}

/** 地点入口可以预填日期与备注；编辑既有账目时不覆盖原值。新建时付款人默认「我」、参与人默认全员。 */
async function open(
  entry?: LedgerEntry,
  kind?: LedgerKind,
  presets: Partial<Pick<LedgerDraft, 'occurred_on' | 'notes'>> = {},
) {
  if (!entry && kind === 'refund') return
  refundOrigin.value = null
  splitSettingsOpened.value = false
  if (!members.value.length) await refreshMembers()
  await editor.open(
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

async function openRefund(origin: LedgerEntry) {
  if (saving.value || uncertainCreate.value || origin.kind !== 'expense') return
  try {
    const [current, refunds] = await Promise.all([
      getLedgerEntry(context.tripId, origin.id),
      listAllLedgerEntries(context.tripId, { kind: 'refund', refunded_entry_id: origin.id }),
    ])
    const remaining = sumMoney([current.amount, ...refunds.map((r) => `-${r.amount}`)])
    if (!/[1-9]/.test(remaining) || remaining.startsWith('-')) {
      ElMessage.info('这笔账单已全额退款')
      return
    }
    if (!members.value.length) await refreshMembers()
    refundOrigin.value = current
    splitSettingsOpened.value = false
    await editor.open(undefined, {
      kind: 'refund',
      amount: remaining,
      category_id: current.category_id,
      occurred_on: context.today.value,
      refunded_entry_id: current.id,
      payer_member_id: current.payer_member_id,
      split_mode: current.split_mode,
      participant_member_ids: current.splits.map((s) => s.member_id),
    })
  } catch {
    ElMessage.error('无法加载原账单及退款记录，请重试')
  }
}

defineExpose({ open, openRefund, refreshMembers })
</script>

<template>
  <ResponsiveEditorShell
    :class="{ 'ledger-mobile-shell': isMobile }"
    :model-value="opened"
    :title="
      draft.kind === 'refund'
        ? isEditing
          ? '编辑退款'
          : '记录退款'
        : isEditing
          ? '编辑账目'
          : '记一笔'
    "
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
        <div v-if="isMobile" class="ledger-categories" role="group" aria-label="账单分类">
          <button
            v-for="category in categoryOptions"
            :key="category.id"
            type="button"
            :aria-pressed="draft.category_id === category.id"
            :disabled="saving || uncertainCreate || category.disabled || categoryLocked"
            @click="draft.category_id = category.id"
          >
            <span class="ledger-category-icon"
              ><component :is="categoryIconComponent(category.icon)" aria-hidden="true"
            /></span>
            <span>{{ category.name }}</span>
          </button>
          <button
            type="button"
            :disabled="saving || uncertainCreate || categoryLocked"
            @click="openCategoryCreator"
          >
            <span class="ledger-category-icon"><Plus aria-hidden="true" /></span>
            <span>新增分类</span>
          </button>
        </div>
        <p v-if="isMobile && errors.category_id" class="mobile-field-error">
          {{ errors.category_id }}
        </p>
        <p v-if="draft.kind === 'refund'" class="refund-origin">
          退款计入原账单<span v-if="refundOrigin">
            · {{ categoryName(refundOrigin.category_id) }} {{ formatMoney(refundOrigin.amount) }}
            {{ currency }}</span
          >
        </p>
        <div
          v-if="!isMobile"
          class="amount-row"
          :class="{ 'amount-row--personal': draft.split_mode === 'personal' }"
        >
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
            <span
              v-if="draft.split_mode !== 'personal' && draft.participant_member_ids.length > 1"
              class="editor-hint"
            >
              此处填写整笔总金额。
            </span>
          </ElFormItem>
          <ElFormItem
            v-if="draft.split_mode !== 'personal'"
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
        <component
          :is="isMobile ? ElDrawer : 'div'"
          v-bind="
            isMobile
              ? {
                  modelValue: splitSettingsOpened,
                  direction: 'btt',
                  size: 'auto',
                  appendToBody: true,
                  showClose: false,
                  destroyOnClose: true,
                  class: 'tf-editor-shell ledger-split-drawer',
                }
              : {}
          "
          @update:model-value="splitSettingsOpened = $event"
        >
          <template v-if="isMobile" #header="{ titleId, titleClass }">
            <div class="tf-editor-shell__header">
              <h2 :id="titleId" :class="titleClass"><Users aria-hidden="true" />分摊模式</h2>
              <button type="button" aria-label="关闭分摊模式" @click="splitSettingsOpened = false">
                <X aria-hidden="true" />
              </button>
            </div>
          </template>
          <ElFormItem label="分摊模式" :error="errors.split_mode">
            <SlidingSegmented
              :model-value="draft.split_mode"
              :options="splitModeOptions"
              :disabled="saving || uncertainCreate"
              label="分摊模式"
              @update:model-value="setSplitMode"
            />
            <span v-if="ratioUnavailable" class="editor-hint editor-hint--warn">
              所选参与人的百分比之和为 0，无法按比例分摊；请在成员管理中设置比例或改用均摊。
            </span>
            <span v-if="draft.split_mode === 'personal'" class="editor-hint">
              {{
                draft.kind === 'refund' ? '退款由「我」收取' : '由「我」支付并承担全额'
              }}，不参与分摊和成员结算。
            </span>
          </ElFormItem>
          <ElFormItem
            v-if="isMobile && draft.split_mode !== 'personal'"
            :label="draft.kind === 'refund' ? '收款人' : '付款人'"
            :error="errors.payer_member_id"
          >
            <ElSelect
              v-model="draft.payer_member_id"
              :loading="loadingMembers"
              :aria-label="draft.kind === 'refund' ? '收款人' : '付款人'"
            >
              <ElOption v-for="m in members" :key="m.id" :value="m.id" :label="m.name" />
            </ElSelect>
          </ElFormItem>
          <ElFormItem v-if="draft.split_mode !== 'personal'" :error="errors.participant_member_ids">
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
            <ul
              v-if="preview && previewRows.length > 1"
              class="split-preview"
              aria-label="分摊预览"
            >
              <li v-for="row in previewRows" :key="row.id">
                <span>{{ row.name }}</span
                ><strong v-if="row.amount">{{ formatMoney(row.amount) }} {{ currency }}</strong>
              </li>
            </ul>
          </ElFormItem>
          <template v-if="isMobile" #footer>
            <ElButton type="primary" @click="splitSettingsOpened = false">完成</ElButton>
          </template>
        </component>
        <div v-if="!isMobile" class="editor-columns editor-columns--single">
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
        <ElFormItem v-if="!isMobile" label="账单分类" required :error="errors.category_id">
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
        <ElFormItem v-if="!isMobile" label="备注" :error="errors.notes">
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
      <div v-if="isMobile" class="ledger-mobile-dock">
        <div class="ledger-amount-line">
          <label class="ledger-notes-field">
            <NotebookPen aria-hidden="true" />
            <input
              v-model="draft.notes"
              aria-label="备注"
              placeholder="添加备注"
              maxlength="4000"
              enterkeyhint="done"
              :disabled="saving || uncertainCreate || loading"
              :aria-invalid="!!errors.notes"
              @focus="notesFocused = true"
              @blur="notesFocused = false"
              @keydown.enter.prevent="($event.target as HTMLInputElement).blur()"
            />
          </label>
          <div class="ledger-amount-display">
            <span>{{ currency }}</span
            ><output aria-label="金额" aria-live="polite">{{ amountDisplay }}</output>
          </div>
        </div>
        <p v-if="errors.notes" class="mobile-field-error">{{ errors.notes }}</p>
        <p v-if="errors.amount" class="mobile-field-error">{{ errors.amount }}</p>
        <div class="ledger-quick-actions">
          <ElDatePicker
            :model-value="draft.occurred_on"
            type="date"
            :editable="false"
            :clearable="false"
            :disabled="saving || uncertainCreate"
            value-format="YYYY-MM-DD"
            :format="draft.occurred_on === context.today.value ? '[今天]' : 'M月D日'"
            aria-label="选择账单日期"
            placeholder="选择日期"
            class="ledger-date-button"
            popper-class="ledger-mobile-calendar"
            @update:model-value="setText('occurred_on', $event)"
          />
          <button
            type="button"
            :disabled="saving || uncertainCreate"
            :aria-expanded="splitSettingsOpened"
            aria-label="选择分摊模式"
            @click="splitSettingsOpened = true"
          >
            <Users aria-hidden="true" />{{ splitModeLabels[draft.split_mode]
            }}<ChevronDown aria-hidden="true" />
          </button>
        </div>
        <p v-if="errors.occurred_on" class="mobile-field-error">{{ errors.occurred_on }}</p>
        <LedgerAmountKeypad
          v-if="!notesFocused"
          v-model="draft.amount"
          :minor-units="context.minorUnits.value"
          :disabled="saving || uncertainCreate || loading"
          :saving="saving"
          :submit-disabled="submitDisabled"
          :submit-label="submitLabel"
          @submit="save(conflict)"
        />
      </div>
      <template v-else>
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
    </template>
  </ResponsiveEditorShell>
  <CategoryCreateDrawer
    v-if="categoryCreatorMounted"
    ref="categoryCreator"
    @saved="categoriesCreated"
  />
</template>

<style scoped>
.ledger-categories {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 18px 8px;
  margin: 8px 0 24px;
}
.ledger-categories button {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 7px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--tf-text-2);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}
.ledger-category-icon {
  display: grid;
  width: 48px;
  height: 48px;
  place-items: center;
  border-radius: 18px;
  background: var(--tf-surface-sunken);
}
.ledger-category-icon svg {
  width: 23px;
  height: 23px;
  stroke-width: 1.6;
}
.ledger-categories button[aria-pressed='true'] {
  color: var(--tf-accent);
  font-weight: 600;
}
.ledger-categories button[aria-pressed='true'] .ledger-category-icon {
  background: var(--tf-accent-soft);
  box-shadow: inset 0 0 0 1.5px var(--tf-accent);
}
.ledger-categories button:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 4px;
  border-radius: 8px;
}
.ledger-categories button:disabled:not([aria-pressed='true']) {
  opacity: 0.45;
}
.ledger-mobile-dock {
  text-align: left;
}
.ledger-amount-line {
  display: flex;
  gap: 12px;
  align-items: center;
  justify-content: space-between;
  padding: 12px 2px;
}
.ledger-notes-field {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  flex: 1;
  max-width: 42%;
  padding: 8px 0;
  border: 0;
  background: transparent;
  color: var(--tf-text-3);
  font: inherit;
  font-size: 13px;
}
.ledger-notes-field input {
  min-width: 0;
  width: 100%;
  min-height: 28px;
  padding: 0;
  border: 0;
  outline: none;
  border-radius: 0;
  background: transparent;
  color: var(--tf-text-1);
  font: inherit;
  font-size: 16px;
}
.ledger-notes-field input::placeholder {
  color: var(--tf-text-3);
  font-size: 13px;
}
.ledger-notes-field:focus-within {
  box-shadow: 0 1px 0 var(--tf-accent);
}
.ledger-notes-field svg {
  width: 18px;
  height: 18px;
  flex-shrink: 0;
}
.ledger-amount-display {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
  font-variant-numeric: tabular-nums;
}
.ledger-amount-display > span {
  font-size: 11px;
  color: var(--tf-text-3);
}
.ledger-amount-display output {
  font-size: clamp(20px, 7vw, 30px);
  font-weight: 600;
  color: var(--tf-accent);
  overflow-wrap: anywhere;
}
.ledger-quick-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 0 12px;
}
.ledger-quick-actions :deep(.ledger-date-button) {
  width: 132px;
}
.ledger-quick-actions :deep(.el-input__wrapper) {
  padding-inline: 8px;
  min-height: 36px;
  cursor: pointer;
}
.ledger-quick-actions > button {
  display: flex;
  align-items: center;
  gap: 6px;
  min-height: 36px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--tf-radius-control);
  color: var(--tf-text-2);
  background: var(--tf-surface-sunken);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}
.ledger-quick-actions > button svg {
  width: 16px;
  height: 16px;
}
.mobile-field-error {
  color: var(--tf-danger);
  font-size: 12px;
  margin: 0 0 8px;
}
.refund-origin {
  color: var(--tf-text-3);
  font-size: 12px;
  line-height: 1.6;
  margin: 0 0 16px;
}
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
.amount-row.amount-row--personal {
  grid-template-columns: minmax(0, 1fr);
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

<style>
.tf-editor-shell.ledger-split-drawer {
  max-height: min(80dvh, 640px);
}
.ledger-mobile-calendar.el-popper {
  position: fixed !important;
  top: 50% !important;
  left: 50% !important;
  transform: translate(-50%, -50%) !important;
  max-width: calc(100vw - 24px);
}
.ledger-mobile-calendar .el-picker-panel {
  width: min(322px, calc(100vw - 24px));
}
.ledger-mobile-calendar .el-picker-panel__content {
  width: auto;
  margin: 12px;
}
.ledger-mobile-calendar .el-popper__arrow {
  display: none;
}
.tf-editor-shell.ledger-mobile-shell .el-drawer__footer {
  padding: 0 12px max(12px, env(safe-area-inset-bottom));
  background: var(--tf-surface-sunken);
  border-top: 1px solid var(--tf-line-soft);
}
.tf-editor-shell.ledger-mobile-shell .el-drawer__footer > .ledger-mobile-dock {
  display: block;
}
.tf-editor-shell.ledger-mobile-shell .el-drawer__body {
  padding-bottom: 12px;
}
</style>
