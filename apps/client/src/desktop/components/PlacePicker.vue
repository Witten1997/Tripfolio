<script setup lang="ts">
import { MapPin, Search } from '@lucide/vue'
import { ElAlert, ElButton, ElInput } from 'element-plus'
import { computed, nextTick, onScopeDispose, ref, useId, watch } from 'vue'

import AmapView from '@/desktop/components/AmapView.vue'
import { reverseGeocode, type GeoCoordinate, type GeoPlace } from '@/shared/api/geo'
import { roundedCoordinate } from '@/shared/geo/amap'
import { isCoordinate, type MapPoint } from '@/shared/geo/itineraryRoute'
import { usePlaceSearch } from '@/shared/geo/usePlaceSearch'

const props = withDefaults(
  defineProps<{
    value: {
      place_name: string
      address: string
      latitude: number | null
      longitude: number | null
    }
    city?: string
    disabled?: boolean
    validationError?: string
  }>(),
  { city: '', disabled: false, validationError: '' },
)
const emit = defineEmits<{ select: [place: GeoPlace]; clear: []; locating: [busy: boolean] }>()
const id = useId()
const searchInput = ref<InstanceType<typeof ElInput>>()
const point = computed(() =>
  isCoordinate(props.value)
    ? { latitude: props.value.latitude, longitude: props.value.longitude }
    : null,
)
const searcher = usePlaceSearch(props.city, () => point.value)
const { keyword, city, results, loading, searched, error } = searcher
const mapOpened = ref(false)
const locating = ref(false)
const locatingMessage = ref('')
const longitudeText = ref('')
const latitudeText = ref('')
let reverseGeneration = 0
let controller: AbortController | undefined

watch(locating, (busy) => emit('locating', busy), { flush: 'sync' })

const markers = computed<MapPoint[]>(() =>
  point.value
    ? [{ ...point.value, id: 'selected', title: props.value.place_name || '所选位置', number: 1 }]
    : [],
)

watch(
  () => [props.value.place_name, props.value.address, props.value.latitude, props.value.longitude],
  () => {
    reverseGeneration++
    controller?.abort()
    locating.value = false
    longitudeText.value = props.value.longitude === null ? '' : String(props.value.longitude)
    latitudeText.value = props.value.latitude === null ? '' : String(props.value.latitude)
  },
  { immediate: true, flush: 'sync' },
)

function select(place: GeoPlace) {
  if (props.disabled) return
  reverseGeneration++
  controller?.abort()
  locating.value = false
  locatingMessage.value = '地点已选中，保存行程后将加入路线'
  emit('select', place)
  // 已选结果只在摘要里显示，避免搜索列表长期占满编辑区。
  results.value = []
  searched.value = false
}

function clearSelection() {
  if (props.disabled) return
  reverseGeneration++
  controller?.abort()
  locating.value = false
  locatingMessage.value = ''
  emit('clear')
  void nextTick(() => searchInput.value?.focus())
}

defineExpose({ focus: () => searchInput.value?.focus() })

async function choosePoint(coordinate: GeoCoordinate) {
  if (props.disabled) return
  const request = ++reverseGeneration
  controller?.abort()
  controller = new AbortController()
  const signal = controller.signal
  locating.value = true
  locatingMessage.value = ''
  try {
    const place = await reverseGeocode(coordinate, signal)
    if (request === reverseGeneration && !props.disabled) select(place)
  } catch {
    if (request !== reverseGeneration || signal.aborted || props.disabled) return
    select({
      ...coordinate,
      name: '地图选点',
      address: '',
      adcode: null,
      poi_id: null,
      provider: 'amap',
    })
    locatingMessage.value = '已保留选点位置，地址暂未查到，可先保存位置'
  } finally {
    if (request === reverseGeneration) locating.value = false
  }
}

