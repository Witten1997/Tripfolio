<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCard,
  ElDatePicker,
  ElEmpty,
  ElInput,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSkeleton,
  ElTag,
} from 'element-plus'
import { computed, onMounted, reactive, ref, shallowRef, watch } from 'vue'

import CategorySharePie from '@/desktop/components/CategorySharePie.vue'
import DailyNetBar from '@/desktop/components/DailyNetBar.vue'
import ActionIcon from '@/desktop/components/ActionIcon.vue'
import IconAction from '@/desktop/components/IconAction.vue'
import LedgerEntryDialog from '@/desktop/components/LedgerEntryDialog.vue'
import SettlementCard from '@/desktop/components/SettlementCard.vue'
import SlidingSegmented from '@/desktop/components/SlidingSegmented.vue'
import { ApiError } from '@/shared/api/auth'
import { listCategories, type ExpenseCategory } from '@/shared/api/categories'
import { listTripMembers, memberName, type TripMember } from '@/shared/api/members'
import {
  deleteLedgerEntry,
  ledgerKindLabels,
  listAllLedgerEntries,
  listLedgerEntries,
  type LedgerEntry,
  type LedgerKind,
  type LedgerQuery,
} from '@/shared/api/ledger'
import { getTripStatistics, type TripStatistics } from '@/shared/api/statistics'
import { updateTrip } from '@/shared/api/trips'
import {
  actionError,
  createWriteIntent,
  writeWarnings,
  type WriteOutcome,
} from '@/shared/api/writes'
import { canonicalizeAmount } from '@/shared/money'
import { splitModeLabels, type SplitMode } from '@/shared/travel/ledgerDraft'
import {
  categoryAmountRows,
  dailyBars,
  formatMoney,
  formatShare,
  pieSlices,
  sumMoney,
  toNumber,
} from '@/shared/travel/statisticsView'
import { useTripContext } from '@/shared/travel/tripContext'
import { useCursorPage } from '@/shared/travel/useCursorPage'

const context = useTripContext()
const currency = computed(() => context.trip.value?.currency_code ?? 'CNY')

const filters = reactive<{
  dateFrom: string
  dateTo: string
  categoryId: string
  splitMode: '' | SplitMode
  kind: '' | LedgerKind
}>({ dateFrom: '', dateTo: '', categoryId: '', splitMode: '', kind: '' })
const filtersOpened = ref(false)
const kindOptions: Array<{ value: '' | LedgerKind; label: string }> = [
  { value: '', label: '全部' },
  { value: 'expense', label: '未退款' },
  { value: 'refund', label: '有退款' },
]
function setKind(value: string) {
  if (value === '' || value === 'expense' || value === 'refund') filters.kind = value
}
const hasAdvancedFilter = computed(
  () => !!filters.dateFrom || !!filters.dateTo || !!filters.categoryId || !!filters.splitMode,
)
const hasFilter = computed(() => hasAdvancedFilter.value || !!filters.kind)

const categories = shallowRef<ExpenseCategory[]>([])
const members = shallowRef<TripMember[]>([])
const settlementKey = ref(0)
const refunds = shallowRef<LedgerEntry[]>([])
const refundsLoading = ref(false)
const refundsError = ref<string | null>(null)
const expandedRefunds = reactive(new Set<string>())
let refundsGeneration = 0
async function loadRefunds() {
  const request = ++refundsGeneration
  refundsLoading.value = true
  refundsError.value = null
  try {
    const result = await listAllLedgerEntries(context.tripId, { kind: 'refund' })
    if (request === refundsGeneration) refunds.value = result
  } catch (cause) {
    if (request === refundsGeneration)
      refundsError.value = actionError(cause, '无法加载退款记录，请重试')
  } finally {
    if (request === refundsGeneration) refundsLoading.value = false
  }
}
const refundsByEntry = computed(() => {
  const result = new Map<string, LedgerEntry[]>()
  for (const refund of refunds.value) {
    if (!refund.refunded_entry_id) continue
    const group = result.get(refund.refunded_entry_id) ?? []
    group.push(refund)
    result.set(refund.refunded_entry_id, group)
  }
  return result
})
function entryRefunds(entry: LedgerEntry) {
  return refundsByEntry.value.get(entry.id) ?? []
}
function refundedAmount(entry: LedgerEntry) {
  return sumMoney(entryRefunds(entry).map((r) => r.amount))
}
function fullyRefunded(entry: LedgerEntry) {
  const remaining = sumMoney([entry.amount, `-${refundedAmount(entry)}`])
  return remaining.startsWith('-') || !/[1-9]/.test(remaining)
}
function toggleRefunds(id: string) {
  if (expandedRefunds.has(id)) expandedRefunds.delete(id)
  else expandedRefunds.add(id)
}
async function loadMembers() {
  try {
    members.value = await listTripMembers(context.tripId)
  } catch {
    /* 付款人名称退化为「已删除成员」，不阻塞账单 */
  }
}
function payerLabel(entry: LedgerEntry) {
  const name = memberName(members.value, entry.payer_member_id)
  return entry.kind === 'refund' ? `${name}收款` : `${name}付款`
}
const categoryName = (id: string) => categories.value.find((c) => c.id === id)?.name ?? '已删除分类'

