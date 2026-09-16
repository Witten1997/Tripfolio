<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCard,
  ElDialog,
  ElDrawer,
  ElEmpty,
  ElInputNumber,
  ElMessageBox,
  ElRadioButton,
  ElRadioGroup,
  ElSkeleton,
  ElTag,
} from 'element-plus'
import { Bike, CarFront, ChevronRight, Footprints, RotateCcw, Route, Settings2 } from '@lucide/vue'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { VueDraggable, type DraggableEvent } from 'vue-draggable-plus'

import IconAction from '@/desktop/components/IconAction.vue'
import ItineraryItemDialog from '@/desktop/components/ItineraryItemDialog.vue'
import { ApiError } from '@/shared/api/auth'
import { formatDistance, formatDuration } from '@/shared/geo/itineraryRoute'
import {
  deleteItineraryItem,
  itineraryKindLabels,
  itineraryStatusLabels,
  type ItineraryItem,
} from '@/shared/api/itinerary'
import {
  getRoutePlan,
  recalculateRoutePlan,
  updateRouteLegMode,
  type RouteLeg,
  type RouteLegMode,
  type RoutePlan,
} from '@/shared/api/routePlan'
import { updateTrip } from '@/shared/api/trips'
import { randomId } from '@/shared/randomId'
import {
  actionError,
  createWriteIntent,
  writeWarnings,
  type WriteOutcome,
} from '@/shared/api/writes'
import { useTripContext } from '@/shared/travel/tripContext'
import { useItineraryBoard } from '@/shared/travel/useItineraryBoard'

const context = useTripContext()
const trip = context.trip
const board = useItineraryBoard(context.tripId, () => ({
  start: trip.value?.start_date ?? '',
  end: trip.value?.end_date ?? '',
}))
const { days, loading, error, reordering, actionFailure, feedback } = board
const dialog = ref<InstanceType<typeof ItineraryItemDialog>>()
const busy = ref<string | null>(null)
const notice = ref<string[]>([])
const noticeType = ref<'success' | 'warning'>('success')
const intents = new Map<string, ReturnType<typeof createWriteIntent>>()
const routePlan = ref<RoutePlan | null>(null)
const routeFailure = ref<string | null>(null)
const routeDrawerOpen = ref(false)
const preferenceOpen = ref(false)
const selectedLegId = ref<string | null>(null)
const routeBusy = ref(false)
const shortMode = ref<'walking' | 'cycling'>('walking')
const shortDistanceKm = ref(1.5)
const isMobile = ref(false)
const maxRoutePollAttempts = 20
let routePoll: ReturnType<typeof setTimeout> | undefined
let mediaQuery: MediaQueryList | undefined
let mediaQueryListener: (() => void) | undefined
let routePollAttempts = 0
let recalculationRequested = false

const modeMeta = {
  auto: { label: '自动选择', icon: RotateCcw },
  driving: { label: '驾车', icon: CarFront },
  walking: { label: '步行', icon: Footprints },
  cycling: { label: '骑行', icon: Bike },
} as const
const routeModes = Object.keys(modeMeta) as RouteLegMode[]
const byOrigin = computed(
  () => new Map((routePlan.value?.legs ?? []).map((leg) => [leg.from_item_id, leg])),
)
const byDestination = computed(
  () => new Map((routePlan.value?.legs ?? []).map((leg) => [leg.to_item_id, leg])),
)
const itemById = computed(() => new Map(board.items.value.map((item) => [item.id, item])))
const selectedLeg = computed(
  () => routePlan.value?.legs.find((leg) => leg.id === selectedLegId.value) ?? null,
)
const selectedMode = computed<RouteLegMode>(() =>
  selectedLeg.value?.mode_source === 'preference' ? 'auto' : (selectedLeg.value?.mode ?? 'auto'),
)

/**
 * 拖动组件需要可变数组：每天维护一份镜像。VueDraggable 在挂载时读取 v-model，
 * 值不是数组会被当成选项而不初始化，所以镜像必须在首次渲染前就按天铺满（immediate + sync）。
 * 拖动结束后 board 重载，days 变化，镜像随之按服务端结果重建。
 */