function useCoordinates() {
  if (longitudeText.value.trim() === '' || latitudeText.value.trim() === '') {
    locatingMessage.value = '请同时填写经度与纬度'
    return
  }
  const coordinate = {
    longitude: Number(longitudeText.value),
    latitude: Number(latitudeText.value),
  }
  if (!isCoordinate(coordinate)) {
    locatingMessage.value = '请输入有效坐标：经度 −180 至 180，纬度 −90 至 90'
    return
  }
  void choosePoint(roundedCoordinate(coordinate.latitude, coordinate.longitude))
}

onScopeDispose(() => {
  reverseGeneration++
  controller?.abort()
  emit('locating', false)
})
</script>

<template>
  <section class="place-picker" aria-label="高德地点选择">
    <div class="place-heading">
      <h3><MapPin aria-hidden="true" />搜索地点</h3>
      <ElButton
        :disabled="disabled"
        :aria-expanded="mapOpened"
        :aria-controls="`${id}-map`"
        @click="mapOpened = !mapOpened"
        ><MapPin aria-hidden="true" />{{ mapOpened ? '收起地图' : '地图选点' }}</ElButton
      >
    </div>
    <div class="place-search">
      <div class="search-field">
        <label :for="`${id}-keyword`" class="visually-hidden">景点、餐厅或酒店</label>
        <ElInput
          ref="searchInput"
          :id="`${id}-keyword`"
          v-model="keyword"
          :disabled="disabled"
          maxlength="50"
          clearable
          placeholder="搜索景点、餐厅或酒店"
          :aria-invalid="!!validationError"
          :aria-describedby="validationError ? `${id}-error` : undefined"
          @keydown.enter.prevent.stop="searcher.search"
        />
      </div>
      <div class="city-field">
        <label :for="`${id}-city`" class="visually-hidden">城市（可选）</label>
        <ElInput
          :id="`${id}-city`"
          v-model="city"
          :disabled="disabled"
          maxlength="40"
          clearable
          placeholder="城市（可选）"
          @keydown.enter.prevent.stop="searcher.search"
        />
      </div>
      <ElButton
        class="place-search-button"
        :disabled="disabled"
        :loading="loading"
        aria-label="搜索地点"
        @click="searcher.search"
        ><Search v-if="!loading" aria-hidden="true"
      /></ElButton>
    </div>
    <p v-if="validationError" :id="`${id}-error`" class="place-error" role="alert">
      {{ validationError }}
    </p>
    <p v-if="loading || searched" class="place-status" role="status">
      <template v-if="loading">正在搜索地点…</template>
      <template v-else-if="searched && !error">{{
        results.length
          ? `找到 ${results.length} 个地点，选择一个填入行程`
          : '没有找到匹配地点，换个关键词或城市试试'
      }}</template>
    </p>
    <ElAlert v-if="error" :title="error" type="warning" :closable="false" show-icon />
    <ul v-if="results.length" class="place-results" aria-label="高德地点搜索结果">
      <li
        v-for="(place, index) in results"
        :key="place.poi_id ?? `${place.longitude}:${place.latitude}:${index}`"
      >
        <button type="button" :disabled="disabled" @click="select(place)">
          <span class="place-name">{{ place.name }}</span>
          <span class="place-address">{{ place.address || '地址未提供' }}</span>
          <span class="place-use">选择此地点</span>
        </button>
      </li>
    </ul>
    <div v-if="mapOpened" :id="`${id}-map`" class="place-map-area">
      <p class="place-hint">点击地图选点，也可在下方输入高德坐标。</p>
      <AmapView :points="markers" :center="point" :selectable="!disabled" @choose="choosePoint" />
      <div class="coordinate-inputs">
        <div>
          <label :for="`${id}-lng`">经度</label
          ><ElInput
            :id="`${id}-lng`"
            v-model="longitudeText"
            :disabled="disabled"
            inputmode="decimal"
            placeholder="116.397026"
            @keydown.enter.prevent.stop="useCoordinates"
          />
        </div>
        <div>
          <label :for="`${id}-lat`">纬度</label
          ><ElInput
            :id="`${id}-lat`"
            v-model="latitudeText"
            :disabled="disabled"
            inputmode="decimal"
            placeholder="39.918058"
            @keydown.enter.prevent.stop="useCoordinates"
          />
        </div>
        <ElButton :disabled="disabled" :loading="locating" @click="useCoordinates"
          >使用坐标</ElButton
        >
      </div>
    </div>
    <div v-if="point || value.place_name || value.address" class="selected-place">
      <span
        >{{ point ? '已选择' : '原有地点（未定位）' }} · {{ value.place_name || '所选位置' }}
        <small v-if="value.address">{{ value.address }}</small>
        <small v-else-if="point"
          >{{ point.longitude.toFixed(6) }}, {{ point.latitude.toFixed(6) }}</small
        ></span
      >
      <ElButton :disabled="disabled" text @click="clearSelection">清除地点</ElButton>
    </div>
    <p class="place-status" role="status">{{ locating ? '正在查找选点地址…' : locatingMessage }}</p>
  </section>
