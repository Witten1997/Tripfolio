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
  ElSkeleton,
  ElTag,
} from 'element-plus'
import { Bike, CarFront, ChevronRight, Footprints, RotateCcw, Route, Settings2 } from '@lucide/vue'
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { VueDraggable, type DraggableEvent } from 'vue-draggable-plus'

import IconAction from '@/desktop/components/IconAction.vue'
import ItineraryItemDialog from '@/desktop/components/ItineraryItemDialog.vue'
import SlidingSegmented from '@/desktop/components/SlidingSegmented.vue'
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
import { itineraryKindIcons } from '@/shared/travel/itineraryKindVisuals'
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
const shortModeOptions = [
  { value: 'walking', label: '步行' },
  { value: 'cycling', label: '骑行' },
]
const shortDistanceKm = ref(1.5)
const isMobile = ref(false)
const maxRoutePollAttempts = 20
let routePoll: ReturnType<typeof setTimeout> | undefined
let mediaQuery: MediaQueryList | undefined
let mediaQueryListener: (() => void) | undefined
let routePollAttempts = 0
let recalculationRequested = false
let scrollFrame: number | undefined
let scrollListener: (() => void) | undefined
const dragging = ref(false)

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
const itemById = computed(() => new Map(board.items.value.map((item) => [item.id, item])))
const selectedLeg = computed(
  () => routePlan.value?.legs.find((leg) => leg.id === selectedLegId.value) ?? null,
)
const selectedMode = computed<RouteLegMode>(() =>
  selectedLeg.value?.mode_source === 'preference' ? 'auto' : (selectedLeg.value?.mode ?? 'auto'),
)
const activeDay = ref('')
const isToday = (date: string) => date === context.today.value

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
    if (!next.some((day) => day.date === activeDay.value)) {
      activeDay.value = next.find((day) => isToday(day.date))?.date ?? next[0]?.date ?? ''
    }
  },
  { immediate: true, flush: 'sync' },
)
function syncActiveDay() {
  if (!days.value.length) return
  const navBottom = document
    .querySelector<HTMLElement>('.day-jump-nav')
    ?.getBoundingClientRect().bottom
  const sections = Array.from(document.querySelectorAll<HTMLElement>('.day-list > .day'))
  const scrollMarginTop = sections[0]
    ? Number.parseFloat(window.getComputedStyle(sections[0]).scrollMarginTop)
    : 0
  // 选择锚点至少落在日期区块的滚动留白之后，避免 sticky 日期栏遮住旧日期内容时仍维持旧高亮。
  const anchor = Math.max(
    (navBottom ?? 0) + 16,
    Number.isFinite(scrollMarginTop) ? scrollMarginTop : 0,
  )
  const contentTop = (navBottom ?? 0) + 4
  const visibleContent = Array.from(
    document.querySelectorAll<HTMLElement>('.day-list .item, .day-list .route-connector'),
  ).find((element) => {
    const rect = element.getBoundingClientRect()
    return rect.bottom > contentTop && rect.top < window.innerHeight
  })
  const contentDay = visibleContent?.closest('.day') as HTMLElement | null
  const current =
    contentDay ??
    sections.reduce<HTMLElement | null>((selected, section) => {
      const top = section.getBoundingClientRect().top
      if (top > anchor) return selected
      return !selected || top > selected.getBoundingClientRect().top ? section : selected
    }, null)
  const section = current ?? sections[0]
  const date = section?.id.replace(/^day-/, '')
  if (!date || date === activeDay.value) return
  activeDay.value = date
  document.querySelector<HTMLElement>(`[data-day-jump="${date}"]`)?.scrollIntoView({
    behavior: 'smooth',
    block: 'nearest',
    inline: 'nearest',
  })
}

function handleScroll() {
  if (scrollFrame !== undefined) return
  scrollFrame = window.requestAnimationFrame(() => {
    scrollFrame = undefined
    syncActiveDay()
  })
}

watch(
  days,
  async () => {
    await nextTick()
    syncActiveDay()
  },
  { flush: 'post' },
)
const total = computed(() => board.items.value.length)

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

