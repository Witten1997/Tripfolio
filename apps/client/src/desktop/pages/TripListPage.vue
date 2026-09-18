<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCard,
  ElEmpty,
  ElInput,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSkeleton,
  ElTag,
} from 'element-plus'
import { CalendarDays, MapPin, Plane, Route as RouteIcon } from '@lucide/vue'
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'

import tripCardLandscape from '@/assets/trip-card-landscape.jpg'
import CategoryManagerDialog from '@/desktop/components/CategoryManagerDialog.vue'
import IconAction from '@/desktop/components/IconAction.vue'
import TripEditorDialog from '@/desktop/components/TripEditorDialog.vue'
import { ApiError } from '@/shared/api/auth'
import {
  archiveTrip,
  listTrips,
  trashTrip,
  type Trip,
  type TripListItem,
  type TripQuery,
} from '@/shared/api/trips'
import {
  actionError,
  createWriteIntent,
  writeWarnings,
  type WriteOutcome,
} from '@/shared/api/writes'
import { useMetadataStore } from '@/shared/stores/metadata'
import { useCursorPage } from '@/shared/travel/useCursorPage'
import { recalculateRoutePlan } from '@/shared/api/routePlan'
import { formatDistance } from '@/shared/geo/itineraryRoute'
import { randomId } from '@/shared/randomId'

const metadata = useMetadataStore()
const editor = ref<InstanceType<typeof TripEditorDialog>>()
const categoryManager = ref<InstanceType<typeof CategoryManagerDialog>>()
const search = ref('')
const searchInput = ref<InstanceType<typeof ElInput>>()
const activeTool = ref<'search' | 'filters' | null>(null)
const filters = reactive({
  phase: '' as '' | TripListItem['phase'],
  archived: 'all' as NonNullable<TripQuery['archived']>,
  sort: 'start_date_desc' as NonNullable<TripQuery['sort']>,
  q: '',
})
const { items, cursor, loading, loadingMore, error, moreError, reload, loadMore } = useCursorPage(
  () => ({ ...filters, phase: filters.phase || undefined, q: filters.q || undefined, limit: 30 }),
  listTrips,
)
const phases = [
  { key: 'planned', label: '待出发', type: 'primary' },
  { key: 'ongoing', label: '旅行中', type: 'success' },
  { key: 'ended', label: '已结束', type: 'info' },
] as const
const groups = computed(() =>
  phases
    .map((phase) => ({ ...phase, trips: items.value.filter((trip) => trip.phase === phase.key) }))
    .filter((group) => group.trips.length),
)
const isFiltered = computed(() => !!filters.q || !!filters.phase || filters.archived !== 'all')
const feedback = ref<string[]>([])
const feedbackType = ref<'success' | 'warning'>('success')
const actionFailure = ref<string | null>(null)
const busy = ref<string | null>(null)
const intents = new Map<string, ReturnType<typeof createWriteIntent>>()
const sortAscending = computed(() => filters.sort === 'start_date_asc')
const requestedRoutePlans = new Set<string>()
const maxRouteRefreshAttempts = 12
let routeRefreshTimer: ReturnType<typeof setTimeout> | undefined
let routeRefreshAttempts = 0

function routeDistance(trip: TripListItem) {
  if (trip.route_status === 'ready' && trip.total_distance_meters != null)
    return formatDistance(trip.total_distance_meters)
  if (trip.route_status === 'empty') return '暂无路线'
  if (trip.route_status === 'incomplete') return '部分未算出'
  return '计算中'
}

function scheduleRouteRefresh() {
  if (routeRefreshTimer || routeRefreshAttempts >= maxRouteRefreshAttempts) return
  routeRefreshTimer = setTimeout(async () => {
    routeRefreshTimer = undefined
    routeRefreshAttempts += 1
    await reload()
    if (items.value.some((trip) => ['stale', 'calculating'].includes(trip.route_status)))
      scheduleRouteRefresh()
  }, 1800)
}