const lists = ref<Record<string, ItineraryItem[]>>({})
watch(
  days,
  (next) => {
    lists.value = Object.fromEntries(next.map((d) => [d.date, [...d.items]]))
  },
  { immediate: true, flush: 'sync' },
)
const total = computed(() => board.items.value.length)
const isToday = (date: string) => date === context.today.value

async function reloadAll() {
  await board.reload()
  await loadRoutePlan()
}

function scheduleRoutePoll() {
  if (routePoll || routePollAttempts >= maxRoutePollAttempts) return
  routePoll = setTimeout(async () => {
    routePoll = undefined
    routePollAttempts += 1
    await loadRoutePlan()
  }, 1400)
}

async function loadRoutePlan() {
  try {
    routePlan.value = await getRoutePlan(context.tripId)
    routeFailure.value = null
    if (routePlan.value.summary.status === 'stale' && !recalculationRequested) {
      recalculationRequested = true
      routePollAttempts = 0
      await recalculateRoutePlan(context.tripId, randomId())
    }
    if (!['stale', 'calculating'].includes(routePlan.value.summary.status)) {
      recalculationRequested = false
      routePollAttempts = 0
    }
    if (['stale', 'calculating'].includes(routePlan.value.summary.status)) scheduleRoutePoll()
  } catch (cause) {
    routeFailure.value = actionError(cause, '路线信息暂时无法加载。')
  }
}

function legDescription(leg: RouteLeg) {
  if (
    leg.status === 'ready' &&
    leg.route_distance_meters != null &&
    leg.route_duration_seconds != null
  )
    return `${formatDistance(leg.route_distance_meters)} · ${formatDuration(leg.route_duration_seconds)}`
  if (leg.status === 'failed') return '路线暂未算出'
  return '正在计算路线'
}

function compactLegDescription(leg: RouteLeg) {
  if (
    leg.status === 'ready' &&
    leg.route_distance_meters != null &&
    leg.route_duration_seconds != null
  ) {
    const distance =
      leg.route_distance_meters < 1000
        ? `${Math.round(leg.route_distance_meters)}m`
        : `${(leg.route_distance_meters / 1000).toFixed(1)}km`
    const minutes = Math.ceil(leg.route_duration_seconds / 60)
    const duration =
      minutes === 0
        ? '0min'
        : minutes < 60
        ? `${minutes}min`
        : `${Math.floor(minutes / 60)}h${minutes % 60 ? `${minutes % 60}min` : ''}`
    return `${distance} · ${duration}`
  }
  if (leg.status === 'failed') return '路线暂未算出'
  return '正在计算路线'
}

function isCrossDayLeg(leg: RouteLeg) {
  return (
    itemById.value.get(leg.from_item_id)?.scheduled_on !==
    itemById.value.get(leg.to_item_id)?.scheduled_on
  )
}

function routeAriaLabel(leg: RouteLeg) {
  const from = itemById.value.get(leg.from_item_id)?.title ?? '上一站'
  const to = itemById.value.get(leg.to_item_id)?.title ?? '下一站'
  return `查看从 ${from} 到 ${to} 的路线`
}

function openRouteLeg(leg: RouteLeg) {
  selectedLegId.value = leg.id
  routeDrawerOpen.value = true
}

function openPreferences() {
  shortMode.value = (trip.value?.route_short_mode as 'walking' | 'cycling') ?? 'walking'
  shortDistanceKm.value = (trip.value?.route_short_distance_meters ?? 1500) / 1000
  preferenceOpen.value = true
}

async function chooseRouteMode(mode: RouteLegMode) {
  const leg = selectedLeg.value
  if (!leg || routeBusy.value || selectedMode.value === mode) return
  routeBusy.value = true
  try {
    await updateRouteLegMode(context.tripId, leg.id, leg.version, mode, randomId())
    routeDrawerOpen.value = false
    await loadRoutePlan()
  } catch (cause) {
    routeFailure.value = actionError(cause, '交通方式保存失败，请稍后重试。')
  } finally {
    routeBusy.value = false
  }
}

