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
import AssetPicker from '@/desktop/components/AssetPicker.vue'
import CategoryCreateDrawer from '@/desktop/components/CategoryCreateDrawer.vue'
import ResponsiveEditorShell from '@/desktop/components/ResponsiveEditorShell.vue'
import SlidingSegmented from '@/desktop/components/SlidingSegmented.vue'
import { ApiError } from '@/shared/api/auth'
import type { CollectionBaseline } from '@/shared/api/collectionGuards'
import { DraftError } from '@/shared/travel/tripDraft'
import type { ExpenseCategory } from '@/shared/api/categories'
import {
  createLedgerEntry,
  getLedgerEntry,
  ledgerKindLabels,
  listAllLedgerEntries,
  updateLedgerEntry,
  requiresLedgerMembers,
  type LedgerPatch,
  type LedgerWriteContext,
  type LedgerEntry,
  type LedgerKind,
} from '@/shared/api/ledger'
import { listTripMembersWithBaseline, memberName, type TripMember } from '@/shared/api/members'
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
let categoryGeneration = -1
async function openCategoryCreator() {
  if (editingLocked.value || categoryLocked.value) return
  const token = generation
  categoryGeneration = token
  categoryCreatorMounted.value = true
  await nextTick()
  if (token === generation && opened.value && !editingLocked.value)
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
  if (!opened.value || editingLocked.value || categoryGeneration !== generation) return
  availableCategories.value = categories
  if (selectedId && !categoryLocked.value) draft.category_id = selectedId
  emit('update:categories', categories)
}
const context = useTripContext()
const currency = computed(() => context.trip.value?.currency_code ?? 'CNY')
const isMobile = ref(false)
const notesFocused = ref(false)
const preparingAttachments = ref(false)
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
const members = ref<TripMember[]>([])
const membersBaseline = shallowRef<CollectionBaseline | null>(null)
const loadingMembers = ref(false)
const membersError = ref<string | null>(null)
const initializing = ref(false)
const uncertainWrite = ref(false)
const needsReview = ref(false)
const reviewing = ref(false)
const reviewError = ref<string | null>(null)
type MembersSnapshot = Awaited<ReturnType<typeof listTripMembersWithBaseline>>
const candidate = shallowRef<{
  entity: LedgerEntry | null
  snapshot: MembersSnapshot
  generation: number
} | null>(null)
let generation = 0
let disposed = false
const current = (token: number) => token === generation && !disposed && opened.value
function installMembers(snapshot: MembersSnapshot) {
  members.value = snapshot.members
  membersBaseline.value = snapshot.baseline
  membersError.value = null
}
// 页面通知只标记需要核对；不能把新条件套在仍在编辑的旧草稿上。
function refreshMembers() {
  if (!opened.value || uncertain.value) return
  needsReview.value = true
  candidate.value = null
}
async function readInitialMembers(token: number): Promise<MembersSnapshot | null> {
  loadingMembers.value = true
  membersError.value = null
  try {
    const snapshot = await listTripMembersWithBaseline(context.tripId)
    if (!current(token)) return null
    installMembers(snapshot)
    return snapshot
  } catch {
    if (current(token))
      membersError.value = '无法加载完整成员资料；可继续修改已有账目的备注等非财务内容。'
    return null
  } finally {
    if (current(token)) loadingMembers.value = false
  }
}
async function retryMembers() {
  if (editingLocked.value) return
  if (membersBaseline.value || dirty.value) return readCandidate()
  const snapshot = await readInitialMembers(generation)
  if (snapshot && !isEditing.value) Object.assign(draft, defaultMemberDraft(snapshot.members))
}
onUnmounted(() => {
  disposed = true
  generation++
  candidate.value = null
})
const splitModeOptions = (Object.keys(splitModeLabels) as SplitMode[]).map((value) => ({
  value,
  label: splitModeLabels[value],
}))
function setSplitMode(value: string) {
  if (editingLocked.value) return
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
  if (editingLocked.value) return
  draft.participant_member_ids =
    draft.participant_member_ids.length === members.value.length
      ? []
      : members.value.map((m) => m.id)
}

const editor = useItemEditor<
  LedgerEntry,
  LedgerDraft,
  ReturnType<typeof validateLedgerDraft>,
  LedgerPatch,
  LedgerWriteContext