watch(
  items,
  (trips) => {
    for (const trip of trips) {
      if (trip.route_status !== 'stale' || requestedRoutePlans.has(trip.id)) continue
      requestedRoutePlans.add(trip.id)
      routeRefreshAttempts = 0
      void recalculateRoutePlan(trip.id, randomId())
        .then(scheduleRouteRefresh)
        .catch(() => undefined)
    }
    if (trips.some((trip) => ['stale', 'calculating'].includes(trip.route_status)))
      scheduleRouteRefresh()
  },
  { deep: false },
)

async function toggleTool(tool: 'search' | 'filters') {
  activeTool.value = activeTool.value === tool ? null : tool
  if (activeTool.value === 'search') {
    await nextTick()
    searchInput.value?.focus()
  }
}

function toggleSort() {
  filters.sort = sortAscending.value ? 'start_date_desc' : 'start_date_asc'
}

function applySearch() {
  const next = search.value.trim()
  if (filters.q === next) void reload()
  else filters.q = next
}

function clearFilters() {
  search.value = ''
  Object.assign(filters, { q: '', phase: '', archived: 'all' })
}

function formatTripDate(date: string) {
  return date
    .split('-')
    .map((part) => String(Number(part)))
    .join('.')
}

function tripDuration(startDate: string, endDate: string) {
  const start = Date.parse(`${startDate}T00:00:00Z`)
  const end = Date.parse(`${endDate}T00:00:00Z`)
  if (!Number.isFinite(start) || !Number.isFinite(end)) return '-'
  const days = Math.max(1, Math.round((end - start) / 86_400_000) + 1)
  return days === 1 ? '1天' : `${days}天${days - 1}晚`
}

async function saved(outcome: WriteOutcome<Trip>) {
  const warnings = writeWarnings(outcome.result)
  feedbackType.value = warnings.length ? 'warning' : 'success'
  feedback.value = ['旅行已保存。', ...warnings]
  actionFailure.value = null
  await reload()
}

async function mutate(trip: TripListItem, action: 'archive' | 'trash') {
  if (busy.value) return
  busy.value = trip.id
  actionFailure.value = null
  const slot = `${action}:${trip.id}`
  const intent = intents.get(slot) ?? createWriteIntent()
  intents.set(slot, intent)
  const archived = !trip.archived_at
  const operationId = intent.key({
    action,
    id: trip.id,
    version: trip.version,
    archived: action === 'archive' ? archived : undefined,
  })
  try {
    const outcome =
      action === 'archive'
        ? await archiveTrip(trip.id, trip.version, archived, operationId)
        : await trashTrip(trip.id, trip.version, operationId)
    intent.reset()
    const warnings = writeWarnings(outcome.result)
    feedbackType.value = warnings.length ? 'warning' : 'success'
    feedback.value = [
      action === 'trash'
        ? '整趟旅行已移入回收站，可在截止时间前恢复。'
        : archived
          ? '旅行已归档，仍可编辑。'
          : '已取消归档。',
      ...warnings,
    ]
    await reload()
  } catch (cause) {
    actionFailure.value = actionError(
      cause,
      '网络连接中断，结果尚未确认。可重试同一操作或刷新列表确认状态。',
    )
    if (
      cause instanceof ApiError &&
      ['VERSION_CONFLICT', 'TRIP_DELETED'].includes(cause.code ?? '')
    )
      await reload()
  } finally {
    busy.value = null
  }
}

async function confirmTrash(trip: TripListItem) {
  if (busy.value) return
  try {
    await ElMessageBox.confirm(
      `将“${trip.name}”及其行程、账目、行李、待办、预订资料和照片一起移入回收站。30 天内可恢复整趟旅行。`,
      '将整趟旅行移入回收站？',
      {
        type: 'warning',
        confirmButtonText: '确认移入回收站',
        cancelButtonText: '保留旅行',
      },
    )
  } catch {
    return
  }
  await mutate(trip, 'trash')
}

