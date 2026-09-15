<script setup lang="ts">
import { ElAlert, ElRadioButton, ElRadioGroup } from 'element-plus'
import { computed, ref } from 'vue'

import AmapView from '@/desktop/components/AmapView.vue'
import { formatDistance, formatDuration, type Waypoint } from '@/shared/geo/itineraryRoute'
import { travelModeLabels, type TravelMode } from '@/shared/geo/travelModes'

import type { PublicItineraryItem, PublicRoutes } from './api'
import { buildSharedMap } from './sharedRoutes'
import type { SharedRoutesState } from './useSharedTrip'

const props = defineProps<{
  items: PublicItineraryItem[]
  routes: PublicRoutes | null
  routesState: SharedRoutesState
}>()
const mode = defineModel<TravelMode>('mode', { required: true })
const modes = Object.keys(travelModeLabels) as TravelMode[]
const map = ref<InstanceType<typeof AmapView>>()

const shared = computed(() =>
  buildSharedMap(props.items, props.routesState === 'ready' ? props.routes : null),
)
const unlocated = computed(() => props.items.length - shared.value.points.length)
const summary = computed(() => {
  const s = shared.value
  if (s.legs.length === 0) return null
  if (props.routesState === 'loading') return '正在计算路线…'
  if (props.routesState !== 'ready' || s.readyCount === 0) {
    return '路线服务暂不可用，图中为示意连线，不代表实际道路。'
  }
  const partial = s.failedCount > 0 ? `；另有 ${s.failedCount} 段未能算出，按示意连线显示` : ''
  return `${travelModeLabels[mode.value]}约 ${formatDistance(s.distanceMeters)}，预计 ${formatDuration(s.durationSeconds)}（仅含已算出的 ${s.readyCount} 段${partial}）`
})

function focus(point: Waypoint<PublicItineraryItem>) {
  map.value?.focus(point)
}
</script>

<template>
  <section class="share-map tf-surface" aria-label="行程地图">
    <div class="share-map__bar">
      <ElRadioGroup v-model="mode" aria-label="出行方式" size="small">
        <ElRadioButton v-for="m in modes" :key="m" :value="m">{{
          travelModeLabels[m]
        }}</ElRadioButton>
      </ElRadioGroup>
      <p v-if="summary" class="share-map__summary" role="status">{{ summary }}</p>
    </div>
    <ElAlert
      v-if="unlocated > 0"
      :title="`${unlocated} 个项目没有定位信息，未在地图上显示`"
      type="info"
      show-icon
      :closable="false"
    />
    <p v-if="!shared.points.length" class="share-map__empty">本次行程还没有定位信息。</p>
    <template v-else>
      <AmapView ref="map" class="share-map__canvas" :points="shared.points" :paths="shared.paths" />
      <ol class="share-map__list" aria-label="地图点位">
        <li v-for="point in shared.points" :key="point.id">
          <button type="button" class="share-map__point" @click="focus(point)">
            <span class="share-map__number" aria-hidden="true">{{ point.number }}</span
            >{{ point.title }}
          </button>
        </li>
      </ol>
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
.share-map__summary,
.share-map__empty {
  margin: 0;
  font-size: 13px;
  color: var(--tf-text-2);
}
.share-map__canvas {
  height: clamp(320px, 55vh, 560px);
  border-radius: var(--tf-radius-panel);
  overflow: hidden;
}
.share-map__list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.share-map__point {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: var(--tf-control-size);
  padding: 0 12px 0 6px;
  border: 1px solid var(--tf-line);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-sunken);
  color: var(--tf-text-1);
  font: inherit;
  cursor: pointer;
}
.share-map__point:focus-visible {
  outline: revert;
}
.share-map__number {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
  font-size: 12px;
}
</style>
