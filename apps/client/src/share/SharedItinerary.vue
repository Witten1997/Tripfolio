<script setup lang="ts">
import { ElAlert, ElRadioButton, ElRadioGroup } from 'element-plus'
import { computed } from 'vue'

import { formatDistance, formatDuration } from '@/shared/geo/itineraryRoute'
import { travelModeLabels, type TravelMode } from '@/shared/geo/travelModes'
import { itineraryKindLabels } from '@/shared/travel/itineraryKinds'
import { groupByDay } from '@/shared/travel/tripDays'

import type { PublicItineraryItem, PublicTrip, PublicRoutes } from './api'
import { buildSharedMap, type SharedLeg } from './sharedRoutes'
import type { SharedRoutesState } from './useSharedTrip'

const props = defineProps<{
  trip: PublicTrip
  items: PublicItineraryItem[]
  routes: PublicRoutes | null
  routesState: SharedRoutesState
}>()
// 出行方式与地图视图共用：在两个视图里切换哪个都是同一份选择。
const mode = defineModel<TravelMode>('mode', { required: true })
const modes = Object.keys(travelModeLabels) as TravelMode[]

const days = computed(() => groupByDay(props.trip.start_date, props.trip.end_date, props.items))
const shared = computed(() =>
  buildSharedMap(props.items, props.routesState === 'ready' ? props.routes : null),
)
const hasLegs = computed(() => shared.value.legs.length > 0)

function timeOf(item: PublicItineraryItem): string {
  const start = item.planned_start_local?.slice(11, 16)
  const end = item.planned_end_local?.slice(11, 16)
  if (start && end) return `${start} – ${end}`
  if (start && item.planned_duration_minutes)
    return `${start} · 约 ${item.planned_duration_minutes} 分钟`
  return start ?? ''
}

const legOf = (id: string) => shared.value.byOrigin.get(id)

/** 与主人侧 ItineraryTab 同一套文案：算出的给距离与用时，未算出的说明原因。 */
function legText(leg: SharedLeg): string {
  if (leg.route)
    return `${travelModeLabels[mode.value]} ${formatDistance(leg.route.distance_meters)} · ${formatDuration(leg.route.duration_seconds)}`
  if (props.routesState === 'idle' || props.routesState === 'loading') return '正在计算路程…'
  return '这段未能算出路线'
}
</script>

<template>
  <div class="share-itinerary">
    <div v-if="hasLegs" class="share-route-options">
      <span>相邻地点路程</span>
      <ElRadioGroup v-model="mode" size="small" aria-label="行程距离计算方式">
        <ElRadioButton v-for="m in modes" :key="m" :value="m">{{
          travelModeLabels[m]
        }}</ElRadioButton>
      </ElRadioGroup>
      <span class="share-route-options__hint">按排列顺序估算，不含停留时间</span>
    </div>
    <ElAlert
      v-if="routesState === 'unavailable'"
      type="warning"
      show-icon
      :closable="false"
      title="路线服务暂不可用，站间距离暂不显示，行程顺序不受影响。"
    />
    <ol class="share-days" aria-label="按天行程">
      <li v-for="day in days" :key="day.date" class="share-day tf-surface">
        <h2 class="share-day__title">
          {{ day.title }}<small v-if="day.outside">（旅行日期之外）</small>
        </h2>
        <p v-if="!day.items.length" class="share-day__empty">这一天暂无安排</p>
        <ol v-else class="share-items">
          <li v-for="(item, index) in day.items" :key="item.id" class="share-item">
            <span class="share-item__number" aria-hidden="true">{{ index + 1 }}</span>
            <div class="share-item__body">
              <p class="share-item__title">
                {{ item.title }}
                <span class="share-item__kind">{{ itineraryKindLabels[item.kind] }}</span>
              </p>
              <p v-if="timeOf(item)" class="share-item__time">{{ timeOf(item) }}</p>
              <p v-if="item.place_name || item.address" class="share-item__place">
                {{ item.place_name }}<span v-if="item.address"> · {{ item.address }}</span>
              </p>
              <p v-if="legOf(item.id)" class="share-item__leg" role="status">
                <span class="share-item__next">下一站 · {{ legOf(item.id)?.to.title }}</span>
                {{ legText(legOf(item.id)!) }}
                <span v-if="legOf(item.id)?.crossDay">（跨日接续）</span>
                <span v-if="legOf(item.id)?.missingBetween"
                  >（中间 {{ legOf(item.id)?.missingBetween }} 项尚未定位）</span
                >
              </p>
            </div>
          </li>
        </ol>
      </li>
    </ol>
  </div>
</template>

<style scoped>
.share-itinerary {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.share-route-options {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
  padding: 12px 14px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-card);
  background: var(--tf-surface);
}
.share-route-options > span:first-child {
  font-weight: 600;
}
.share-route-options__hint {
  font-size: 12px;
  color: var(--tf-text-2);
}
.share-days,
.share-items {
  margin: 0;
  padding: 0;
  list-style: none;
}
.share-days {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.share-day {
  padding: 16px 18px;
  border-radius: var(--tf-radius-card);
}
.share-day__title {
  margin: 0 0 10px;
  font-size: 16px;
}
.share-day__title small {
  margin-left: 6px;
  font-size: 12px;
  font-weight: normal;
  color: var(--tf-text-3);
}
.share-day__empty {
  margin: 0;
  color: var(--tf-text-3);
}
.share-item {
  display: flex;
  gap: 12px;
  padding: 10px 0;
  border-top: 1px solid var(--tf-line-soft);
}
.share-item__number {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
  font-size: 12px;
}
.share-item__body p {
  margin: 0;
}
.share-item__title {
  font-weight: 600;
}
.share-item__kind {
  margin-left: 6px;
  padding: 1px 6px;
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-inset);
  font-size: 12px;
  font-weight: normal;
  color: var(--tf-text-2);
}
.share-item__time,
.share-item__place {
  margin-top: 2px;
  font-size: 13px;
  color: var(--tf-text-2);
}
.share-item__body .share-item__leg {
  margin-top: 6px;
  padding: 6px 10px;
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-inset);
  font-size: 13px;
  color: var(--tf-text-2);
  font-variant-numeric: tabular-nums;
}
.share-item__next {
  margin-right: 6px;
  color: var(--tf-text-1);
  font-weight: 600;
}
</style>