onMounted(() => {
  if (metadata.status === 'idle' || metadata.status === 'error') void metadata.load()
})

onUnmounted(() => {
  if (routeRefreshTimer) clearTimeout(routeRefreshTimer)
})

function openCreate() {
  editor.value?.open()
}

defineExpose({ openCreate })
</script>

<template>
  <div class="trip-list-page">
    <header class="page-heading">
      <div>
        <div class="page-title-row">
          <h1>我的旅行</h1>
          <Plane class="page-title-plane" aria-hidden="true" />
        </div>
        <p>每一次出发，都值得好好记录。</p>
      </div>
      <div class="heading-actions tf-actions">
        <div class="heading-primary-actions">
          <ElButton @click="categoryManager?.open()">账单分类</ElButton
          ><ElButton type="primary" @click="editor?.open()">新建旅行</ElButton>
        </div>
        <div class="heading-tool-actions" aria-label="旅行列表工具">
          <IconAction
            icon="search"
            label="搜索旅行"
            appearance="ghost"
            :aria-expanded="activeTool === 'search'"
            aria-controls="trip-search-panel"
            :class="{ 'is-active': activeTool === 'search' }"
            @click="toggleTool('search')"
          />
          <IconAction
            icon="filter"
            label="筛选旅行"
            appearance="ghost"
            :aria-expanded="activeTool === 'filters'"
            aria-controls="trip-filter-panel"
            :class="{ 'is-active': activeTool === 'filters' }"
            @click="toggleTool('filters')"
          />
          <IconAction
            :icon="sortAscending ? 'sort-asc' : 'sort-desc'"
            appearance="ghost"
            :label="
              sortAscending
                ? '当前按出发日期升序，点击切换为降序'
                : '当前按出发日期降序，点击切换为升序'
            "
            @click="toggleSort"
          />
        </div>
      </div>
    </header>
    <Transition name="tool-panel">
      <form
        v-if="activeTool === 'search'"
        id="trip-search-panel"
        class="trip-tool-panel search-panel"
        @submit.prevent="applySearch"
      >
        <ElInput
          id="trip-search"
          ref="searchInput"
          v-model="search"
          aria-label="搜索旅行"
          placeholder="旅行名称或目的地"
          maxlength="100"
          clearable
          @clear="applySearch"
        />
        <IconAction icon="search" label="执行搜索" appearance="ghost" @click="applySearch" />
      </form>
      <section
        v-else-if="activeTool === 'filters'"
        id="trip-filter-panel"
        class="trip-tool-panel filter-panel"
        aria-label="筛选旅行"
      >
        <div class="filter-field">
          <label for="phase-filter">旅行阶段</label
          ><ElSelect
            id="phase-filter"
            v-model="filters.phase"
            aria-label="旅行阶段"
            placeholder="全部阶段"
            ><ElOption label="全部阶段" value="" /><ElOption
              v-for="phase in phases"
              :key="phase.key"
              :label="phase.label"
              :value="phase.key"
          /></ElSelect>
        </div>
        <div class="filter-field">
          <label for="archive-filter">归档状态</label
          ><ElSelect id="archive-filter" v-model="filters.archived" aria-label="归档状态"
            ><ElOption label="全部旅行" value="all" /><ElOption
              label="未归档"
              value="false" /><ElOption label="已归档" value="true"
          /></ElSelect>
        </div>
      </section>
    </Transition>
    <ElAlert
      v-if="feedback.length"
      :type="feedbackType"
      :title="feedback.join(' ')"
      show-icon
      @close="feedback = []"
    />
    <ElAlert v-if="actionFailure" type="error" :title="actionFailure" :closable="false" show-icon />
    <ElSkeleton v-if="loading" :rows="7" animated class="list-skeleton" />
    <ElCard v-else-if="error" shadow="never"
      ><ElAlert :title="error" type="error" :closable="false" show-icon /><ElButton
        class="retry-button"
        @click="reload"
        >重新加载</ElButton
      ></ElCard
    >
    <ElCard v-else-if="!items.length" shadow="never"
      ><ElEmpty
        :description="isFiltered ? '没有符合条件的旅行' : '还没有旅行，开始计划下一次出发吧'"
      >
        <ElButton v-if="isFiltered" @click="clearFilters">清除筛选</ElButton
        ><ElButton v-else type="primary" @click="editor?.open()">新建旅行</ElButton>
      </ElEmpty></ElCard
    >
    <template v-else>
      <section
        v-for="group in groups"
        :key="group.key"
        class="trip-group"
        :aria-labelledby="`phase-${group.key}`"
      >
        <h2 :id="`phase-${group.key}`">
          <ElTag :type="group.type" effect="plain">{{ group.label }}</ElTag
          ><span>{{ group.trips.length }} 趟</span>
        </h2>
        <div class="trip-grid">
          <ElCard
            v-for="trip in group.trips"
            :key="trip.id"
            shadow="hover"
            class="trip-card tf-card"
          >
            <div class="trip-card-layout">
              <img class="trip-card-art" :src="tripCardLandscape" alt="" aria-hidden="true" />
              <div class="trip-card-main">
                <div class="trip-card-title">
                  <MapPin class="trip-title-icon" aria-hidden="true" />
                  <h3>
                    <RouterLink
                      :to="{ name: 'trip-detail', params: { tripId: trip.id } }"
                      class="trip-link"
                      >{{ trip.name }}</RouterLink
                    >
                  </h3>
                  <ElTag v-if="trip.archived_at" type="info" size="small">已归档</ElTag>
                </div>
                <p class="trip-dates">
                  <CalendarDays aria-hidden="true" />
                  <span
                    >{{ formatTripDate(trip.start_date) }} -
                    {{ formatTripDate(trip.end_date) }}</span
                  >
                </p>
                <div class="trip-actions tf-actions">
                  <IconAction
                    :to="{ name: 'trip-detail', params: { tripId: trip.id } }"
                    icon="eye"
                    :label="`查看详情：${trip.name}`"
                    appearance="ghost"
                  />
                  <IconAction
                    icon="edit"
                    :label="`编辑旅行：${trip.name}`"
                    appearance="ghost"
                    :disabled="!!busy"
                    @click="editor?.open(trip)"
                  />
                  <IconAction
                    :icon="trip.archived_at ? 'archive-restore' : 'archive'"
                    :label="`${trip.archived_at ? '取消归档' : '归档'}：${trip.name}`"
                    appearance="ghost"
                    :disabled="!!busy && busy !== trip.id"
                    :loading="busy === trip.id"
                    @click="mutate(trip, 'archive')"
                  />
                  <IconAction
                    icon="trash"
                    :label="`移入回收站：${trip.name}`"
                    appearance="ghost"
                    class="trip-action--danger"
                    type="danger"
                    :disabled="!!busy"
                    @click="confirmTrash(trip)"
                  />
                </div>
              </div>
              <aside class="trip-card-summary" :aria-label="`${trip.name}旅行概览`">
                <div class="trip-summary-mark" aria-hidden="true">
                  <span>TRIPFOLIO</span>
                  <RouteIcon />
                </div>
                <dl class="trip-metrics">
                  <div>
                    <dt>时长</dt>
                    <dd>{{ tripDuration(trip.start_date, trip.end_date) }}</dd>
                  </div>
                  <div>
                    <dt>里程</dt>
                    <dd>{{ routeDistance(trip) }}</dd>
                  </div>
                </dl>
              </aside>
            </div>
          </ElCard>
        </div>
      </section>
      <div class="pagination-area">
        <span>已加载 {{ items.length }} 趟旅行</span
        ><ElAlert v-if="moreError" :title="moreError" type="error" :closable="false" /><ElButton
          v-if="cursor"
          :loading="loadingMore"
          @click="loadMore"
          >{{ moreError ? '重试加载下一页' : '加载更多' }}</ElButton
        ><span v-else>已显示全部旅行</span>
      </div>
    </template>
    <TripEditorDialog ref="editor" @saved="saved" />
    <CategoryManagerDialog ref="categoryManager" />
  </div>
