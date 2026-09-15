<script setup lang="ts">
import { ElAlert, ElOption, ElRadioButton, ElRadioGroup, ElSelect } from 'element-plus'
import { computed, ref } from 'vue'

import AmapView from '@/desktop/components/AmapView.vue'
import {
  formatDistance,
  formatDuration,
  sortedItinerary,
  type Waypoint,
} from '@/shared/geo/itineraryRoute'
import { travelModeLabels, type TravelMode } from '@/shared/geo/travelModes'
import { dayTitle } from '@/shared/travel/tripDays'

import type { PublicItineraryItem, PublicRoutes } from './api'
import { buildSharedMap, type SharedLeg } from './sharedRoutes'
import type { SharedRoutesState } from './useSharedTrip'

const props = defineProps<{
  items: PublicItineraryItem[]
  routes: PublicRoutes | null
  routesState: SharedRoutesState
}>()
const mode = defineModel<TravelMode>('mode', { required: true })
const modes = Object.keys(travelModeLabels) as TravelMode[]
const map = ref<InstanceType<typeof AmapView>>()
const focused = ref('')
const date = ref('all')

const dates = computed(() => [
  ...new Set(sortedItinerary(props.items).map((item) => item.scheduled_on)),
])
/** 日期筛选只作用于地图：整趟路段由服务端一次给出，按天筛选在本地完成，不额外请求。 */
const filtered = computed(() =>
  date.value === 'all'
    ? props.items
    : props.items.filter((item) => item.scheduled_on === date.value),
)
const shared = computed(() =>
  buildSharedMap(filtered.value, props.routesState === 'ready' ? props.routes : null),
)
const unlocated = computed(() => filtered.value.length - shared.value.points.length)
const calculating = computed(() => props.routesState === 'idle' || props.routesState === 'loading')
/** 只要有一段没算出来（含服务不可用），里程与用时只覆盖成功路段，标题也随之改成「已算路段」。 */
const partial = computed(() => props.routesState !== 'ready' || shared.value.failedCount > 0)
const mileage = computed(() =>
  shared.value.readyCount ? formatDistance(shared.value.distanceMeters) : '—',
)
const duration = computed(() =>
  shared.value.readyCount ? formatDuration(shared.value.durationSeconds) : '—',
)
const progress = computed(() => {
  const total = shared.value.legs.length
  if (calculating.value) return `正在计算 ${shared.value.readyCount} / ${total} 段…`
  return `${shared.value.readyCount} / ${total} 段已计算`
})

/** 与主人侧 TripMapTab 同一套文案：算出的给距离与用时，未算出的说明原因。 */
function legText(leg: SharedLeg): string {
  if (leg.route)
    return `${travelModeLabels[mode.value]} ${formatDistance(leg.route.distance_meters)} · ${formatDuration(leg.route.duration_seconds)}`
  if (calculating.value) return '正在计算下一段…'
  return '这段未能算出路线，图中为示意连线'
}

function focus(point: Waypoint<PublicItineraryItem>) {
  focused.value = point.id
  map.value?.focus(point)
}
</script>

<template>
  <section class="share-map tf-surface" aria-label="行程地图">
    <div class="share-map__bar">
      <ElSelect v-model="date" aria-label="地图日期范围" class="share-map__day" size="small">
        <ElOption label="整趟旅行" value="all" />
        <ElOption v-for="day in dates" :key="day" :label="dayTitle(day)" :value="day" />
      </ElSelect>
      <ElRadioGroup v-model="mode" aria-label="出行方式" size="small">
        <ElRadioButton v-for="m in modes" :key="m" :value="m">{{
          travelModeLabels[m]
        }}</ElRadioButton>
      </ElRadioGroup>
    </div>
    <ElAlert
      v-if="unlocated > 0"
      :title="`${unlocated} 个项目没有定位信息，未在地图上显示`"
      type="info"
      show-icon
      :closable="false"
    />
    <ElAlert
      v-if="routesState === 'unavailable'"
      type="warning"
      show-icon
      :closable="false"
      title="路线服务暂不可用，总里程与用时无法显示，图中为示意连线，不代表实际道路。"
    />
    <p v-if="!shared.points.length" class="share-map__empty">
      {{ date === 'all' ? '本次行程还没有定位信息。' : '这一天还没有定位信息。' }}
    </p>
    <template v-else>
      <div class="share-map__stats" :aria-busy="calculating">
        <div>
          <span>已定位地点</span>
          <strong
            >{{ shared.points.length }}<small> / {{ filtered.length }} 站</small></strong
          >
        </div>
        <div>
          <span>{{ partial ? '已算路段里程' : '道路总里程' }}</span>
          <strong>{{ mileage }}</strong>
        </div>
        <div>
          <span>预计{{ travelModeLabels[mode] }}用时</span>
          <strong>{{ duration }}</strong>
        </div>
        <p role="status">{{ progress }}<br />不含停留时间，按当前道路情况估算</p>
      </div>
      <div class="share-map__layout">
        <div class="share-map__main">
          <AmapView
            ref="map"
            class="share-map__canvas"
            :points="shared.points"
            :paths="shared.paths"
          />
          <p class="share-map__legend">
            <span class="share-map__legend-road" />高德道路路线
            <span class="share-map__legend-guide" />点位示意接续，不代表可通行道路
          </p>
        </div>
        <aside class="share-stops" aria-label="沿途各站">
          <div class="share-stops__heading">
            <h3>沿途各站</h3>
            <span>{{ shared.points.length }} 个地点 · 按行程顺序</span>
          </div>
          <ol class="share-stops__list">
            <li
              v-for="point in shared.points"
              :key="point.id"
              :class="{ 'share-stop--focused': focused === point.id }"
            >
              <button
                type="button"
                class="share-stop"
                :aria-current="focused === point.id ? 'location' : undefined"
                @click="focus(point)"
              >
                <span class="share-stop__number" aria-hidden="true">{{ point.number }}</span>
                <span class="share-stop__info">
                  <strong>{{ point.item.title }}</strong>
                  <span>{{ point.item.scheduled_on }} · {{ point.title }}</span>
                  <small v-if="point.item.address">{{ point.item.address }}</small>
                </span>
              </button>
              <p v-if="shared.byOrigin.get(point.id)" class="share-stop__leg">
                {{ legText(shared.byOrigin.get(point.id)!) }}
                <span v-if="shared.byOrigin.get(point.id)!.crossDay" class="share-stop__note"
                  >跨日接续</span
                >
                <span v-if="shared.byOrigin.get(point.id)!.missingBetween" class="share-stop__note"
                  >中间 {{ shared.byOrigin.get(point.id)!.missingBetween }} 项尚未定位</span
                >
              </p>
            </li>
          </ol>
        </aside>
      </div>
    </template>
  </section>
