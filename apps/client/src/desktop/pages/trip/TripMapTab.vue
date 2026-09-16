<script setup lang="ts">
import { ElAlert, ElButton, ElEmpty, ElOption, ElSelect, ElSkeleton } from 'element-plus'
import { computed, onMounted, onScopeDispose, ref, shallowRef } from 'vue'

import AmapView from '@/desktop/components/AmapView.vue'
import IconAction from '@/desktop/components/IconAction.vue'
import ItineraryItemDialog from '@/desktop/components/ItineraryItemDialog.vue'
import { travelModeLabels, type TravelMode } from '@/shared/api/geo'
import { listAllItineraryItems, type ItineraryItem } from '@/shared/api/itinerary'
import { getRoutePlan, type RoutePlan } from '@/shared/api/routePlan'
import { actionError } from '@/shared/api/writes'
import {
  directDistanceMeters,
  formatDistance,
  formatDuration,
  sortedItinerary,
  type RouteLeg,
  type Waypoint,
} from '@/shared/geo/itineraryRoute'
import { useItineraryRoutes } from '@/shared/geo/useItineraryRoutes'
import { useTripContext } from '@/shared/travel/tripContext'
import { dayTitle } from '@/shared/travel/tripDays'

const context = useTripContext()
const items = shallowRef<ItineraryItem[]>([])
const routePlan = shallowRef<RoutePlan | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const routePlanError = ref<string | null>(null)
const date = ref('all')
const map = ref<InstanceType<typeof AmapView>>()
const dialog = ref<InstanceType<typeof ItineraryItemDialog>>()
const focused = ref('')
const dates = computed(() => [
  ...new Set(sortedItinerary(items.value).map((item) => item.scheduled_on)),
])
const filtered = computed(() =>
  date.value !== 'all'
    ? items.value.filter((item) => item.scheduled_on === date.value)
    : items.value,
)
const plannedLegs = computed(
  () =>
    new Map(
      (routePlan.value?.legs ?? []).map((leg) => [`${leg.from_item_id}>${leg.to_item_id}`, leg]),
    ),
)
function modeForLeg(leg: RouteLeg): TravelMode {
  const planned = plannedLegs.value.get(leg.id)
  if (planned?.mode_source === 'manual') return planned.mode
  const preference = routePlan.value?.preference
  const distance = planned?.direct_distance_meters ?? directDistanceMeters(leg.from, leg.to)
  if (preference && distance <= preference.short_distance_meters) return preference.short_mode
  return 'driving'
}
const routes = useItineraryRoutes(() => filtered.value, modeForLeg)
const {
  points,
  legs,
  paths,
  byOrigin,
  missingCount,
  loading: calculating,
  failedCount,
  readyCount,
  totals,
} = routes
const unlocated = computed(() =>
  filtered.value.filter((item) => !points.value.some((point) => point.id === item.id)),
)
let generation = 0

async function reload() {
  const request = ++generation
  loading.value = true
  error.value = null
  routePlanError.value = null
  try {
    const [itemResult, planResult] = await Promise.allSettled([
      listAllItineraryItems(context.tripId),
      getRoutePlan(context.tripId),
    ])
    if (request !== generation) return
    if (itemResult.status === 'fulfilled') items.value = itemResult.value
    else error.value = actionError(itemResult.reason, '无法加载行程地点，请检查网络后重试')
    if (planResult.status === 'fulfilled') routePlan.value = planResult.value
    else {
      routePlan.value = null
      routePlanError.value = actionError(planResult.reason, '交通方式暂时无法加载，当前按驾车绘制')
    }
  } finally {
    if (request === generation) loading.value = false
  }
}

function focusPoint(point: Waypoint) {
  focused.value = point.id
  map.value?.focus(point)
}
function markerSelected(id: string) {
  const point = points.value.find((p) => p.id === id)
  if (point) focusPoint(point)
}

onMounted(() => {
  void reload()
})
onScopeDispose(() => {
  generation++
})
</script>