</template>

<style scoped>
.trip-list-page {
  box-sizing: border-box;
  width: 100%;
  max-width: 1280px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 26px;
  padding: 18px 8px 52px;
}
.page-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 4px 2px 12px;
}
.page-title-row {
  display: flex;
  align-items: center;
  gap: 12px;
}
.page-heading h1 {
  margin: 0;
  color: color-mix(in srgb, var(--tf-accent) 72%, var(--tf-text-1));
  font-family: var(--tf-font-display);
  font-size: clamp(32px, 3vw, 42px);
  font-weight: 750;
  line-height: 1.08;
}
.page-title-plane {
  width: 34px;
  height: 34px;
  color: var(--tf-accent);
  stroke-width: 1.7;
  transform: rotate(-14deg);
}
.page-heading p {
  margin: 11px 0 0;
  color: var(--tf-text-3);
  font-size: 16px;
  line-height: 1.5;
}
.heading-actions {
  display: flex;
  flex-shrink: 0;
  gap: 8px;
}
.heading-primary-actions,
.heading-tool-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.heading-primary-actions .el-button {
  margin-left: 0;
}
.heading-tool-actions :deep(.is-active) {
  background: var(--tf-accent-soft);
  color: var(--tf-accent);
}
.trip-tool-panel {
  box-sizing: border-box;
  display: flex;
  align-items: end;
  gap: 12px;
  width: 100%;
  padding: 14px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface);
  box-shadow: var(--tf-shadow-1);
}
.search-panel .el-input {
  flex: 1;
}
.filter-panel {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 220px));
  justify-content: end;
}
.filter-field label {
  display: block;
  margin-bottom: 7px;
  font-size: 12px;
  color: var(--tf-text-2);
}
.filter-field .el-select {
  width: 100%;
}
.tool-panel-enter-active,
.tool-panel-leave-active {
  transition:
    opacity var(--tf-duration-fast) var(--tf-ease),
    transform var(--tf-duration-fast) var(--tf-ease);
}
.tool-panel-enter-from,
.tool-panel-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
.list-skeleton {
  box-sizing: border-box;
  padding: 24px;
  background: var(--tf-surface);
  border-radius: var(--tf-radius-control);
}
.retry-button {
  margin-top: 16px;
}
.trip-group h2 {
  margin: 0 0 18px;
  display: flex;
  gap: 12px;
  align-items: center;
}
.trip-group h2 :deep(.el-tag) {
  min-height: 40px;
  padding: 0 19px;
  border-color: color-mix(in srgb, var(--tf-surface-raised) 64%, transparent);
  border-radius: 999px;
  background: color-mix(in srgb, var(--tf-surface-raised) 46%, transparent);
  color: var(--tf-accent);
  box-shadow:
    0 10px 24px -16px color-mix(in srgb, var(--tf-accent) 62%, transparent),
    inset 0 1px 0 color-mix(in srgb, var(--tf-surface-raised) 85%, transparent);
  -webkit-backdrop-filter: blur(10px) saturate(120%);
  backdrop-filter: blur(10px) saturate(120%);
  font-size: 15px;
  font-weight: 750;
}
.trip-group h2 > span:last-child {
  font-size: 14px;
  font-weight: normal;
  color: var(--tf-text-3);
}
.trip-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(520px, 1fr));
  gap: 24px;
}
.trip-card {
  position: relative;
  isolation: isolate;
  overflow: hidden;
  border-color: color-mix(in srgb, var(--tf-surface-raised) 42%, transparent);
  border-radius: 28px;
  background: color-mix(in srgb, var(--tf-surface-raised) 20%, transparent);
  box-shadow:
    0 3px 7px color-mix(in srgb, var(--tf-accent) 10%, transparent),
    0 24px 52px -23px color-mix(in srgb, var(--tf-accent) 30%, transparent),
    inset 0 1px 0 color-mix(in srgb, var(--tf-surface-raised) 82%, transparent),
    inset 1px 0 0 color-mix(in srgb, var(--tf-surface-raised) 48%, transparent),
    inset 0 -1px 0 color-mix(in srgb, var(--tf-accent) 14%, transparent);
  transition:
    transform var(--tf-duration) var(--tf-ease),
    box-shadow var(--tf-duration) var(--tf-ease);
}
.trip-card:hover {
  transform: translateY(-1px);
  box-shadow:
    0 4px 9px color-mix(in srgb, var(--tf-accent) 12%, transparent),
    0 28px 58px -24px color-mix(in srgb, var(--tf-accent) 34%, transparent),
    inset 0 1px 0 color-mix(in srgb, var(--tf-surface-raised) 88%, transparent),
    inset 1px 0 0 color-mix(in srgb, var(--tf-surface-raised) 52%, transparent),
    inset 0 -1px 0 color-mix(in srgb, var(--tf-accent) 15%, transparent);
}
@supports (mask-composite: exclude) or (-webkit-mask-composite: xor) {
  .trip-card {
    border-color: transparent;
  }
  .trip-card::before {
    position: absolute;
    inset: 0;
    z-index: 3;
    box-sizing: border-box;
    padding: 1px;
    border-radius: inherit;
    background: linear-gradient(
      135deg,
      color-mix(in srgb, var(--tf-surface-raised) 96%, transparent) 0%,
      color-mix(in srgb, var(--tf-surface-raised) 78%, transparent) 18%,
      color-mix(in srgb, var(--tf-surface-raised) 34%, transparent) 42%,
      color-mix(in srgb, var(--tf-surface-raised) 18%, transparent) 64%,
      color-mix(in srgb, var(--tf-accent) 18%, transparent) 86%,
      color-mix(in srgb, var(--tf-accent) 12%, transparent) 100%
    );
    content: '';
    pointer-events: none;
    -webkit-mask:
      linear-gradient(var(--tf-text-1) 0 0) content-box,
      linear-gradient(var(--tf-text-1) 0 0);
    mask:
      linear-gradient(var(--tf-text-1) 0 0) content-box,
      linear-gradient(var(--tf-text-1) 0 0);
    -webkit-mask-composite: xor;
    mask-composite: exclude;
  }
}
.trip-card :deep(.el-card__body) {
  position: relative;
  z-index: 2;
  padding: 0;
  box-sizing: border-box;
  overflow: visible;
}
.trip-card-layout {
  position: relative;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 164px;
  min-height: 224px;
  overflow: hidden;
  border-radius: inherit;
}
.trip-card-layout::after {
  position: absolute;
  inset: 0;
  z-index: 1;
  background:
    linear-gradient(
      90deg,
      color-mix(in srgb, var(--tf-accent-soft) 56%, transparent) 0%,
      color-mix(in srgb, var(--tf-accent-soft) 24%, transparent) 54%,
      transparent 76%
    ),
    linear-gradient(
      180deg,
      color-mix(in srgb, var(--tf-surface-raised) 26%, transparent),
      transparent 44%,
      color-mix(in srgb, var(--tf-accent-soft) 12%, transparent)
    );
  content: '';
  pointer-events: none;
}
.trip-card-art {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
  object-position: 50% 58%;
  opacity: 0.82;
  pointer-events: none;
}
.trip-card-main {
  position: relative;
  z-index: 2;
  display: flex;
  min-width: 0;
  padding: 22px 24px 18px;
  flex-direction: column;
}
.trip-card-title {
  display: flex;
  align-items: center;
  gap: 10px;
}
.trip-title-icon {
  flex: 0 0 auto;
  width: 26px;
  height: 26px;
  color: var(--tf-warning);
  filter: drop-shadow(0 2px 5px color-mix(in srgb, var(--tf-warning) 16%, transparent));
  stroke-width: 2.4;
}
.trip-card h3 {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 24px;
  line-height: 1.3;
  overflow-wrap: anywhere;
}
.trip-link {
  color: color-mix(in srgb, var(--tf-accent) 54%, var(--tf-text-1));
  text-decoration: none;
}
.trip-link:hover {
  color: var(--tf-accent);
}
.trip-dates {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 13px 0 0 36px;
  color: var(--tf-text-2);
  font-size: 15px;
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  line-height: 1.5;
}
.trip-dates svg {
  width: 18px;
  height: 18px;
  flex: 0 0 auto;
  color: var(--tf-accent);
  stroke-width: 1.8;
}
.trip-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-start;
  align-self: flex-start;
  gap: 10px;
  margin-top: auto;
  padding: 7px 9px;
  border: 0;
  border-radius: 18px;
  background: color-mix(in srgb, var(--tf-accent-soft) 34%, transparent);
  box-shadow: none;
  -webkit-backdrop-filter: blur(10px) saturate(115%);
  backdrop-filter: blur(10px) saturate(115%);
}
.trip-actions .el-button {
  margin-left: 0;
}
.trip-actions :deep(.trip-action--danger) {
  color: var(--tf-danger);
}
.trip-actions :deep(.trip-action--danger:hover) {
  background: var(--tf-danger-soft);
  color: var(--tf-danger);
}
.trip-card-summary {
  position: relative;
  z-index: 2;
  display: flex;
  min-width: 0;
  padding: 18px 16px;
  flex-direction: column;
  background: color-mix(in srgb, var(--tf-warning-soft) 48%, transparent);
  border-left: 1px dashed color-mix(in srgb, var(--tf-accent) 34%, transparent);
  box-shadow: inset 1px 0 0 color-mix(in srgb, var(--tf-surface-raised) 28%, transparent);
  -webkit-backdrop-filter: blur(11px) saturate(115%);
  backdrop-filter: blur(11px) saturate(115%);
}
.trip-card-summary::before,
.trip-card-summary::after {
  position: absolute;
  left: 0;
  z-index: 1;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: var(--tf-canvas);
  content: '';
}
.trip-card-summary::before {
  top: 0;
  transform: translate(-50%, -50%);
}
.trip-card-summary::after {
  bottom: 0;
  transform: translate(-50%, 50%);
}
.trip-summary-mark {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding-bottom: 10px;
  border-bottom: 1px dashed color-mix(in srgb, var(--tf-text-1) 34%, transparent);
  color: var(--tf-text-2);
  font-size: 10px;
  font-weight: 800;
  letter-spacing: 0.06em;
}
.trip-summary-mark svg {
  width: 20px;
  height: 20px;
}
.trip-metrics {
  display: flex;
  flex: 1;
  margin: 0;
  padding: 12px 0 0;
  flex-direction: column;
  justify-content: space-between;
  gap: 10px;
  text-align: right;
}
.trip-metrics div {
  min-width: 0;
}
.trip-metrics dt {
  color: var(--tf-text-2);
  font-size: 11px;
  font-weight: 600;
}
.trip-metrics dd {
  margin: 3px 0 0;
  color: var(--tf-text-1);
  font-size: 16px;
  font-weight: 800;
  line-height: 1.25;
  overflow-wrap: anywhere;
}
.pagination-area {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  color: var(--tf-text-3);
  font-size: 12px;
  padding: 8px;
}
@media (min-width: 601px) {
  .trip-grid {
    grid-template-columns: repeat(auto-fill, minmax(347px, 1fr));
    gap: 16px;
  }
  .trip-card {
    border-radius: 19px;
  }
  .trip-card-layout {
    grid-template-columns: minmax(0, 1fr) 109px;
    min-height: 149px;
  }
  .trip-card-main {
    padding: 15px 16px 12px;
  }
  .trip-card-title {
    gap: 7px;
  }
  .trip-title-icon {
    width: 17px;
    height: 17px;
    stroke-width: 2;
  }
  .trip-card h3 {
    font-size: 16px;
  }
  .trip-dates {
    gap: 5px;
    margin: 9px 0 0 24px;
    font-size: 10px;
  }
  .trip-dates svg {
    width: 12px;
    height: 12px;
  }
  .trip-actions {
    gap: 7px;
    padding: 5px 6px;
    border-radius: 12px;
  }
  .trip-actions :deep(.icon-action) {
    width: 27px;
    height: 27px;
    min-height: 27px;
    border-radius: 8px;
  }
  .trip-card-summary {
    padding: 12px 11px;
  }
  .trip-card-summary::before,
  .trip-card-summary::after {
    width: 13px;
    height: 13px;
  }
  .trip-summary-mark {
    gap: 5px;
    padding-bottom: 7px;
    font-size: 8px;
  }
  .trip-summary-mark svg {
    width: 13px;
    height: 13px;
  }
  .trip-metrics {
    gap: 7px;
    padding-top: 8px;
  }
  .trip-metrics dt {
    font-size: 9px;
  }
  .trip-metrics dd {
    margin-top: 2px;
    font-size: 11px;
  }
}
@media (max-width: 600px) {
  .trip-list-page {
    gap: 20px;
    padding: 0;
  }
  .page-heading {
    align-items: flex-start;
  }
  .page-title-row {
    gap: 9px;
  }
  .page-title-plane {
    width: 28px;
    height: 28px;
  }
  .trip-grid {
    grid-template-columns: 1fr;
    gap: 18px;
  }
  .trip-card {
    border-radius: 24px;
  }
  .trip-card-layout {
    grid-template-columns: minmax(0, 1fr) 112px;
    min-height: 160px;
  }
  .trip-card-main {
    padding: 12px 12px 10px;
  }
  .trip-card-summary {
    padding: 10px 9px;
  }
  .trip-summary-mark span {
    font-size: 9px;
  }
  .trip-card h3 {
    font-size: 20px;
  }
  .trip-dates {
    gap: 6px;
    margin: 10px 0 0;
    font-size: 13px;
  }
  .trip-actions {
    align-self: stretch;
    justify-content: space-between;
    flex-wrap: nowrap;
    gap: 2px;
    padding: 3px 5px;
    border-radius: 17px;
  }
  .trip-actions :deep(.icon-action) {
    width: 36px;
    height: 36px;
    min-height: 36px;
  }
  .trip-metrics dt {
    font-size: 10px;
  }
  .trip-metrics dd {
    font-size: 13px;
  }
  .filter-panel {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 360px) {
  .trip-card-layout {
    grid-template-columns: minmax(0, 1fr) 96px;
    min-height: 176px;
  }
  .trip-card-main {
    z-index: 3;
    padding-bottom: 60px;
  }
  .trip-card-summary {
    padding: 8px 7px 56px 9px;
  }
  .trip-summary-mark {
    padding-bottom: 8px;
  }
  .trip-metrics {
    gap: 4px;
    padding-top: 8px;
  }
  .trip-metrics dt {
    font-size: 11px;
  }
  .trip-dates {
    font-size: 12px;
  }
  .trip-actions {
    position: absolute;
    bottom: 10px;
    left: 15px;
    z-index: 4;
    width: calc(100% + 66px);
  }
  .trip-actions :deep(.icon-action) {
    width: 40px;
    height: 40px;
    min-height: 40px;
  }
  .trip-metrics dd {
    font-size: 12px;
    white-space: nowrap;
  }
}
</style>