>({
  emptyDraft: () => emptyLedgerDraft(),
  draftFrom: ledgerDraftFrom,
  validate: (draft) =>
    validateLedgerDraft(draft, { currency: currency.value, minorUnits: context.minorUnits.value }),
  diff: changedLedgerFields,
  get: (id) => getLedgerEntry(context.tripId, id),
  captureIntentContext: (submission) => {
    if (needsReview.value || candidate.value || conflict.value || reviewing.value)
      throw new Error('请先核对并确认最新账目和成员资料。')
    if (submission.kind === 'update' && !requiresLedgerMembers(submission.patch))
      return { guards: [] }
    if (!membersBaseline.value) throw new Error('请先读取并核对完整成员资料。')
    const available = new Set(members.value.map((m) => m.id))
    const fields: Record<string, string> = {}
    if (!available.has(draft.payer_member_id)) fields.payer_member_id = '付款人已移除，请重新选择'
    const ids =
      draft.split_mode === 'personal' ? [draft.payer_member_id] : draft.participant_member_ids
    if (ids.some((id) => !available.has(id)))
      fields.participant_member_ids = '参与人已移除，请重新选择'
    if (Object.keys(fields).length) throw new DraftError(fields)
    return { guards: membersBaseline.value.guards.map((guard) => ({ ...guard })) }
  },
  create: (body, operationId, writeContext) =>
    trackWrite(() => createLedgerEntry(context.tripId, body, operationId, writeContext)),
  update: (id, version, patch, operationId, writeContext) =>
    trackWrite(() =>
      updateLedgerEntry(context.tripId, id, version, patch, operationId, writeContext),
    ),
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

const uncertain = computed(() => uncertainWrite.value || uncertainCreate.value)
const editingLocked = computed(
  () =>
    saving.value ||
    uncertain.value ||
    initializing.value ||
    loading.value ||
    loadingMembers.value ||
    reviewing.value,
)
async function trackWrite(write: () => Promise<WriteOutcome<LedgerEntry>>) {
  try {
    const outcome = await write()
    uncertainWrite.value = false
    return outcome
  } catch (cause) {
    uncertainWrite.value ||= !(cause instanceof ApiError) || (cause.problem?.status ?? 500) >= 500
    if (
      !uncertainWrite.value &&
      cause instanceof ApiError &&
      (cause.problem?.status === 412 || cause.problem?.status === 428)
    )
      needsReview.value = true
    throw cause
  }
}
function setParticipants(value: (string | number)[]) {
  if (value.every((id): id is string => typeof id === 'string'))
    setField('participant_member_ids', value)
}
function setField<K extends keyof LedgerDraft>(field: K, value: LedgerDraft[K]) {
  if (!editingLocked.value && !(field === 'category_id' && categoryLocked.value))
    draft[field] = value
}

watch(
  opened,
  (value) => {
    notesFocused.value = false
    if (!value) {
      generation++
      splitSettingsOpened.value = false
      candidate.value = null
      initializing.value = loadingMembers.value = reviewing.value = false
    }
    emit('update:opened', value)
  },
  { flush: 'sync' },
)

const categoryName = (id: string) =>
  availableCategories.value.find((c) => c.id === id)?.name ?? '（已删除分类）'

const categoryLocked = computed(() => draft.kind === 'refund' && !!draft.refunded_entry_id)
const submitDisabled = computed(
  () =>
    saving.value ||
    preparingAttachments.value ||
    (!uncertain.value &&
      (editingLocked.value ||
        needsReview.value ||
        !!candidate.value ||
        conflict.value ||
        (isEditing.value && (!baseline.value || !dirty.value)))),
)
const submitLabel = computed(() =>
  uncertain.value ? '重试确认' : isEditing.value ? '保存修改' : '保存',
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
  const comparison = candidate.value?.entity ?? latest.value
  if (!baseline.value || !comparison) return []
  const mine = draft
  const original = ledgerDraftFrom(baseline.value)
  const theirs = ledgerDraftFrom(comparison)
  const display = (field: keyof LedgerDraft, value: string) => {
    if (field === 'kind') return ledgerKindLabels[value as LedgerKind] ?? value
    if (field === 'category_id') return value ? categoryName(value) : '（空）'
    if (field === 'refunded_entry_id') return value ? '已关联原支出' : '未关联'
    if (field === 'attachment_asset_ids')
      return value ? `${value.split(',').length} 张票据` : '无票据'
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
    .filter((field) => JSON.stringify(mine[field]) !== JSON.stringify(original[field]))
    .map((field) => ({
      field,
      label: ledgerFieldLabels[field] ?? field,
      mine: display(field, String(mine[field])),
      theirs: display(field, String(theirs[field])),
    }))
})

function setText(field: 'occurred_on', value: unknown) {
  setField(field, typeof value === 'string' ? value : '')
}

async function beforeClose(done?: () => void) {
  await requestClose(done)
}

async function requestClose(done?: () => void) {
  if (saving.value || uncertain.value) return false
  const token = generation
  if (dirty.value) {
    try {
      await ElMessageBox.confirm('尚有未保存的输入，关闭后将放弃这些输入。', '关闭记账', {
        confirmButtonText: '放弃输入并关闭',
        cancelButtonText: '继续编辑',
        type: 'warning',
      })
    } catch {
      return false
    }
  }
  if (token !== generation || saving.value || uncertain.value) return false
  generation++
  editor.close()
  initializing.value = loadingMembers.value = reviewing.value = false
  candidate.value = null
  done?.()
  return true
}

async function readCandidate() {
  if (editingLocked.value || preparingAttachments.value || loadingLatest.value) return
  const token = generation
  reviewing.value = true
  candidate.value = null
  reviewError.value = null
  try {
    const [entity, snapshot] = await Promise.all([
      baseline.value ? getLedgerEntry(context.tripId, baseline.value.id) : Promise.resolve(null),
      listTripMembersWithBaseline(context.tripId),
    ])
    if (current(token) && !uncertain.value)
      candidate.value = { entity, snapshot, generation: token }
  } catch {
    if (current(token)) reviewError.value = '无法读取最新账目和完整成员资料，原输入已保留。'
  } finally {
    if (current(token)) reviewing.value = false
  }
}

function adoptCandidate(keepChanges: boolean) {
  const next = candidate.value
  if (!next || !current(next.generation) || editingLocked.value || loadingLatest.value) return
  const changes: Partial<LedgerDraft> = {}
  if (keepChanges && baseline.value) {
    const original = ledgerDraftFrom(baseline.value)
    for (const key of Object.keys(original) as (keyof LedgerDraft)[]) {
      if (JSON.stringify(draft[key]) !== JSON.stringify(original[key]))
        Object.assign(changes, { [key]: Array.isArray(draft[key]) ? [...draft[key]] : draft[key] })
    }
  }
  if (next.entity) {
    editor.latest.value = next.entity
    editor.adoptLatest()
    if (keepChanges) Object.assign(draft, changes)
  }
  // 创建没有远端实体，两种确认都保留整份草稿，不能自动补删成员或重算退款。
  installMembers(next.snapshot)
  needsReview.value = false
  candidate.value = null
  reviewError.value = null
  error.value = null
}

async function save() {
  if (saving.value || preparingAttachments.value) return
  if (!uncertain.value) {
    if (editingLocked.value || needsReview.value || candidate.value || conflict.value) return
    if (isMobile.value && draft.amount.endsWith('.')) draft.amount = draft.amount.slice(0, -1)
  }
  const outcome = await editor.save()
  if (outcome) emit('saved', outcome)
  else if (
    errors.value.participant_member_ids ||
    errors.value.payer_member_id ||
    errors.value.split_mode
  )
    splitSettingsOpened.value = true
}

async function beginOpen(): Promise<number | null> {
  if (disposed || saving.value || uncertain.value) return null
  if (opened.value && !(await requestClose())) return null
  const token = ++generation
  initializing.value = true
  members.value = []
  membersBaseline.value = null
  membersError.value = null
  needsReview.value = false
  candidate.value = null
  reviewError.value = null
  refundOrigin.value = null
  splitSettingsOpened.value = false
  return token
}

async function open(
  entry?: LedgerEntry,
  kind?: LedgerKind,
  presets: Partial<Pick<LedgerDraft, 'occurred_on' | 'notes'>> = {},
) {
  if (!entry && kind === 'refund') return
  const token = await beginOpen()
  if (token === null || token !== generation || disposed) return
  const opening = editor.open(
    entry,
    entry ? {} : { occurred_on: context.today.value, kind: kind ?? 'expense', ...presets },
  )
  const snapshot = await readInitialMembers(token)
  await opening
  if (!current(token)) return
  if (snapshot && !entry) Object.assign(draft, defaultMemberDraft(snapshot.members))
  initializing.value = false
}

async function openRefund(origin: LedgerEntry) {
  if (origin.kind !== 'expense') return
  const token = await beginOpen()
  if (token === null || token !== generation || disposed) return
  try {
    const [entity, refunds] = await Promise.all([
      getLedgerEntry(context.tripId, origin.id),
      listAllLedgerEntries(context.tripId, { kind: 'refund', refunded_entry_id: origin.id }),
    ])
    if (token !== generation || disposed) return
    const remaining = sumMoney([entity.amount, ...refunds.map((r) => `-${r.amount}`)])
    if (!/[1-9]/.test(remaining) || remaining.startsWith('-')) {
      ElMessage.info('这笔账单已全额退款')
      return
    }
    refundOrigin.value = entity
    await editor.open(undefined, {
      kind: 'refund',
      amount: remaining,
      category_id: entity.category_id,
      occurred_on: context.today.value,
      refunded_entry_id: entity.id,
      payer_member_id: entity.payer_member_id,
      split_mode: entity.split_mode,
      participant_member_ids: entity.splits.map((s) => s.member_id),
    })
    if (!current(token)) return
    await readInitialMembers(token)
  } catch {
    if (token === generation && !disposed) ElMessage.error('无法加载原账单及退款记录，请重试')
  } finally {
    if (token === generation && !disposed) initializing.value = false
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
    :close-on-press-escape="!saving && !uncertain"
    :before-close="beforeClose"
  >
    <ElSkeleton v-if="loading" :rows="6" animated />
    <template v-else>
      <ElAlert
        v-if="error"
        :title="error"
        :type="uncertain ? 'warning' : 'error'"
        :closable="false"
        show-icon
        class="editor-alert"
      />
      <ElButton v-if="isEditing && !baseline" @click="editor.load">重新加载账目</ElButton>
      <ElForm v-else label-position="top" :disabled="editingLocked" @submit.prevent="save()">
        <div v-if="isMobile" class="ledger-categories" role="group" aria-label="账单分类">
          <button
            v-for="category in categoryOptions"
            :key="category.id"
            type="button"
            :aria-pressed="draft.category_id === category.id"
            :disabled="editingLocked || category.disabled || categoryLocked"
            @click="setField('category_id', category.id)"
          >
            <span class="ledger-category-icon"
              ><component :is="categoryIconComponent(category.icon)" aria-hidden="true"
            /></span>
            <span>{{ category.name }}</span>
          </button>
          <button
            type="button"
            :disabled="editingLocked || categoryLocked"
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
              :model-value="draft.amount"
              @update:model-value="setField('amount', $event)"
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
              :model-value="draft.payer_member_id"
              @update:model-value="setField('payer_member_id', $event)"
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
          <ElButton
            size="small"
            :loading="loadingMembers"
            :disabled="editingLocked"
            @click="retryMembers"
            >重试</ElButton
          >
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
              :disabled="editingLocked"
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
              :model-value="draft.payer_member_id"
              @update:model-value="setField('payer_member_id', $event)"
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
                  :disabled="editingLocked || !members.length"
                  @click="toggleAllParticipants"
                  >{{
                    draft.participant_member_ids.length === members.length ? '清空' : '全选'
                  }}</ElButton
                >
              </span>
            </template>
            <ElCheckboxGroup
              :model-value="draft.participant_member_ids"
              @update:model-value="setParticipants"
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
            :model-value="draft.category_id"
            @update:model-value="setField('category_id', $event)"
            filterable
            :disabled="editingLocked || categoryLocked"
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
            :model-value="draft.notes"
            @update:model-value="setField('notes', $event)"
            type="textarea"
            :rows="2"
            maxlength="4000"
            show-word-limit
            placeholder="可选"
          />
        </ElFormItem>
        <ElFormItem label="票据" :error="errors.attachment_asset_ids">
          <AssetPicker
            :model-value="draft.attachment_asset_ids"
            @update:model-value="setField('attachment_asset_ids', $event)"
            :trip-id="context.tripId"
            :max="10"
            :disabled="editingLocked"
            @busy="preparingAttachments = $event"
          />
        </ElFormItem>
        <button type="submit" class="visually-hidden" tabindex="-1" aria-hidden="true">保存</button>
      </ElForm>
      <section
        v-if="conflict || needsReview || candidate || reviewError"
        class="conflict-panel"
        aria-live="polite"
      >
        <h3>核对账目和成员资料</h3>
        <p>你的输入已保留。请读取并核对最新账目与成员；确认采用后，再保存你的修改。</p>
        <ElSkeleton v-if="loadingLatest" :rows="2" animated />
        <ElAlert v-if="latestError" :title="latestError" type="error" :closable="false" />
        <table v-if="candidate?.entity || latest" class="conflict-table">
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
        <ElAlert v-if="reviewError" :title="reviewError" type="error" :closable="false" />
        <div v-if="candidate" aria-label="成员核对">
          <p>
            原成员：{{ members.map((m) => `${m.name} ${m.share_percent}%`).join('、') || '无' }}
          </p>
          <p>
            最新成员（按顺序）：{{
              candidate.snapshot.members.map((m) => `${m.name} ${m.share_percent}%`).join('、') ||
              '无'
            }}
          </p>
          <p>付款人与参与人不会自动替换，请检查已移除的成员。</p>
        </div>
        <div class="conflict-actions tf-actions">
          <ElButton
            :disabled="editingLocked || loadingLatest"
            :loading="reviewing"
            @click="readCandidate"
            >读取最新资料</ElButton
          >
          <ElButton v-if="candidate" :disabled="editingLocked" @click="adoptCandidate(true)"
            >保留我的改动并采用已核对基线</ElButton
          >
          <ElButton
            v-if="candidate && isEditing"
            :disabled="editingLocked"
            @click="adoptCandidate(false)"
            >放弃输入采用最新</ElButton
          >
          <ElButton v-if="candidate" :disabled="editingLocked" @click="candidate = null"
            >取消核对</ElButton
          >
        </div>
      </section>
    </template>
    <template #footer>
      <p v-if="uncertain" role="status">保存结果尚未确认，请原样重试，确认后再编辑或关闭。</p>
      <ElButton
        v-if="isMobile && uncertain"
        type="primary"
        :disabled="submitDisabled"
        :loading="saving"
        @click="save()"
        >重试确认</ElButton
      >
      <div v-if="isMobile" class="ledger-mobile-dock">
        <div class="ledger-amount-line">
          <label class="ledger-notes-field">
            <NotebookPen aria-hidden="true" />
            <input
              :value="draft.notes"
              @input="setField('notes', ($event.target as HTMLInputElement).value)"
              aria-label="备注"
              placeholder="添加备注"
              maxlength="4000"
              enterkeyhint="done"
              :disabled="editingLocked"
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
            :disabled="editingLocked"
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
            :disabled="editingLocked"
            :aria-expanded="splitSettingsOpened"
            aria-label="选择分摊模式"
            @click="!editingLocked && (splitSettingsOpened = true)"
          >
            <Users aria-hidden="true" />{{ splitModeLabels[draft.split_mode]
            }}<ChevronDown aria-hidden="true" />
          </button>
        </div>
        <p v-if="errors.occurred_on" class="mobile-field-error">{{ errors.occurred_on }}</p>
        <LedgerAmountKeypad
          v-if="!notesFocused"
          :model-value="draft.amount"
          @update:model-value="setField('amount', $event)"
          :minor-units="context.minorUnits.value"
          :disabled="editingLocked"
          :saving="saving"
          :submit-disabled="submitDisabled"
          :submit-label="submitLabel"
          @submit="save()"
        />
      </div>
      <template v-else>
        <ElButton type="primary" :loading="saving" :disabled="submitDisabled" @click="save()">{{
          submitLabel
        }}</ElButton>
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