const dialog = ref<InstanceType<typeof LedgerEntryDialog>>()
const notice = ref<string[]>([])
const noticeType = ref<'success' | 'warning'>('success')
const actionFailure = ref<string | null>(null)
const busy = ref<string | null>(null)
const intents = new Map<string, ReturnType<typeof createWriteIntent>>()

/** 日期、分类与分摊模式筛选同时作用于统计与明细；类型只筛明细。 */
const scopeQuery = computed(() => ({
  date_from: filters.dateFrom || undefined,
  date_to: filters.dateTo || undefined,
  category_id: filters.categoryId || undefined,
  split_mode: filters.splitMode || undefined,
}))

const page = useCursorPage<LedgerEntry, LedgerQuery>(
  () => ({
    ...scopeQuery.value,
    kind: 'expense',
    has_refunds: filters.kind ? filters.kind === 'refund' : undefined,
    limit: 50,
  }),
  (query) => listLedgerEntries(context.tripId, query),
)

const statistics = shallowRef<TripStatistics | null>(null)
const statsLoading = ref(false)
const statsError = ref<string | null>(null)
let statsGeneration = 0

async function reloadStatistics() {
  const request = ++statsGeneration
  statsLoading.value = true
  statsError.value = null
  try {
    // daily_limit 取契约上限，尽量一次画完整趟
    const loaded = await getTripStatistics(context.tripId, {
      ...scopeQuery.value,
      daily_limit: 100,
    })
    if (request === statsGeneration) statistics.value = loaded
  } catch (cause) {
    if (request === statsGeneration) {
      statsError.value = actionError(cause, '无法加载统计，请检查网络后重试')
    }
  } finally {
    if (request === statsGeneration) statsLoading.value = false
  }
}

async function reloadAll() {
  settlementKey.value++
  await Promise.all([reloadStatistics(), page.reload(), loadRefunds()])
}

const totals = computed(() => statistics.value?.filtered_totals ?? null)
const budget = computed(() => statistics.value?.trip_budget ?? null)
const slices = computed(() => pieSlices(statistics.value?.by_category ?? []))
const bars = computed(() => dailyBars(statistics.value?.daily.items ?? []))
const dailyTruncated = computed(() => !!statistics.value?.daily.next_cursor)
/** 只显示当前范围内有账目的分类，保留全额退款与被引用的已删除分类。 */
const categoryRows = computed(() => categoryAmountRows(statistics.value?.by_category ?? []))
const overspent = computed(() => toNumber(budget.value?.overspent_amount ?? '0') > 0)

/* ---- 总预算在统计框中直接编辑（TR-04） ---- */
const budgetEditing = ref(false)
const budgetInput = ref('')
const budgetSaving = ref(false)
const budgetError = ref<string | null>(null)
const budgetIntent = createWriteIntent()

function startBudgetEdit() {
  budgetInput.value = context.trip.value?.budget_amount ?? ''
  budgetError.value = null
  budgetEditing.value = true
}