async function savePreferences() {
  if (!trip.value || routeBusy.value) return
  routeBusy.value = true
  try {
    const result = await updateTrip(
      context.tripId,
      trip.value.version,
      {
        route_short_mode: shortMode.value,
        route_short_distance_meters: Math.round(shortDistanceKm.value * 1000),
      },
      randomId(),
    )
    if (result.resource) context.replace(result.resource)
    preferenceOpen.value = false
    await loadRoutePlan()
  } catch (cause) {
    routeFailure.value = actionError(cause, '交通偏好保存失败，请稍后重试。')
  } finally {
    routeBusy.value = false
  }
}

function jumpToToday() {
  const el = document.getElementById(`day-${context.today.value}`)
  el?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

async function onDragEnd(event: DraggableEvent<ItineraryItem>) {
  const from = event.from.dataset.date
  const to = event.to.dataset.date
  const id = event.data?.id
  if (!from || !to || !id || event.newIndex === undefined) {
    await reloadAll()
    return
  }
  // moveItem 成功或失败都会重载 board，镜像由 days 的 watcher 重建
  const moved = await board.moveItem(id, to, event.newIndex)
  if (moved && feedback.value.length) {
    noticeType.value = 'warning'
    notice.value = feedback.value
  }
}

async function saved(outcome: WriteOutcome<ItineraryItem>) {
  const warnings = writeWarnings(outcome.result)
  noticeType.value = warnings.length ? 'warning' : 'success'
  notice.value = ['行程已保存。', ...warnings]
  actionFailure.value = null
  await reloadAll()
}

async function remove(item: ItineraryItem) {
  if (busy.value || reordering.value) return
  try {
    await ElMessageBox.confirm(`删除“${item.title}”不影响其他记录。`, '删除这条行程？', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '保留',
    })
  } catch {
    return
  }
  busy.value = item.id
  actionFailure.value = null
  const intent = intents.get(item.id) ?? createWriteIntent()
  intents.set(item.id, intent)
  try {
    await deleteItineraryItem(
      context.tripId,
      item.id,
      item.version,
      intent.key({ delete: item.id, version: item.version }),
    )
    intent.reset()
    noticeType.value = 'success'
    notice.value = ['行程已删除。']
    await reloadAll()
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

function timeLabel(item: ItineraryItem) {
  const start = item.planned_start_local?.slice(11, 16)
  if (item.planned_end_local) {
    const sameDay = item.planned_end_local.slice(0, 10) === item.scheduled_on
    const end = sameDay
      ? item.planned_end_local.slice(11, 16)
      : item.planned_end_local.slice(5, 16).replace('T', ' ')
    return start ? `${start} - ${end}` : `至 ${end}`
  }
  if (item.planned_duration_minutes) {
    return start
      ? `${start} · ${item.planned_duration_minutes} 分钟`
      : `${item.planned_duration_minutes} 分钟`
  }
  return start ?? ''
}

onMounted(async () => {
  if (typeof window !== 'undefined' && window.matchMedia) {
    mediaQuery = window.matchMedia('(max-width: 767px)')
    mediaQueryListener = () => (isMobile.value = mediaQuery?.matches ?? false)
    mediaQueryListener()
    mediaQuery.addEventListener('change', mediaQueryListener)
  }
  await reloadAll()
})

onUnmounted(() => {
  if (routePoll) clearTimeout(routePoll)
  if (mediaQueryListener) mediaQuery?.removeEventListener('change', mediaQueryListener)
})
</script>

<template>
  <div class="itinerary-tab">
    <div class="tab-toolbar">
      <span class="tab-summary">
        共 {{ total }} 条行程 · 拖动卡片调整顺序或移到其他日期
        <template v-if="reordering">，正在保存排序…</template>
      </span>
      <div class="tab-actions tf-actions">
        <IconAction
          icon="globe"
          label="地图视图"
          :to="{
            name: 'trip-map',
            params: { tripId: context.tripId },
          }"
        />
        <ElButton v-if="days.some((d) => isToday(d.date))" size="small" @click="jumpToToday"
          >今日行程</ElButton
        >
        <IconAction
          icon="plus"
          label="新建行程"
          type="primary"
          @click="dialog?.open(undefined, trip?.start_date)"
        />
      </div>
    </div>
    <div v-if="routePlan" class="route-options">
      <div>
        <Route aria-hidden="true" />
        <span>
          相邻点位路线
          <template
            v-if="
              routePlan.summary.status === 'ready' &&
              routePlan.summary.total_distance_meters != null
            "
          >
            · {{ formatDistance(routePlan.summary.total_distance_meters) }}
          </template>
          <template v-else-if="routePlan.summary.status === 'incomplete'">
            · 部分路线未完成</template
          >
          <template v-else-if="['stale', 'calculating'].includes(routePlan.summary.status)">
            · 计算中</template
          >
        </span>
      </div>
      <ElButton size="small" :icon="Settings2" @click="openPreferences">交通偏好</ElButton>
    </div>
    <ElAlert
      v-if="routeFailure"
      type="warning"
      :title="routeFailure"
      show-icon
      @close="routeFailure = null"
    />
    <ElAlert
      v-if="notice.length"
      :type="noticeType"
      :title="notice.join(' ')"
      show-icon
      @close="notice = []"
    />
    <ElAlert v-if="actionFailure" type="error" :title="actionFailure" :closable="false" show-icon />
    <ElSkeleton v-if="loading && !total" :rows="8" animated class="tab-skeleton" />
    <ElCard v-else-if="error" shadow="never">
      <ElAlert :title="error" type="error" :closable="false" show-icon />
      <ElButton class="retry-button" @click="reloadAll">重新加载</ElButton>
    </ElCard>
    <div v-else class="day-list" :class="{ 'day-list--busy': reordering }">
      <section
        v-for="day in days"
        :id="`day-${day.date}`"
        :key="day.date"
        class="day tf-surface"
        :class="{ 'day--today': isToday(day.date), 'day--outside': day.outside }"
      >
        <header class="day-header">
          <h2>
            {{ day.title }}
            <ElTag v-if="isToday(day.date)" type="success" size="small" effect="plain">今天</ElTag>
            <ElTag v-if="day.outside" type="warning" size="small" effect="plain">旅行日期外</ElTag>
          </h2>
          <span class="day-count">{{ day.items.length }} 项</span>
          <IconAction
            icon="plus"
            :label="`为 ${day.title} 添加行程`"
            text
            :disabled="reordering"
            @click="dialog?.open(undefined, day.date)"
          />
        </header>
        <VueDraggable
          v-model="lists[day.date]"
          group="itinerary"
          :data-date="day.date"
          :disabled="reordering || !!busy"
          :animation="150"
          :force-fallback="true"
          handle=".drag-handle"
          class="day-items"
          ghost-class="item--ghost"
          @end="onDragEnd"
        >
          <div
            v-for="item in lists[day.date] ?? []"
            :key="item.id"
            class="item-group"
          >
            <button
              v-if="byDestination.get(item.id) && isCrossDayLeg(byDestination.get(item.id)!)"
              type="button"
              class="route-connector route-connector--cross-day"
              :aria-label="routeAriaLabel(byDestination.get(item.id)!)"
              @click="openRouteLeg(byDestination.get(item.id)!)"
            >
              <component
                :is="modeMeta[byDestination.get(item.id)!.mode].icon"
                aria-hidden="true"
              />
              <span>{{ modeMeta[byDestination.get(item.id)!.mode].label }}</span>
              <strong>{{ compactLegDescription(byDestination.get(item.id)!) }}</strong>
              <ChevronRight aria-hidden="true" />
            </button>
            <article
              class="item"
              :class="[`item--${item.status}`, `item--kind-${item.kind}`]"
            >
            <button
              type="button"
              class="drag-handle"
              :aria-label="`拖动 ${item.title}`"
              :disabled="reordering"
            >
              ⋮⋮
            </button>
            <div class="item-body">
              <div class="item-title">
                <ElTag size="small" effect="plain">{{ itineraryKindLabels[item.kind] }}</ElTag>
                <strong>{{ item.title }}</strong>
                <ElTag
                  v-if="item.status !== 'pending'"
                  size="small"
                  :type="item.status === 'completed' ? 'success' : 'info'"
                  >{{ itineraryStatusLabels[item.status] }}</ElTag
                >
              </div>
              <p v-if="timeLabel(item)" class="item-time">{{ timeLabel(item) }}</p>
              <p v-if="item.place_name || item.address" class="item-place">
                {{ item.place_name }}<span v-if="item.place_name && item.address"> · </span
                >{{ item.address }}
              </p>
              <p v-if="item.estimated_amount" class="item-amount">
                预计 {{ item.currency_code }} {{ item.estimated_amount }}
              </p>
              <p v-if="item.notes" class="item-notes">{{ item.notes }}</p>
            </div>
            <div class="item-actions tf-actions">
              <IconAction
                icon="edit"
                :label="`编辑行程：${item.title}`"
                text
                :disabled="!!busy || reordering"
                @click="dialog?.open(item)"
              />
              <IconAction
                icon="trash"
                :label="`删除行程：${item.title}`"
                text
                type="danger"
                :loading="busy === item.id"
                :disabled="(!!busy && busy !== item.id) || reordering"
                @click="remove(item)"
              />
            </div>
            </article>
            <button
              v-if="byOrigin.get(item.id) && !isCrossDayLeg(byOrigin.get(item.id)!)"
              type="button"
              class="route-connector"
              :aria-label="routeAriaLabel(byOrigin.get(item.id)!)"
              @click="openRouteLeg(byOrigin.get(item.id)!)"
            >
              <component :is="modeMeta[byOrigin.get(item.id)!.mode].icon" aria-hidden="true" />
              <span>{{ modeMeta[byOrigin.get(item.id)!.mode].label }}</span>
              <strong>{{ compactLegDescription(byOrigin.get(item.id)!) }}</strong>
              <ChevronRight aria-hidden="true" />
            </button>
          </div>
        </VueDraggable>
        <ElEmpty
          v-if="!lists[day.date]?.length"
          description="这一天还没有安排"
          :image-size="48"
          class="day-empty"
        />
      </section>
    </div>
    <ItineraryItemDialog ref="dialog" @saved="saved" />

    <ElDrawer
      v-model="routeDrawerOpen"
      :direction="isMobile ? 'btt' : 'rtl'"
      :size="isMobile ? '78%' : '430px'"
      class="route-drawer"
      title="选择交通方式"
    >
      <template v-if="selectedLeg">
        <div class="route-endpoints">
          <span>{{ itemById.get(selectedLeg.from_item_id)?.title ?? '起点' }}</span>
          <span>{{ itemById.get(selectedLeg.to_item_id)?.title ?? '终点' }}</span>
        </div>
        <div class="route-drawer-heading">
          <div>
            <h3>出行方式</h3>
            <p>直线距离 {{ formatDistance(selectedLeg.direct_distance_meters) }}</p>
          </div>
          <ElButton text :icon="Settings2" @click="openPreferences">偏好设置</ElButton>
        </div>
        <div class="route-mode-list" :aria-busy="routeBusy">
          <button
            v-for="mode in routeModes"
            :key="mode"
            type="button"
            class="route-mode-option"
            :class="{ 'is-selected': selectedMode === mode }"
            :disabled="routeBusy"
            @click="chooseRouteMode(mode)"
          >
            <component :is="modeMeta[mode].icon" aria-hidden="true" />
            <span>
              <strong>{{ modeMeta[mode].label }}</strong>
              <small v-if="mode === 'auto'">按交通偏好自动判断</small>
              <small v-else-if="selectedLeg.mode === mode && selectedLeg.status === 'ready'">
                {{ legDescription(selectedLeg) }}
              </small>
              <small v-else>选择后重新计算</small>
            </span>
          </button>
        </div>
      </template>
    </ElDrawer>

    <ElDialog
      v-model="preferenceOpen"
      title="交通偏好"
      :width="isMobile ? 'calc(100% - 24px)' : '500px'"
      class="route-preference-dialog"
    >
      <div class="preference-form">
        <div class="preference-row">
          <div>
            <strong>短途区间</strong>
            <span>0 至</span>
          </div>
          <ElInputNumber v-model="shortDistanceKm" :min="0" :max="50" :step="0.5" :precision="1" />
          <span>公里</span>
        </div>
        <div class="preference-mode">
          <span>短途自动方式</span>
          <ElRadioGroup v-model="shortMode">
            <ElRadioButton value="walking">步行</ElRadioButton>
            <ElRadioButton value="cycling">骑行</ElRadioButton>
          </ElRadioGroup>
        </div>
        <div class="preference-driving">
          <CarFront aria-hidden="true" />
          <span>超过 {{ shortDistanceKm.toFixed(1) }} 公里自动使用驾车</span>
        </div>
      </div>
      <template #footer>
        <ElButton @click="preferenceOpen = false">取消</ElButton>
        <ElButton type="primary" :loading="routeBusy" @click="savePreferences">保存</ElButton>
      </template>
    </ElDialog>
  </div>
</template>

<style scoped>
.route-options {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface);
  font-size: 13px;
  color: var(--tf-text-2);
}
.route-options > div {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.route-options svg {
  width: 18px;
  flex: 0 0 auto;
  color: var(--tf-accent);
}
.route-connector {
  display: grid;
  grid-template-columns: 18px auto minmax(80px, 1fr) 16px;
  align-items: center;
  column-gap: 0;
  width: 100%;
  min-height: 40px;
  margin: 0;
  padding: 6px 8px 6px 0;
  border: 0;
  background: transparent;
  color: var(--tf-text-2);
  font-size: 12px;
  text-align: left;
  cursor: pointer;
}
.route-connector:hover strong,
.route-connector:focus-visible strong {
  color: var(--tf-accent);
}
.route-connector:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 4px;
}
.route-connector > svg {
  width: 18px;
  height: 18px;
  color: var(--tf-accent);
}
.route-connector > svg:last-child {
  width: 15px;
  margin-left: 4px;
  color: var(--tf-text-3);
}
.route-connector strong {
  margin-left: 12px;
  color: var(--tf-text-1);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  white-space: nowrap;
}
.route-connector--cross-day {
  margin-bottom: 2px;
  border-top: 1px dashed var(--tf-line);
  border-bottom: 1px dashed var(--tf-line-soft);
  background: color-mix(in srgb, var(--tf-accent-soft) 55%, transparent);
}
.itinerary-tab {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.tab-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}
.tab-summary {
  font-size: 12px;
  color: var(--tf-text-3);
}
.tab-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.tab-actions .el-button {
  margin-left: 0;
}
.tab-skeleton {
  padding: 24px;
  background: var(--tf-surface);
  border-radius: var(--tf-radius-control);
}
.retry-button {
  margin-top: 16px;
}
.day-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.day-list--busy {
  pointer-events: none;
}
.day {
  padding: 16px 18px;
  scroll-margin-top: 80px;
}
.day--today {
  border-color: var(--tf-success);
}
.day--outside {
  border-style: dashed;
}
.day-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}
.day-header h2 {
  margin: 0;
  font-size: 17px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.day-count {
  font-size: 12px;
  color: var(--tf-text-3);
  margin-right: auto;
}
.day-items {
  display: flex;
  flex-direction: column;
  gap: 0;
  min-height: 12px;
}
.item-group {
  display: flex;
  flex-direction: column;
}
.item-group.item--ghost {
  background: var(--tf-accent-soft);
  border-radius: var(--tf-radius-control);
  outline: 1px dashed var(--tf-accent);
  outline-offset: -1px;
}
.item {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  padding: 9px 12px;
  border: 1px solid color-mix(in srgb, var(--item-kind, var(--tf-line)) 24%, var(--tf-line-soft));
  border-radius: var(--tf-radius-control);
  background: color-mix(in srgb, var(--item-kind, var(--tf-surface-inset)) 12%, var(--tf-surface));
  box-shadow: inset 4px 0 0 var(--item-kind, transparent);
  transition: transform var(--tf-duration-fast) var(--tf-ease), box-shadow var(--tf-duration-fast) var(--tf-ease);
}
.item--kind-transport { --item-kind: var(--tf-chart-4); }
.item--kind-attraction { --item-kind: var(--tf-chart-6); }
.item--kind-lodging { --item-kind: var(--tf-chart-2); }
.item--kind-dining { --item-kind: var(--tf-chart-5); }
.item--kind-other { --item-kind: var(--tf-info); }
.item:hover { transform: translateY(-1px); box-shadow: inset 4px 0 0 var(--item-kind, transparent), var(--tf-shadow-1); }
.item--completed .item-title strong,
.item--skipped .item-title strong {
  color: var(--tf-text-2);
}
.item--skipped .item-title strong {
  text-decoration: line-through;
}
.item--ghost {
  background: var(--tf-accent-soft);
  border-style: dashed;
  border-color: var(--tf-accent);
}
.drag-handle {
  cursor: grab;
  border: 0;
  background: transparent;
  color: var(--tf-text-3);
  font-size: 14px;
  line-height: 1;
  padding: 6px 2px;
  letter-spacing: -2px;
}
.drag-handle:disabled {
  cursor: default;
}
.item-body {
  flex: 1;
  min-width: 0;
}
.item-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.item-title strong {
  overflow-wrap: anywhere;
}
.item-time,
.item-place,
.item-amount,
.item-notes {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--tf-text-2);
  overflow-wrap: anywhere;
}
.item-notes {
  color: var(--tf-text-3);
  white-space: pre-wrap;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.item-actions {
  display: flex;
  flex-shrink: 0;
}
.item-actions .el-button {
  margin-left: 0;
}
.day-empty {
  padding: 4px 0 0;
}
.day-empty :deep(.el-empty__description) {
  margin-top: 4px;
}
:global(.route-drawer .el-drawer__header) {
  margin-bottom: 0;
  padding: 20px 22px 16px;
  color: var(--tf-text-1);
  font-weight: 800;
}
:global(.route-drawer .el-drawer__body) {
  padding: 0 22px 24px;
}
.route-endpoints {
  position: relative;
  display: grid;
  gap: 20px;
  padding: 20px 20px 20px 48px;
  border-radius: var(--tf-radius-control);
  background: var(--tf-accent-soft);
  color: var(--tf-text-1);
  font-weight: 700;
}
.route-endpoints::before {
  position: absolute;
  top: 27px;
  bottom: 27px;
  left: 26px;
  border-left: 1px dashed var(--tf-accent);
  content: '';
}
.route-endpoints span::before {
  position: absolute;
  left: 22px;
  width: 8px;
  height: 8px;
  margin-top: 5px;
  border: 2px solid var(--tf-accent);
  border-radius: 50%;
  background: var(--tf-surface);
  content: '';
}
.route-drawer-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin: 26px 0 12px;
}
.route-drawer-heading h3,
.route-drawer-heading p {
  margin: 0;
}
.route-drawer-heading h3 {
  font-size: 17px;
}
.route-drawer-heading p {
  margin-top: 4px;
  color: var(--tf-text-3);
  font-size: 12px;
}
.route-mode-list {
  display: grid;
  gap: 8px;
}
.route-mode-option {
  display: grid;
  grid-template-columns: 24px minmax(0, 1fr);
  align-items: center;
  gap: 12px;
  min-height: 66px;
  padding: 12px 14px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-inset);
  color: var(--tf-text-2);
  text-align: left;
  cursor: pointer;
}
.route-mode-option:hover,
.route-mode-option.is-selected {
  border-color: var(--tf-accent);
  background: var(--tf-accent-soft);
}
.route-mode-option.is-selected {
  color: var(--tf-accent);
}
.route-mode-option svg {
  width: 22px;
}
.route-mode-option span {
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.route-mode-option strong {
  color: var(--tf-text-1);
  font-size: 14px;
}
.route-mode-option small {
  color: var(--tf-text-3);
}
.preference-form {
  display: grid;
  gap: 18px;
}
.preference-row,
.preference-mode,
.preference-driving {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px;
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-inset);
}
.preference-row > div:first-child {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 3px;
}
.preference-row span,
.preference-mode > span,
.preference-driving {
  color: var(--tf-text-2);
  font-size: 13px;
}
.preference-mode {
  justify-content: space-between;
}
.preference-driving svg {
  width: 20px;
  color: var(--tf-accent);
}
@media (max-width: 600px) {
  .item {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
  }
  .item-actions {
    grid-column: 2;
    justify-self: end;
  }
  .route-options {
    align-items: flex-start;
  }
  .route-connector {
    min-height: 44px;
  }
  :global(.route-drawer.el-drawer) {
    border-radius: 16px 16px 0 0;
  }
  .preference-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
  }
  .preference-mode {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