<template>
  <section class="trip-map-tab" aria-label="行程地图与路线统计">
    <header class="map-heading">
      <div>
        <h2>行程地图</h2>
        <p>把每一站连起来，看看这趟旅行怎么走。</p>
      </div>
      <div class="map-filters">
        <ElSelect v-model="date" aria-label="地图日期范围" class="day-filter">
          <ElOption label="整趟旅行" value="all" />
          <ElOption v-for="day in dates" :key="day" :label="dayTitle(day)" :value="day" />
        </ElSelect>
      </div>
    </header>
    <ElSkeleton v-if="loading && !items.length" :rows="8" animated />
    <div v-else-if="error" class="map-load-error">
      <ElAlert :title="error" type="error" :closable="false" show-icon />
      <ElButton @click="reload">重新加载</ElButton>
    </div>
    <template v-else>
      <ElAlert
        v-if="routePlanError"
        type="warning"
        :closable="false"
        show-icon
        :title="routePlanError"
      />
      <div class="route-summary" :aria-busy="calculating">
        <div>
          <span>已定位地点</span
          ><strong
            >{{ points.length }}<small> / {{ filtered.length }} 站</small></strong
          >
        </div>
        <div>
          <span>{{
            failedCount || missingCount || calculating ? '已算路段里程' : '道路总里程'
          }}</span
          ><strong>{{ readyCount ? formatDistance(totals.distance) : '—' }}</strong>
        </div>
        <div>
          <span>道路预计用时</span
          ><strong>{{ readyCount ? formatDuration(totals.duration) : '—' }}</strong>
        </div>
        <p role="status">
          {{
            calculating
              ? `正在计算 ${readyCount} / ${legs.length} 段…`
              : `${readyCount} / ${legs.length} 段已计算`
          }}<br />不含停留时间，按当前道路情况估算
        </p>
      </div>
      <ElAlert
        v-if="failedCount"
        type="warning"
        :closable="false"
        show-icon
        :title="`${failedCount} 段暂未算出路线，总里程与用时仅包含成功路段。可切换出行方式或稍后重试。`"
      >
        <ElButton :loading="calculating" @click="routes.refresh">重新计算未完成路段</ElButton>
      </ElAlert>
      <ElEmpty
        v-if="!points.length"
        :description="
          filtered.length
            ? '这些行程还没有地图位置，编辑行程并选择高德地点即可开始'
            : '还没有行程，先添加想去的地方吧'
        "
      >
        <RouterLink
          :to="{ name: 'trip-itinerary', params: { tripId: context.tripId } }"
          class="map-link"
          >前往每日行程</RouterLink
        >
      </ElEmpty>
      <div v-else class="route-layout">
        <div class="map-main">
          <AmapView
            ref="map"
            class="route-map"
            :points="points"
            :paths="paths"
            @focus-point="markerSelected"
          />
          <p class="map-legend">
            <span class="legend-driving" />驾车 <span class="legend-walking" />步行
            <span class="legend-cycling" />骑行
            <span class="legend-guide" />点位示意接续，不代表可通行道路
          </p>
        </div>
        <aside class="route-stops" aria-label="行程点位顺序">
          <div class="stops-heading">
            <h3>沿途各站</h3>
            <span>{{ points.length }} 个地点 · 按行程顺序</span>
          </div>
          <ol class="stop-list">
            <li
              v-for="point in points"
              :key="point.id"
              :class="[
                { 'stop--focused': focused === point.id },
                `stop--kind-${point.item.kind}`,
              ]"
            >
              <button
                type="button"
                class="stop-button"
                :aria-current="focused === point.id ? 'location' : undefined"
                @click="focusPoint(point)"
              >
                <span class="stop-number">{{ point.number }}</span>
                <span class="stop-info"
                  ><strong>{{ point.item.title }}</strong
                  ><span>{{ point.item.scheduled_on }} · {{ point.title }}</span
                  ><small v-if="point.item.address">{{ point.item.address }}</small></span
                >
              </button>
              <IconAction
                icon="edit"
                :label="`编辑地点：${point.item.title}`"
                text
                class="stop-edit"
                @click="dialog?.open(point.item)"
              />
              <div v-if="byOrigin.get(point.id)" class="stop-leg">
                <template v-if="byOrigin.get(point.id)?.route"
                  >{{ travelModeLabels[byOrigin.get(point.id)!.mode] }}
                  {{ formatDistance(byOrigin.get(point.id)!.route!.distance_meters) }} ·
                  {{ formatDuration(byOrigin.get(point.id)!.route!.duration_seconds) }}</template
                >
                <template v-else-if="byOrigin.get(point.id)?.status === 'loading'"
                  >正在计算下一段…</template
                >
                <template v-else>{{ byOrigin.get(point.id)?.message }}</template>
                <span v-if="byOrigin.get(point.id)?.crossDay" class="leg-note">跨日接续</span>
                <span v-if="byOrigin.get(point.id)?.missingBetween" class="leg-note"
                  >中间 {{ byOrigin.get(point.id)?.missingBetween }} 项尚未定位</span
                >
              </div>
            </li>
          </ol>
        </aside>
      </div>
      <section v-if="unlocated.length" class="unlocated" aria-label="待补充位置的行程">
        <h3>{{ unlocated.length }} 项待补充位置</h3>
        <p>这些行程暂未显示在地图上。补充地点后，路线会自动更新。</p>
        <div class="unlocated-items">
          <ElButton v-for="item in unlocated" :key="item.id" @click="dialog?.open(item)"
            >{{ item.scheduled_on }} · {{ item.title }}</ElButton
          >
        </div>
      </section>
    </template>
    <ItineraryItemDialog ref="dialog" @saved="reload" />
  </section>
</template>