function jumpToDay(date: string) {
  activeDay.value = date
  document.querySelector<HTMLElement>(`[data-day-jump="${date}"]`)?.scrollIntoView({
    behavior: 'smooth',
    block: 'nearest',
    inline: 'center',
  })
  const el = document.getElementById(`day-${date}`)
  el?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function jumpToToday() {
  jumpToDay(context.today.value)
}

function dayTitlePart(title: string, part: 'date' | 'weekday') {
  const [date, weekday] = title.split(' ')
  return part === 'date' ? date : weekday
}

async function onDragEnd(event: DraggableEvent<ItineraryItem>) {
  dragging.value = false
  const from = event.from.dataset.date
  const to = event.to.dataset.date
  const id = event.data?.id
  if (!from || !to || !id || event.newIndex === undefined) {
    await reloadAll()
    return
  }
  // moveItem 成功或失败都会重载 board，镜像由 days 的 watcher 重建
  const moved = await board.moveItem(id, to, event.newIndex)
  if (moved) await loadRoutePlan()
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
    return start ? `${start}-${end}` : `至 ${end}`
  }
  if (item.planned_duration_minutes) {
    if (!item.planned_start_local || !start) return `${item.planned_duration_minutes} 分钟`
    const [year, month, day, hour, minute] =
      item.planned_start_local
        .match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/)
        ?.slice(1)
        .map(Number) ?? []
    if ([year, month, day, hour, minute].some((part) => !Number.isInteger(part))) {
      return `${item.planned_duration_minutes} 分钟`
    }
    const endDate = new Date(
      Date.UTC(year!, month! - 1, day!, hour!, minute! + item.planned_duration_minutes),
    )
    const pad = (value: number) => String(value).padStart(2, '0')
    const endDay = `${endDate.getUTCFullYear()}-${pad(endDate.getUTCMonth() + 1)}-${pad(endDate.getUTCDate())}`
    const endTime = `${pad(endDate.getUTCHours())}:${pad(endDate.getUTCMinutes())}`
    const end = endDay === item.scheduled_on ? endTime : `${endDay.slice(5)} ${endTime}`
    return `${start}-${end}`
  }
  return start ?? ''
}

onMounted(async () => {
  if (typeof window !== 'undefined') {
    scrollListener = handleScroll
    window.addEventListener('scroll', scrollListener, { passive: true })
  }
  if (typeof window !== 'undefined' && window.matchMedia) {
    mediaQuery = window.matchMedia('(max-width: 767px)')
    mediaQueryListener = () => (isMobile.value = mediaQuery?.matches ?? false)
    mediaQueryListener()
    mediaQuery.addEventListener('change', mediaQueryListener)
  }
  await reloadAll()
  await nextTick()
  syncActiveDay()
})

onUnmounted(() => {
  if (routePoll) clearTimeout(routePoll)
  if (mediaQueryListener) mediaQuery?.removeEventListener('change', mediaQueryListener)
  if (scrollListener) window.removeEventListener('scroll', scrollListener)
  if (scrollFrame !== undefined) window.cancelAnimationFrame(scrollFrame)
})
</script>

