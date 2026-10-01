<script setup lang="ts">
import { computed, useId } from 'vue'

import provinces from '@/shared/travel/assets/china-provinces.json'
import type { VisitedCity } from '@/shared/travel/dashboardView'

const props = defineProps<{ cities: VisitedCity[]; selected: string }>()
const emit = defineEmits<{ select: [cityCode: string] }>()
const id = useId()
const visited = computed(() => new Set(props.cities.map((city) => city.provinceCode)))
const selectedProvince = computed(
  () => props.cities.find((city) => city.code === props.selected)?.provinceCode,
)
const markers = computed(() =>
  props.cities
    .filter((city) => city.latitude !== null && city.longitude !== null)
    .map((city) => ({
      ...city,
      x: (city.longitude! - 73) * 9.2 + 30,
      y: (54 - city.latitude!) * 10.4 + 20,
    })),
)
function selectProvince(code: string) {
  const city = props.cities.find((item) => item.provinceCode === code)
  if (city) emit('select', city.code)
}
</script>

<template>
  <div class="footprint-map">
    <!-- 省级边界来源：https://geo.datav.aliyun.com/areas_v3/bound/100000_full.json -->
    <svg viewBox="0 0 650 440" role="group" aria-label="国内旅行足迹地图，可选择已到访的省份和城市">
      <defs>
        <clipPath :id="`${id}-main`"><rect width="650" height="415" /></clipPath>
        <clipPath :id="`${id}-inset`">
          <rect x="548" y="298" width="82" height="116" rx="4" />
        </clipPath>
      </defs>
      <g :clip-path="`url(#${id}-main)`">
        <path
          v-for="province in provinces"
          :key="province.code"
          :d="province.path"
          fill-rule="evenodd"
          class="province"
          :class="{
            visited: visited.has(province.code),
            selected: selectedProvince === province.code,
          }"
          :role="visited.has(province.code) ? 'button' : undefined"
          :tabindex="visited.has(province.code) ? 0 : undefined"
          :aria-label="`${province.name}${visited.has(province.code) ? '，已到访，查看城市' : '，尚未到访'}`"
          @click="selectProvince(province.code)"
          @keydown.enter="selectProvince(province.code)"
          @keydown.space.prevent="selectProvince(province.code)"
        >
          <title>{{ province.name }}</title>
        </path>
      </g>
      <g
        v-for="city in markers"
        :key="city.code"
        :transform="`translate(${city.x},${city.y})`"
        class="city-marker"
        :class="{ selected: selected === city.code }"
        role="button"
        tabindex="0"
        :aria-label="`${city.name}，${city.places.length}处地点，${city.visits}次到访`"
        :aria-pressed="selected === city.code"
        @click="emit('select', city.code)"
        @keydown.enter="emit('select', city.code)"
        @keydown.space.prevent="emit('select', city.code)"
      >
        <circle r="13" class="hit-area" />
        <circle r="5" class="dot" />
        <text v-if="selected === city.code" x="10" y="-10">{{ city.name }}</text>
        <title>{{ city.name }} · {{ city.places.length }} 处地点</title>
      </g>
      <rect x="548" y="298" width="82" height="116" rx="4" class="inset" />
      <g :clip-path="`url(#${id}-inset)`" aria-hidden="true">
        <path
          v-for="province in provinces"
          :key="province.code"
          :d="province.inset"
          class="province"
          fill-rule="evenodd"
        />
      </g>
      <text x="589" y="430" text-anchor="middle" class="inset-label">南海诸岛</text>
    </svg>
    <div class="map-legend">
      <span><i></i>到访省级地区</span><span><i class="unvisited"></i>尚未到访</span
      ><span>点击圆点查看城市</span>
    </div>
  </div>
</template>

<style scoped>
.footprint-map {
  min-width: 0;
}
svg {
  display: block;
  width: 100%;
  max-height: 470px;
}
.province {
  fill: var(--tf-surface-sunken);
  stroke: var(--tf-line);
  stroke-width: 0.65;
  vector-effect: non-scaling-stroke;
}
.province.visited {
  fill: color-mix(in srgb, var(--tf-accent) 28%, var(--tf-surface-raised));
  cursor: pointer;
}
.province.selected {
  fill: color-mix(in srgb, var(--tf-accent) 50%, var(--tf-surface-raised));
}
.province.visited:hover {
  fill: color-mix(in srgb, var(--tf-accent) 65%, var(--tf-surface-raised));
}
.province:focus-visible {
  stroke: var(--tf-text-1);
  stroke-width: 2;
  outline: none;
}
.city-marker {
  cursor: pointer;
  outline: none;
}
.hit-area {
  fill: transparent;
}
.dot {
  fill: var(--tf-accent);
  stroke: var(--tf-accent-contrast);
  stroke-width: 2;
}
.city-marker.selected .dot,
.city-marker:focus-visible .dot {
  stroke: var(--tf-text-1);
  stroke-width: 3;
}
.city-marker text {
  font-size: 13px;
  font-weight: 650;
  fill: var(--tf-text-1);
  paint-order: stroke;
  stroke: var(--tf-surface-raised);
  stroke-width: 4;
  stroke-linejoin: round;
}
.inset {
  fill: var(--tf-surface-raised);
  stroke: var(--tf-line-soft);
}
.inset-label {
  fill: var(--tf-text-3);
  font-size: 10px;
}
.map-legend {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 16px;
  color: var(--tf-text-3);
  font-size: 12px;
}
.map-legend span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.map-legend i {
  width: 10px;
  height: 10px;
  border: 1px solid var(--tf-line);
  border-radius: 3px;
  background: color-mix(in srgb, var(--tf-accent) 28%, var(--tf-surface-raised));
}
.map-legend .unvisited {
  background: var(--tf-surface-sunken);
}
</style>