async function saveBudget() {
  const trip = context.trip.value
  if (!trip || budgetSaving.value) return
  const raw = budgetInput.value.trim()
  let amount: string | null = null
  if (raw !== '') {
    try {
      amount = canonicalizeAmount(raw, context.minorUnits.value)
    } catch (cause) {
      budgetError.value = cause instanceof Error ? cause.message : '预算金额格式不正确'
      return
    }
  }
  if (amount === (trip.budget_amount ?? null)) {
    budgetEditing.value = false
    return
  }
  budgetSaving.value = true
  budgetError.value = null
  try {
    const outcome = await updateTrip(
      trip.id,
      trip.version,
      { budget_amount: amount },
      budgetIntent.key({ id: trip.id, version: trip.version, budget_amount: amount }),
    )
    budgetIntent.reset()
    budgetEditing.value = false
    if (outcome.resource) context.replace(outcome.resource)
    else await context.reload()
    noticeType.value = 'success'
    notice.value = ['总预算已更新。']
    await reloadStatistics()
  } catch (cause) {
    budgetError.value = actionError(cause, '网络连接中断，结果尚未确认。可重试或刷新后确认。')
    if (cause instanceof ApiError && cause.code === 'VERSION_CONFLICT') await context.reload()
  } finally {
    budgetSaving.value = false
  }
}

/* ---- 明细操作 ---- */
function intentFor(slot: string) {
  const existing = intents.get(slot) ?? createWriteIntent()
  intents.set(slot, existing)
  return existing
}

async function remove(entry: LedgerEntry) {
  if (entry.kind === 'expense') {
    try {
      const linked = await listAllLedgerEntries(context.tripId, {
        kind: 'refund',
        refunded_entry_id: entry.id,
      })
      if (linked.length) {
        actionFailure.value = '这笔账单有退款记录，请先展开退款并删除退款记录，再删除账单。'
        expandedRefunds.add(entry.id)
        return
      }
    } catch (cause) {
      actionFailure.value = actionError(cause, '无法核对退款记录，请重试')
      return
    }
  }
  if (busy.value) return
  const isExpense = entry.kind === 'expense'
  try {
    await ElMessageBox.confirm(
      isExpense ? '删除后这笔账单不再计入支出统计。' : '删除后这笔退款不再冲减分类净支出。',
      `删除这笔${ledgerKindLabels[entry.kind]}？`,
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '保留' },
    )
  } catch {
    return
  }
  busy.value = entry.id
  actionFailure.value = null
  const intent = intentFor(`delete:${entry.id}`)
  try {
    const outcome = await deleteLedgerEntry(
      context.tripId,
      entry.id,
      entry.version,
      intent.key({ delete: entry.id, version: entry.version }),
    )
    intent.reset()
    noticeType.value = 'success'
    notice.value = ['账目已删除。', ...writeWarnings(outcome.result)]
    // 删除最后一条账目会解锁旅行币种，旅行版本随之变化
    await Promise.all([reloadAll(), context.reload()])
  } catch (cause) {
    actionFailure.value = actionError(cause, '网络连接中断，结果尚未确认。可重试或刷新后确认。')
    if (
      cause instanceof ApiError &&
      ['VERSION_CONFLICT', 'RESOURCE_GONE'].includes(cause.code ?? '')
    )
      await reloadAll()
  } finally {
    busy.value = null
  }
}

async function saved(outcome: WriteOutcome<LedgerEntry>) {
  const warnings = writeWarnings(outcome.result)
  noticeType.value = warnings.length ? 'warning' : 'success'
  notice.value = ['账目已保存。', ...warnings]
  actionFailure.value = null
  // 第一条账目会锁定旅行币种，旅行资源进入 affected
  await Promise.all([reloadAll(), context.reload()])
}

function clearFilters() {
  filters.dateFrom = ''
  filters.dateTo = ''
  filters.categoryId = ''
  filters.splitMode = ''
  filters.kind = ''
}

function toggleCategory(id: string) {
  filters.categoryId = filters.categoryId === id ? '' : id
}