<template>
  <div class="itinerary-tab">
    <nav v-if="days.length" class="day-jump-nav tf-surface" aria-label="快速跳转到日期">
      <div class="day-jump-track">
        <button
          v-for="day in days"
          :key="day.date"
          type="button"
          class="day-jump"
          :data-day-jump="day.date"
          :class="{
            'is-active': activeDay === day.date,
            'is-today': isToday(day.date),
            'is-outside': day.outside,
          }"
          :aria-current="activeDay === day.date ? 'date' : undefined"
          @click="jumpToDay(day.date)"
        >
          {{ day.title }}
        </button>
      </div>
    </nav>
    <div class="tab-toolbar">
      <span class="tab-summary">
        共 {{ total }} 条行程 · 拖动卡片调整顺序或移到其他日期
        <template v-if="reordering">，正在保存排序…</template>
      </span>
      <div class="tab-actions tf-actions">
        <ElButton v-if="days.some((d) => isToday(d.date))" size="small" @click="jumpToToday"
          >今日行程</ElButton
        >
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
    <div
      v-else
      class="day-list"
      :class="{ 'day-list--busy': reordering, 'day-list--dragging': dragging }"
    >
      <section
        v-for="day in days"
        :id="`day-${day.date}`"
        :key="day.date"
        class="day"
        :class="{
          'day--active': activeDay === day.date,
          'day--today': isToday(day.date),
          'day--outside': day.outside,
        }"
      >
        <div class="day-rail">
          <h2 class="day-badge">
            <span>{{ dayTitlePart(day.title, 'date') }}</span>
            <small>{{ dayTitlePart(day.title, 'weekday') }}</small>
          </h2>
          <span class="day-timeline" aria-hidden="true"></span>
        </div>
        <div class="day-panel">
          <span class="day-node" aria-hidden="true"></span>
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
            @start="dragging = true"
            @end="onDragEnd"
          >
            <div v-for="item in lists[day.date] ?? []" :key="item.id" class="item-group">
              <article class="item" :class="[`item--${item.status}`, `item--kind-${item.kind}`]">
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
                    <strong>{{ item.title }}</strong>
                    <ElTag
                      size="small"
                      effect="plain"
                      class="item-kind-tag"
                      :class="`tf-itinerary-kind--${item.kind}`"
                    >
                      <component :is="itineraryKindIcons[item.kind]" aria-hidden="true" />
                      {{ itineraryKindLabels[item.kind] }}
                    </ElTag>
                    <ElTag
                      v-if="item.status !== 'pending'"
                      size="small"
                      :type="item.status === 'completed' ? 'success' : 'info'"
                      >{{ itineraryStatusLabels[item.status] }}</ElTag
                    >
                  </div>
                  <p v-if="item.place_name || item.address" class="item-place">
                    {{ item.place_name }}<span v-if="item.place_name && item.address"> · </span
                    >{{ item.address }}
                  </p>
                  <p v-if="item.notes" class="item-notes">{{ item.notes }}</p>
                  <div v-if="timeLabel(item) || item.estimated_amount" class="item-meta">
                    <span v-if="timeLabel(item)" class="item-time">{{ timeLabel(item) }}</span>
                    <span v-if="item.estimated_amount" class="item-amount">
                      预计 {{ item.currency_code }} {{ item.estimated_amount }}
                    </span>
                  </div>
                </div>
                <div class="item-actions tf-actions">
                  <IconAction
                    icon="edit"
                    :label="`编辑行程：${item.title}`"
                    appearance="ghost"
                    text
                    :disabled="!!busy || reordering"
                    @click="dialog?.open(item)"
                  />
                  <IconAction
                    icon="trash"
                    :label="`删除行程：${item.title}`"
                    appearance="ghost"
                    text
                    type="danger"
                    :loading="busy === item.id"
                    :disabled="(!!busy && busy !== item.id) || reordering"
                    @click="remove(item)"
                  />
                </div>
              </article>
              <button
                v-if="byOrigin.get(item.id)"
                type="button"
                class="route-connector"
                :class="{
                  'route-connector--cross-day': isCrossDayLeg(byOrigin.get(item.id)!),
                }"
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
          <footer class="day-add-row">
            <div class="day-heading">
              <ElTag v-if="isToday(day.date)" type="success" size="small" effect="plain"
                >今天</ElTag
              >
              <ElTag v-if="day.outside" type="warning" size="small" effect="plain"
                >旅行日期外</ElTag
              >
            </div>
            <IconAction
              icon="plus"
              :label="`为 ${day.title} 添加行程`"
              appearance="ghost"
              text
              :disabled="reordering"
              @click="dialog?.open(undefined, day.date)"
            />
          </footer>
        </div>
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
          <SlidingSegmented v-model="shortMode" :options="shortModeOptions" label="短途自动方式" />
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
.day-jump-nav {
  position: sticky;
  top: 24px;
  z-index: 5;
  min-width: 0;
  padding: 8px;
  overflow: hidden;
  border-radius: var(--tf-radius-control);
}
.day-jump-track {
  display: flex;
  gap: 6px;
  overflow-x: auto;
  overscroll-behavior-x: contain;
  scrollbar-width: none;
  scroll-snap-type: x proximity;
}
.day-jump-track::-webkit-scrollbar {
  display: none;
}
.day-jump {
  flex: 1 0 112px;
  min-height: 42px;
  padding: 8px 12px;
  border: 1px solid transparent;
  border-radius: calc(var(--tf-radius-control) - 2px);
  background: transparent;
  color: var(--tf-text-2);
  font: inherit;
  font-size: 13px;
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  letter-spacing: 0;
  scroll-snap-align: start;
  white-space: nowrap;
  cursor: pointer;
  transition:
    background-color var(--tf-duration-fast) var(--tf-ease),
    border-color var(--tf-duration-fast) var(--tf-ease),
    color var(--tf-duration-fast) var(--tf-ease),
    transform var(--tf-duration-fast) var(--tf-ease);
}
.day-jump:hover {
  border-color: var(--tf-line-soft);
  background: var(--tf-surface-inset);
  color: var(--tf-text-1);
}
.day-jump:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: -2px;
}
.day-jump:active {
  transform: scale(0.98);
}
.day-jump.is-active {
  border-color: var(--tf-accent);
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
  box-shadow: var(--tf-shadow-1);
}
.day-jump.is-today:not(.is-active) {
  border-color: var(--tf-success);
  color: var(--tf-success);
}
.day-jump.is-outside {
  border-style: dashed;
}
:global([data-theme='glass']) .day-jump-nav {
  border-radius: 22px 16px 20px 14px / 16px 21px 14px 19px;
  background-color: color-mix(in srgb, var(--tf-surface-raised) 64%, transparent);
  box-shadow:
    0 12px 28px -20px color-mix(in srgb, var(--tf-accent) 48%, transparent),
    inset 0 1px 0 color-mix(in srgb, var(--tf-surface-raised) 86%, transparent),
    inset 0 -1px 0 color-mix(in srgb, var(--tf-accent) 8%, transparent);
}
:global([data-theme='glass']) .day-jump {
  border-radius: 17px 13px 18px 12px / 13px 18px 12px 17px;
}
:global([data-theme='glass']) .day-jump:nth-child(even) {
  border-radius: 13px 18px 12px 17px / 17px 13px 18px 12px;
}
:global([data-theme='glass']) .day-jump.is-active {
  border-color: color-mix(in srgb, var(--tf-surface-raised) 68%, transparent);
  background-color: var(--tf-accent);
  background-image: linear-gradient(
    135deg,
    color-mix(in srgb, var(--tf-surface-raised) 20%, transparent),
    transparent 46%,
    color-mix(in srgb, var(--tf-accent) 8%, transparent)
  );
  box-shadow:
    0 10px 20px -12px color-mix(in srgb, var(--tf-accent) 70%, transparent),
    inset 0 1px 0 color-mix(in srgb, var(--tf-surface-raised) 46%, transparent),
    inset 0 -1px 0 color-mix(in srgb, var(--tf-accent) 20%, transparent);
}
@media (min-width: 601px) {
  .day-jump-nav {
    box-sizing: border-box;
    height: 46px;
    padding: 4px 8px;
  }
  .day-jump-track {
    height: 100%;
  }
  .day-jump {
    min-height: 0;
    padding-block: 4px;
  }
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
.day-list--dragging .route-connector {
  visibility: hidden;
}
.day {
  display: grid;
  grid-template-columns: 64px minmax(0, 1fr);
  gap: 16px;
  min-width: 0;
  scroll-margin-top: 104px;
}
.day-rail {
  position: relative;
  display: flex;
  justify-content: center;
}
.day-badge {
  position: relative;
  z-index: 1;
  box-sizing: border-box;
  display: flex;
  width: 58px;
  height: 58px;
  margin: 0;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--tf-line-soft);
  border-radius: 17px;
  background: var(--tf-surface-sunken);
  color: var(--tf-text-1);
  box-shadow: var(--tf-shadow-1), var(--tf-surface-highlight);
  font-variant-numeric: tabular-nums;
  line-height: 1.05;
}
.day-badge span {
  font-size: 15px;
  font-weight: 750;
}
.day-badge small {
  margin-top: 5px;
  color: var(--tf-text-3);
  font-size: 11px;
  font-weight: 600;
}
.day--active .day-badge {
  border-color: var(--tf-accent);
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
}
.day--active .day-badge small {
  color: var(--tf-accent-contrast);
}
:global([data-theme='glass']) .day-badge {
  border-radius: 18px 13px 19px 14px / 15px 19px 13px 18px;
}
:global([data-theme='glass']) .day--active .day-badge {
  border-color: color-mix(in srgb, var(--tf-surface-raised) 66%, transparent);
  background-color: var(--tf-accent);
  background-image: linear-gradient(
    140deg,
    color-mix(in srgb, var(--tf-surface-raised) 20%, transparent),
    transparent 48%,
    color-mix(in srgb, var(--tf-accent) 9%, transparent)
  );
  box-shadow:
    0 12px 22px -13px color-mix(in srgb, var(--tf-accent) 74%, transparent),
    inset 0 1px 0 color-mix(in srgb, var(--tf-surface-raised) 48%, transparent),
    inset 0 -1px 0 color-mix(in srgb, var(--tf-accent) 20%, transparent);
}
.day-timeline {
  position: absolute;
  top: 58px;
  bottom: -16px;
  left: 50%;
  border-left: 1px solid color-mix(in srgb, var(--tf-accent) 42%, var(--tf-line-soft));
}
.day:last-child .day-timeline {
  display: none;
}
.day-panel {
  position: relative;
  min-width: 0;
  padding: 0 0 6px;
}
.day-node {
  position: absolute;
  top: 27px;
  left: -14px;
  width: 9px;
  height: 9px;
  border: 2px solid var(--tf-surface-raised);
  border-radius: 50%;
  background: var(--tf-accent);
  box-shadow: 0 0 0 1px var(--tf-accent);
}
.day-node::before {
  position: absolute;
  top: 3px;
  right: 7px;
  width: 16px;
  border-top: 1px solid color-mix(in srgb, var(--tf-accent) 42%, var(--tf-line-soft));
  content: '';
}
.day-add-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: var(--tf-control-size);
  margin-top: 8px;
  padding-left: 4px;
}
.day-heading {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex-wrap: wrap;
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
  display: grid;
  grid-template-columns: 22px minmax(0, 1fr) auto;
  gap: 8px;
  align-items: flex-start;
  padding: 12px 12px 13px;
  border: 1px solid color-mix(in srgb, var(--item-kind, var(--tf-line)) 24%, var(--tf-line-soft));
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-raised);
  box-shadow:
    inset 3px 0 0 var(--item-kind, transparent),
    var(--tf-shadow-1),
    var(--tf-surface-highlight);
  transition:
    border-color var(--tf-duration-fast) var(--tf-ease),
    transform var(--tf-duration-fast) var(--tf-ease);
}
.item--kind-transport {
  --item-kind: var(--tf-itinerary-transport);
}
.item--kind-attraction {
  --item-kind: var(--tf-itinerary-attraction);
}
.item--kind-lodging {
  --item-kind: var(--tf-itinerary-lodging);
}
.item--kind-dining {
  --item-kind: var(--tf-itinerary-dining);
}
.item--kind-other {
  --item-kind: var(--tf-itinerary-other);
}
.item-kind-tag :deep(.el-tag__content) {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.item-kind-tag :deep(svg) {
  width: 14px;
  height: 14px;
  color: var(--tf-itinerary-kind-color);
}
.item:hover {
  border-color: color-mix(in srgb, var(--item-kind) 48%, var(--tf-line-soft));
  transform: translateY(-1px);
}
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
  letter-spacing: 0;
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
  color: var(--tf-text-1);
  font-family: var(--tf-font-display);
  font-size: 15px;
  font-weight: 700;
  line-height: 1.45;
  overflow-wrap: anywhere;
}
.item-place,
.item-notes {
  margin: 4px 0 0;
  font-size: 13px;
  line-height: 1.55;
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
.item-meta {
  display: flex;
  align-items: center;
  gap: 6px 16px;
  margin-top: 8px;
  flex-wrap: wrap;
  color: var(--tf-text-2);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.item-time,
.item-amount {
  overflow-wrap: anywhere;
}
.item-actions {
  display: flex;
  flex-shrink: 0;
  gap: 0;
  flex-wrap: nowrap;
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
  .day-jump-nav {
    box-sizing: border-box;
    top: 8px;
    margin-inline: -8px;
    height: 46px;
    padding: 4px 6px;
  }
  .day-jump-track {
    height: 100%;
  }
  .day-jump {
    flex-basis: 104px;
    min-height: 0;
    padding-block: 4px;
    padding-inline: 10px;
    font-size: 12px;
  }
  .day-list {
    gap: 14px;
  }
  .day {
    grid-template-columns: 46px minmax(0, 1fr);
    gap: 8px;
    scroll-margin-top: 84px;
  }
  .day-badge {
    width: 44px;
    height: 50px;
    border-radius: 14px;
  }
  .day-badge span {
    font-size: 13px;
  }
  .day-badge small {
    margin-top: 4px;
    font-size: 10px;
  }
  .day-timeline {
    top: 50px;
    bottom: -14px;
  }
  .day-panel {
    padding: 0 0 4px;
  }
  .day-node {
    top: 22px;
    left: -6px;
    width: 7px;
    height: 7px;
  }
  .day-node::before {
    top: 2px;
    right: 6px;
    width: 7px;
  }
  .day-add-row {
    margin-top: 6px;
    padding-left: 2px;
  }
  .item {
    grid-template-columns: auto minmax(0, 1fr);
    padding: 10px 10px 11px;
  }
  .item-actions {
    grid-column: 2;
    justify-self: end;
    margin-top: -2px;
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