</template>

<style scoped>
.place-picker {
  margin: 0 0 18px;
  padding: 16px;
  background: color-mix(in srgb, var(--tf-surface-inset) 64%, transparent);
  border-radius: calc(var(--tf-radius-control) + 4px);
}
.place-heading,
.place-search,
.coordinate-inputs,
.selected-place {
  display: flex;
  align-items: end;
  gap: 12px;
}
.place-heading {
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.place-heading h3 {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  font-size: 15px;
  color: var(--tf-text-1);
}
.place-heading h3 svg,
.place-heading .el-button svg,
.place-search-button svg {
  width: 18px;
  height: 18px;
  stroke-width: 1.5;
}
.search-field {
  flex: 1;
  min-width: 0;
}
.city-field {
  flex: 0 1 150px;
  min-width: 0;
}
.place-search-button {
  width: var(--tf-control-size);
  min-width: var(--tf-control-size);
  padding: 0;
}
label {
  display: block;
  margin-bottom: 6px;
  font-size: 12px;
  color: var(--tf-text-2);
}
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  padding: 0;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
.place-status,
.place-hint {
  margin: 8px 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--tf-text-2);
}
.place-status:empty {
  margin: 0;
}
.place-error {
  margin: 8px 0;
  font-size: 13px;
  color: var(--tf-danger);
}
.place-results {
  list-style: none;
  padding: 0;
  margin: 8px 0 12px;
  max-height: 240px;
  overflow-y: auto;
  overscroll-behavior: contain;
  border: 1px solid var(--tf-line);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface);
}
.place-results li + li {
  border-top: 1px solid var(--tf-line-soft);
}
.place-results button {
  width: 100%;
  min-height: 60px;
  padding: 10px 12px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 4px 12px;
  text-align: left;
  border: 0;
  background: transparent;
  color: var(--tf-text-1);
  cursor: pointer;
  font: inherit;
}
.place-results button:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: -3px;
}
.place-name {
  font-size: 14px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.place-address {
  grid-column: 1;
  font-size: 12px;
  color: var(--tf-text-2);
  overflow-wrap: anywhere;
}
.place-use {
  grid-column: 2;
  grid-row: 1 / 3;
  align-self: center;
  font-size: 12px;
  color: var(--tf-accent);
}
.coordinate-inputs {
  margin-top: 12px;
}
.coordinate-inputs > div {
  flex: 1;
  min-width: 0;
}
.selected-place {
  justify-content: space-between;
  align-items: center;
  margin-top: 12px;
  color: var(--tf-text-2);
  font-size: 13px;
}
.selected-place small {
  display: block;
  margin-top: 3px;
  font-variant-numeric: tabular-nums;
}
.selected-place > span {
  min-width: 0;
  overflow-wrap: anywhere;
}
@media (hover: hover) {
  .place-results button:hover {
    background: var(--tf-accent-soft);
  }
}
@media (max-width: 600px) {
  .place-search,
  .coordinate-inputs {
    flex-wrap: wrap;
  }
  .search-field {
    flex-basis: 100%;
  }
  .city-field {
    flex: 1;
  }
  .coordinate-inputs > div {
    flex-basis: 40%;
  }
  .place-picker {
    padding: 12px;
  }
}
</style>