</template>

<style scoped>
.share-map {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px 18px;
  border-radius: var(--tf-radius-card);
}
.share-map__bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  align-items: center;
}
.share-map__day {
  width: 180px;
}
.share-map__empty {
  margin: 0;
  font-size: 13px;
  color: var(--tf-text-2);
}
.share-map__stats {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px 28px;
  padding: 14px 16px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-card);
  background: var(--tf-surface-inset);
}
.share-map__stats > div {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.share-map__stats span,
.share-map__stats p {
  font-size: 12px;
  color: var(--tf-text-2);
}
.share-map__stats strong {
  font-size: 20px;
  color: var(--tf-text-1);
  font-variant-numeric: tabular-nums;
}
.share-map__stats small {
  font-size: 13px;
  font-weight: 400;
  color: var(--tf-text-2);
}
.share-map__stats p {
  margin: 0;
  line-height: 1.8;
}
.share-map__layout {
  display: grid;
  gap: 12px;
}
.share-map__main {
  min-width: 0;
}
.share-map__canvas {
  height: clamp(320px, 55vh, 560px);
  border-radius: var(--tf-radius-panel);
  overflow: hidden;
}
.share-map__legend {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin: 10px 0 0;
  font-size: 12px;
  line-height: 1.7;
  color: var(--tf-text-2);
}
.share-map__legend-road,
.share-map__legend-guide {
  width: 24px;
  border-top: 3px solid var(--tf-accent);
}
.share-map__legend-guide {
  margin-left: 8px;
  border-top: 2px dashed var(--tf-text-3);
}
.share-stops {
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-sunken);
  overflow: hidden;
}
.share-stops__heading {
  padding: 14px 16px;
  border-bottom: 1px solid var(--tf-line-soft);
}
.share-stops__heading h3 {
  margin: 0 0 4px;
  font-size: 16px;
}
.share-stops__heading span {
  font-size: 12px;
  color: var(--tf-text-2);
}
.share-stops__list {
  margin: 0;
  padding: 0;
  list-style: none;
}
.share-stops__list li {
  padding: 12px 14px;
  border-bottom: 1px solid var(--tf-line-soft);
}
.share-stops__list li:last-child {
  border-bottom: 0;
}
.share-stop--focused {
  background: var(--tf-accent-soft);
}
.share-stop {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  width: 100%;
  min-height: var(--tf-control-size);
  padding: 4px;
  border: 0;
  border-radius: var(--tf-radius-control);
  background: transparent;
  color: var(--tf-text-1);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.share-stop:focus-visible {
  outline: revert;
  outline-offset: 2px;
}
.share-stop__number {
  flex-shrink: 0;
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
  font-size: 12px;
}
.share-stop__info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.share-stop__info > span,
.share-stop__info small {
  font-size: 12px;
  color: var(--tf-text-2);
}
.share-stop__leg {
  margin: 8px 0 0 38px;
  font-size: 13px;
  color: var(--tf-text-2);
  font-variant-numeric: tabular-nums;
}
.share-stop__note {
  margin-left: 6px;
  color: var(--tf-text-3);
}
@media (min-width: 1024px) {
  .share-map__layout {
    grid-template-columns: minmax(0, 1fr) 300px;
    align-items: start;
  }
  .share-stops__list {
    max-height: calc(clamp(320px, 55vh, 560px) - 74px);
    overflow-y: auto;
    overscroll-behavior: contain;
  }
}
</style>