function focusDay(date: string) {
  filters.dateFrom = date
  filters.dateTo = date
  filtersOpened.value = true
}

async function loadCategories() {
  try {
    categories.value = await listCategories()
  } catch {
    /* 分类加载失败时下拉为空，记账对话框会提示 */
  }
}

// 日期与分类变化要同时刷新统计；明细由 useCursorPage 自己 watch
watch(scopeQuery, reloadStatistics, { deep: true })
// 成员管理保存后刷新名称与记账表单的成员选项
watch(
  () => context.membersVersion.value,
  async () => {
    await Promise.all([loadMembers(), dialog.value?.refreshMembers()])
  },
)
onMounted(async () => {
  await Promise.all([loadCategories(), loadMembers(), reloadStatistics(), loadRefunds()])
})
</script>

<template>
  <div class="ledger-tab">
    <!-- 统计框：净支出、预算对比，总预算可直接编辑 -->
    <ElCard shadow="never" class="stats-card">
      <template #header>
        <div class="chart-header">
          <h2>支出统计</h2>
        </div>
      </template>
      <ElSkeleton v-if="statsLoading && !statistics" :rows="3" animated />
      <ElAlert v-else-if="statsError" :title="statsError" type="error" :closable="false" show-icon>
        <ElButton size="small" class="retry-button" @click="reloadStatistics">重新加载</ElButton>
      </ElAlert>
      <div v-else-if="totals && budget" class="stats">
        <div class="stat stat--hero">
          <span class="stat-label">{{ hasFilter ? '筛选范围净支出' : '总花费（净支出）' }}</span>
          <strong class="stat-value">{{ formatMoney(totals.net_amount) }}</strong>
          <span class="stat-unit">{{ statistics?.currency_code }}</span>
          <span class="stat-hint">{{ totals.entry_count }} 笔账目</span>
        </div>
        <div class="stat">
          <span class="stat-label">支出</span>
          <strong class="stat-value stat-value--small">{{
            formatMoney(totals.expense_amount)
          }}</strong>
        </div>
        <div class="stat">
          <span class="stat-label">退款</span>
          <strong class="stat-value stat-value--small">{{
            formatMoney(totals.refund_amount)
          }}</strong>
        </div>
        <div class="stat stat--budget">
          <span class="stat-label">总预算</span>
          <div v-if="budgetEditing" class="budget-edit">
            <ElInput
              v-model="budgetInput"
              size="small"
              inputmode="decimal"
              placeholder="留空表示不设预算"
              aria-label="总预算"
              @keyup.enter="saveBudget"
            />
            <ElButton size="small" type="primary" :loading="budgetSaving" @click="saveBudget"
              >保存</ElButton
            >
            <ElButton size="small" :disabled="budgetSaving" @click="budgetEditing = false"
              >取消</ElButton
            >
          </div>
          <button
            v-else
            type="button"
            class="budget-value"
            :aria-label="`编辑总预算：${budget.budget_amount ? formatMoney(budget.budget_amount) : '未设置'}`"
            @click="startBudgetEdit"
          >
            <strong class="stat-value stat-value--small">{{
              budget.budget_amount ? formatMoney(budget.budget_amount) : '未设置'
            }}</strong>
            <ActionIcon name="edit" class="budget-edit-icon" />
          </button>
          <span v-if="budgetError" class="budget-error">{{ budgetError }}</span>
        </div>
        <div class="stat">
          <span class="stat-label">{{ overspent ? '已超支' : '剩余预算' }}</span>
          <strong class="stat-value stat-value--small" :class="{ 'stat-value--over': overspent }">{{
            budget.budget_amount
              ? formatMoney(overspent ? budget.overspent_amount! : budget.remaining_amount!)
              : '—'
          }}</strong>
          <span class="stat-hint">整趟 {{ formatMoney(budget.trip_net_amount) }}</span>
        </div>
      </div>
    </ElCard>

    <section class="ledger-controls" aria-label="账单筛选与操作">
      <div class="tab-toolbar">
        <SlidingSegmented
          :model-value="filters.kind"
          :options="kindOptions"
          label="按类型筛选明细"
          @update:model-value="setKind"
        />
        <div class="tab-actions tf-actions">
          <IconAction
            icon="filter"
            label="筛选"
            :type="filtersOpened || hasAdvancedFilter ? 'primary' : undefined"
            :aria-expanded="filtersOpened"
            aria-controls="ledger-filter-panel"
            @click="filtersOpened = !filtersOpened"
          />
          <IconAction
            icon="receipt"
            label="记一笔"
            type="primary"
            @click="dialog?.open(undefined, 'expense')"
          />
        </div>
      </div>
      <Transition name="filter-panel">
        <div v-if="filtersOpened" id="ledger-filter-panel" class="filters">
          <ElDatePicker
            v-model="filters.dateFrom"
            type="date"
            :editable="false"
            value-format="YYYY-MM-DD"
            format="YYYY-MM-DD"
            placeholder="起始日期"
            class="filter-date"
            aria-label="筛选起始日期"
          />
          <ElDatePicker
            v-model="filters.dateTo"
            type="date"
            :editable="false"
            value-format="YYYY-MM-DD"
            format="YYYY-MM-DD"
            placeholder="结束日期"
            class="filter-date"
            aria-label="筛选结束日期"
          />
          <div class="filter-selects">
            <ElSelect
              v-model="filters.categoryId"
              clearable
              filterable
              placeholder="全部分类"
              class="filter-category"
              aria-label="按分类筛选"
            >
              <ElOption v-for="c in categories" :key="c.id" :value="c.id" :label="c.name" />
            </ElSelect>
            <ElSelect
              v-model="filters.splitMode"
              clearable
              placeholder="全部分摊模式"
              class="filter-split-mode"
              aria-label="按分摊模式筛选"
            >
              <ElOption
                v-for="(label, mode) in splitModeLabels"
                :key="mode"
                :value="mode"
                :label="label"
              />
            </ElSelect>
          </div>
          <ElButton v-if="hasFilter" text @click="clearFilters">清除筛选</ElButton>
        </div>
      </Transition>
    </section>

    <ElAlert
      v-if="notice.length"
      :type="noticeType"
      :title="notice.join(' ')"
      show-icon
      @close="notice = []"
    />
    <ElAlert v-if="actionFailure" type="error" :title="actionFailure" :closable="false" show-icon />
    <ElAlert v-if="refundsError" type="error" :title="refundsError" :closable="false" show-icon
      ><ElButton @click="loadRefunds">重新加载退款</ElButton></ElAlert
    >

    <!-- 图表：分类占比与每日净支出 -->
    <div class="charts">
      <ElCard shadow="never" class="chart-card">
        <template #header>
          <div class="chart-header">
            <h2>分类占比</h2>
            <span class="chart-sub">按分类净支出计算</span>
          </div>
        </template>
        <ElSkeleton v-if="statsLoading && !statistics" :rows="5" animated />
        <CategorySharePie
          v-else
          :slices="slices"
          :currency="statistics?.currency_code ?? currency"
          :ratio-available="statistics?.ratio_available ?? false"
          @select="toggleCategory"
        />
      </ElCard>
      <ElCard shadow="never" class="chart-card">
        <template #header>
          <div class="chart-header">
            <h2>每日净支出</h2>
            <span v-if="dailyTruncated" class="chart-sub">仅显示最近 100 天</span>
            <span v-else class="chart-sub">点击某天可筛选该天明细</span>
          </div>
        </template>
        <ElSkeleton v-if="statsLoading && !statistics" :rows="5" animated />
        <DailyNetBar
          v-else
          :bars="bars"
          :currency="statistics?.currency_code ?? currency"
          @select="focusDay"
        />
      </ElCard>
    </div>

    <!-- 成员结算：不受筛选影响 -->
    <SettlementCard :refresh-key="settlementKey" />

    <!-- 分类金额明细：图表的表格视图，也是进入分类明细的入口 -->
    <ElCard v-if="categoryRows.length" shadow="never" class="category-card">
      <template #header>
        <div class="chart-header">
          <h2>分类金额</h2>
          <span class="chart-sub">点击分类查看其明细</span>
        </div>
      </template>
      <table class="category-table">
        <thead>
          <tr>
            <th scope="col">分类</th>
            <th scope="col" class="num">支出</th>
            <th scope="col" class="num">退款</th>
            <th scope="col" class="num">净支出</th>
            <th scope="col" class="num">占比</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in categoryRows"
            :key="row.category_id"
            :class="{ 'category-row--active': filters.categoryId === row.category_id }"
          >
            <th scope="row">
              <button
                type="button"
                class="category-link"
                :aria-pressed="filters.categoryId === row.category_id"
                @click="toggleCategory(row.category_id)"
              >
                {{ row.name }}
              </button>
            </th>
            <td class="num">{{ formatMoney(row.expense_amount) }}</td>
            <td class="num">{{ formatMoney(row.refund_amount) }}</td>
            <td class="num">{{ formatMoney(row.net_amount) }}</td>
            <td class="num">{{ formatShare(row.share) ?? '—' }}</td>
          </tr>
        </tbody>
      </table>
    </ElCard>

    <!-- 账目明细 -->
    <ElSkeleton
      v-if="page.loading.value && !page.items.value.length"
      :rows="6"
      animated
      class="tab-skeleton"
    />
    <ElCard v-else-if="page.error.value" shadow="never">
      <ElAlert :title="page.error.value" type="error" :closable="false" show-icon />
      <ElButton class="retry-button" @click="page.reload">重新加载</ElButton>
    </ElCard>
    <ElCard v-else-if="!page.items.value.length" shadow="never">
      <ElEmpty :description="hasFilter ? '筛选范围内没有账目' : '还没有账目，记下第一笔花费'">
        <ElButton v-if="hasFilter" @click="clearFilters">清除筛选</ElButton>
        <IconAction
          v-else
          icon="receipt"
          label="记一笔"
          type="primary"
          @click="dialog?.open(undefined, 'expense')"
        />
      </ElEmpty>
    </ElCard>
    <template v-else>
      <ul class="entry-list tf-surface">
        <li
          v-for="entry in page.items.value"
          :key="entry.id"
          class="entry"
          :class="{ 'entry--refund': entry.kind === 'refund' }"
        >
          <div class="entry-main">
            <div class="entry-title">
              <ElTag
                :type="entry.kind === 'refund' ? 'success' : 'info'"
                size="small"
                effect="plain"
                >{{ ledgerKindLabels[entry.kind] }}</ElTag
              >
              <strong>{{ categoryName(entry.category_id) }}</strong>
              <span v-if="entry.refunded_entry_id" class="entry-linked">已关联原支出</span>
            </div>
            <p class="entry-meta">
              <span>{{ entry.occurred_on }}</span>
              <span>{{ payerLabel(entry) }}</span>
              <span v-if="entry.notes" class="entry-notes">{{ entry.notes }}</span>
            </p>
            <button
              v-if="entryRefunds(entry).length"
              type="button"
              class="entry-refund-summary"
              :aria-expanded="expandedRefunds.has(entry.id)"
              @click="toggleRefunds(entry.id)"
            >
              已退款 {{ formatMoney(refundedAmount(entry)) }} {{ entry.currency_code }} ·
              {{ expandedRefunds.has(entry.id) ? '收起' : '查看 / 编辑' }}
            </button>
          </div>
          <div class="entry-amount" :class="{ 'entry-amount--refund': entry.kind === 'refund' }">
            {{ entry.kind === 'refund' ? '−' : '' }}{{ formatMoney(entry.personal_amount) }}
            <span class="entry-currency">{{ entry.currency_code }}</span>
            <span
              v-if="entry.split_count > 1 || entry.personal_amount !== entry.amount"
              class="entry-share"
            >
              总额 {{ formatMoney(entry.amount) }} · {{ entry.split_count }} 人{{
                entry.split_mode === 'ratio' ? '按比例' : '均摊'
              }}
            </span>
          </div>
          <div class="entry-actions tf-actions">
            <ElButton
              text
              :disabled="!!busy || refundsLoading || !!refundsError || fullyRefunded(entry)"
              @click="dialog?.openRefund(entry)"
              >{{ fullyRefunded(entry) ? '已全退' : '退款' }}</ElButton
            >
            <IconAction
              icon="edit"
              :label="`编辑${categoryName(entry.category_id)}账目（${formatMoney(entry.personal_amount)} ${entry.currency_code}）`"
              text
              :disabled="!!busy"
              @click="dialog?.open(entry)"
            />
            <IconAction
              icon="trash"
              :label="`删除${categoryName(entry.category_id)}账目（${formatMoney(entry.personal_amount)} ${entry.currency_code}）`"
              text
              type="danger"
              :disabled="!!busy"
              :loading="busy === entry.id"
              @click="remove(entry)"
            />
          </div>
          <ul
            v-if="expandedRefunds.has(entry.id) && entryRefunds(entry).length"
            class="entry-refunds"
            aria-label="退款记录"
          >
            <li v-for="refund in entryRefunds(entry)" :key="refund.id">
              <div>
                <strong>退款 {{ formatMoney(refund.amount) }} {{ refund.currency_code }}</strong
                ><span
                  >{{ refund.occurred_on
                  }}<template v-if="refund.notes"> · {{ refund.notes }}</template></span
                >
              </div>
              <div class="tf-actions">
                <IconAction
                  icon="edit"
                  :label="`编辑退款 ${formatMoney(refund.amount)} ${refund.currency_code}`"
                  text
                  :disabled="!!busy"
                  @click="dialog?.open(refund)"
                /><IconAction
                  icon="trash"
                  :label="`删除退款 ${formatMoney(refund.amount)} ${refund.currency_code}`"
                  text
                  type="danger"
                  :disabled="!!busy"
                  @click="remove(refund)"
                />
              </div>
            </li>
          </ul>
        </li>
      </ul>
      <div v-if="page.cursor.value" class="load-more">
        <ElAlert
          v-if="page.moreError.value"
          :title="page.moreError.value"
          type="error"
          :closable="false"
          show-icon
        />
        <ElButton :loading="page.loadingMore.value" @click="page.loadMore">加载更多</ElButton>
      </div>
    </template>

    <LedgerEntryDialog ref="dialog" v-model:categories="categories" @saved="saved" />
  </div>