<style scoped>
.trip-map-tab {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.map-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  flex-wrap: wrap;
}
.map-heading h2 {
  margin: 0 0 6px;
  font-size: 23px;
  color: var(--tf-text-1);
}
.map-heading p {
  margin: 0;
  color: var(--tf-text-2);
  font-size: 13px;
}
.map-filters {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.day-filter {
  width: 200px;
}
.route-summary {
  display: flex;
  gap: 32px;
  flex-wrap: wrap;
  align-items: center;
  padding: 18px 22px;
  background: var(--tf-surface);
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-card);
}
.route-summary > div {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.route-summary span,
.route-summary p {
  font-size: 12px;
  color: var(--tf-text-2);
}
.route-summary strong {
  font-size: 22px;
  color: var(--tf-text-1);
  font-variant-numeric: tabular-nums;
}
.route-summary small {
  font-size: 13px;
  font-weight: 400;
  color: var(--tf-text-2);
}
.route-summary p {
  margin: 0 0 0 auto;
  line-height: 1.8;
}
.route-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 310px;
  gap: 18px;
  align-items: start;
}
.map-main {
  min-width: 0;
}
.route-map {
  height: clamp(400px, 56vh, 620px);
}
.map-legend {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 12px;
  line-height: 1.7;
  color: var(--tf-text-2);
  margin: 10px 0 0;
}
.legend-driving,
.legend-walking,
.legend-cycling,
.legend-guide {
  width: 24px;
  border-top: 3px solid var(--tf-accent);
}
.legend-walking {
  border-top: 3px dashed var(--tf-chart-3);
  margin-left: 8px;
}
.legend-cycling {
  border-top-color: var(--tf-chart-2);
  margin-left: 8px;
}
.legend-guide {
  border-top: 2px dashed var(--tf-text-3);
  margin-left: 8px;
}
.route-stops {
  background: var(--tf-surface);
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  overflow: hidden;
}
.stops-heading {
  padding: 16px;
  border-bottom: 1px solid var(--tf-line-soft);
}
.stops-heading h3 {
  font-size: 16px;
  margin: 0 0 5px;
}
.stops-heading span {
  color: var(--tf-text-2);
  font-size: 12px;
}
.stop-list {
  list-style: none;
  padding: 0;
  margin: 0;
  max-height: calc(clamp(400px, 56vh, 620px) - 77px);
  overflow-y: auto;
  overscroll-behavior: contain;
}
.stop-list li {
  padding: 12px;
  border-bottom: 1px solid var(--tf-line-soft);
  border-left: 3px solid var(--stop-kind, transparent);
}
.stop--kind-transport { --stop-kind: var(--tf-chart-4); }
.stop--kind-attraction { --stop-kind: var(--tf-chart-6); }
.stop--kind-lodging { --stop-kind: var(--tf-chart-2); }
.stop--kind-dining { --stop-kind: var(--tf-chart-5); }
.stop--kind-other { --stop-kind: var(--tf-info); }
.stop-list li:last-child {
  border-bottom: 0;
}
.stop--focused {
  background: var(--tf-accent-soft);
}
.stop-button {
  width: 100%;
  display: flex;
  align-items: flex-start;
  gap: 10px;
  border: 0;
  background: transparent;
  color: var(--tf-text-1);
  text-align: left;
  padding: 4px;
  cursor: pointer;
  font: inherit;
  border-radius: var(--tf-radius-control);
}
.stop-button:focus-visible {
  outline-offset: 2px;
}
.stop-number {
  flex-shrink: 0;
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: 50%;
  background: var(--stop-kind, var(--tf-accent));
  color: var(--stop-number-contrast, var(--tf-accent-contrast));
  font-size: 12px;
  font-weight: 600;
}
.stop--kind-lodging { --stop-number-contrast: var(--tf-text-1); }
.stop-info {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
  overflow-wrap: anywhere;
}
.stop-info strong {
  font-size: 14px;
  line-height: 1.5;
}
.stop-info span,
.stop-info small {
  font-size: 12px;
  line-height: 1.6;
  color: var(--tf-text-2);
}
.stop-edit {
  margin: 4px 0 0 34px;
}
.stop-leg {
  margin: 8px 0 0 18px;
  padding: 5px 0 2px 22px;
  border-left: 2px dashed var(--tf-line);
  color: var(--tf-text-2);
  font-size: 12px;
  line-height: 1.7;
}
.leg-note {
  display: block;
}
.unlocated {
  padding: 16px 20px;
  border: 1px dashed var(--tf-line);
  border-radius: var(--tf-radius-control);
}
.unlocated h3 {
  margin: 0;
  font-size: 15px;
}
.unlocated p {
  font-size: 13px;
  color: var(--tf-text-2);
}
.unlocated-items {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.unlocated-items .el-button {
  margin: 0;
  white-space: normal;
  height: auto;
  min-height: 36px;
}
.map-load-error {
  display: grid;
  gap: 12px;
  justify-items: start;
}
.map-link {
  display: inline-block;
  padding: 10px 16px;
  color: var(--tf-accent-contrast);
  background: var(--tf-accent);
  border-radius: var(--tf-radius-control);
  text-decoration: none;
}
@media (max-width: 850px) {
  .route-layout {
    grid-template-columns: minmax(0, 1fr);
  }
  .stop-list {
    max-height: none;
  }
  .route-summary {
    gap: 20px;
  }
  .route-summary p {
    margin-left: 0;
  }
}
@media (max-width: 480px) {
  .map-filters {
    width: 100%;
  }
  .day-filter {
    width: 100%;
  }
  .route-summary {
    padding: 16px;
  }
  .route-summary strong {
    font-size: 19px;
  }
}
</style>