</template>

<style scoped>
.entry-refund-summary {
  margin-top: 8px;
  padding: 4px 0;
  border: 0;
  background: transparent;
  color: var(--tf-success);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}
.entry-refund-summary:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 2px;
}
.entry-refunds {
  flex: 0 0 100%;
  grid-column: 1 / -1;
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 8px 0 0;
  padding: 12px;
  list-style: none;
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-sunken);
}
.entry-refunds li {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}
.entry-refunds li > div:first-child {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.entry-refunds span {
  color: var(--tf-text-3);
}
.entry-refunds .tf-actions {
  display: flex;
}
.ledger-tab {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.stats {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: 28px 40px;
}
.stat {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 96px;
}
.stat--hero {
  min-width: 180px;
}
.stat-label {
  font-size: 12px;
  color: var(--tf-text-3);
}
.stat-value {
  font-size: 30px;
  line-height: 1.1;
  color: var(--tf-text-1);
  font-variant-numeric: tabular-nums;
}
.stat-value--small {
  font-size: 20px;
}
.stat-value--over {
  color: var(--tf-danger);
}
.stat-unit {
  font-size: 12px;
  color: var(--tf-text-3);
}
.stat-hint {
  font-size: 12px;
  color: var(--tf-text-3);
}
.stat--budget {
  min-width: 150px;
}
.budget-value {
  display: flex;
  align-items: center;
  min-height: var(--tf-control-size);
  gap: 8px;
  background: none;
  border: 0;
  padding: 0;
  cursor: pointer;
  font: inherit;
  text-align: left;
}
.budget-edit-icon {
  color: var(--tf-accent);
}
.budget-value:focus-visible {
  outline-offset: 2px;
}
.budget-edit {
  display: flex;
  align-items: center;
  gap: 6px;
}
.budget-edit .el-button {
  margin-left: 0;
}
.budget-edit :deep(.el-input) {
  width: 130px;
}
.budget-error {
  font-size: 12px;
  color: var(--tf-danger);
}
.tab-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  flex-wrap: nowrap;
}
.ledger-controls {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.filters {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.filters .el-button {
  margin-left: 0;
}
.filters :deep(.filter-date) {
  width: 180px;
}
.filter-selects {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  width: 308px;
  max-width: 100%;
}
.filter-category,
.filter-split-mode {
  width: 100%;
  min-width: 0;
}
.tab-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.tab-actions .el-button {
  margin-left: 0;
}
.filter-panel-enter-active,
.filter-panel-leave-active {
  transition:
    opacity 160ms var(--tf-ease),
    transform 160ms var(--tf-ease);
}
.filter-panel-enter-from,
.filter-panel-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
.charts {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.2fr);
  gap: 16px;
}
.chart-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.chart-header h2 {
  margin: 0;
  font-size: 15px;
  color: var(--tf-text-1);
}
.chart-sub {
  font-size: 12px;
  color: var(--tf-text-3);
}
.category-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.category-table th,
.category-table td {
  text-align: left;
  padding: 8px 10px;
  border-bottom: 1px solid var(--tf-line-soft);
  font-variant-numeric: tabular-nums;
}
.category-table thead th {
  font-size: 12px;
  color: var(--tf-text-3);
  font-weight: 500;
}
.category-table .num {
  text-align: right;
}
.category-table tbody tr:last-child th,
.category-table tbody tr:last-child td {
  border-bottom: 0;
}
.category-row--active {
  background: var(--tf-accent-soft);
}
.category-link {
  background: none;
  border: 0;
  padding: 0;
  font: inherit;
  color: var(--tf-accent);
  cursor: pointer;
}
.category-link:hover {
  text-decoration: underline;
}
.tab-skeleton {
  padding: 24px;
  background: var(--tf-surface);
  border-radius: var(--tf-radius-control);
}
.retry-button {
  margin-top: 16px;
}
.entry-list {
  list-style: none;
  margin: 0;
  padding: 4px 16px;
}
.entry {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 14px;
  padding: 12px 0;
  border-bottom: 1px solid var(--tf-line-soft);
}
.entry:last-child {
  border-bottom: 0;
}
.entry-main {
  flex: 1;
  min-width: 0;
}
.entry-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.entry-title strong {
  overflow-wrap: anywhere;
}
.entry-linked {
  font-size: 12px;
  color: var(--tf-text-3);
}
.entry-meta {
  margin: 4px 0 0;
  display: flex;
  gap: 14px;
  font-size: 12px;
  color: var(--tf-text-3);
}
.entry-notes {
  overflow-wrap: anywhere;
}
.entry-amount {
  flex-shrink: 0;
  font-size: 18px;
  color: var(--tf-text-1);
  font-variant-numeric: tabular-nums;
}
.entry-amount--refund {
  color: var(--tf-success);
}
.entry-currency {
  font-size: 12px;
  color: var(--tf-text-3);
}
.entry-share {
  display: block;
  font-size: 12px;
  color: var(--tf-text-3);
  text-align: right;
}
.entry-actions {
  display: flex;
  flex-shrink: 0;
}
.entry-actions .el-button {
  margin-left: 0;
}
.load-more {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
@media (max-width: 960px) {
  .charts {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 600px) {
  .tab-toolbar {
    gap: 10px;
  }
  .filters {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    align-items: stretch;
  }
  .filters :deep(.filter-date) {
    width: 100%;
    min-width: 0;
  }
  .filter-selects {
    grid-column: 1 / -1;
    width: 100%;
  }
  .filters > .el-button {
    grid-column: 1 / -1;
    justify-self: start;
  }
  .entry {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .entry-main {
    grid-column: 1 / -1;
  }
  .entry-meta {
    flex-wrap: wrap;
    gap: 6px 14px;
  }
  .entry-amount {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .entry-actions {
    justify-self: end;
  }
}
@media (prefers-reduced-motion: reduce) {
  .filter-panel-enter-active,
  .filter-panel-leave-active {
    transition: none;
  }
}
</style>
