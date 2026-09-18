<script setup lang="ts">
import { ElAlert, ElButton } from 'element-plus'
import { Maximize2, Minimize2 } from '@lucide/vue'
import { h, onBeforeUnmount, onMounted, ref, render, watch } from 'vue'

import type { GeoCoordinate, TravelMode } from '@/shared/api/geo'
import {
  cursorZoomCenter,
  lngLat,
  loadAMap,
  roundedCoordinate,
  type AmapEvent,
  type AmapMap,
  type AmapNamespace,
  type AmapOverlay,
  type LngLatPair,
} from '@/shared/geo/amap'
import type { MapPath, MapPoint } from '@/shared/geo/itineraryRoute'
import { useThemeStore } from '@/shared/stores/theme'
import { itineraryKindIcons } from '@/shared/travel/itineraryKindVisuals'
import { itineraryKindLabels } from '@/shared/travel/itineraryKinds'

const props = withDefaults(
  defineProps<{
    points?: MapPoint[]
    paths?: MapPath[]
    center?: GeoCoordinate | null
    selectable?: boolean
  }>(),
  { points: () => [], paths: () => [], center: null, selectable: false },
)
const emit = defineEmits<{ choose: [point: GeoCoordinate]; focusPoint: [id: string] }>()
const container = ref<HTMLElement>()
const view = ref<HTMLElement>()
const theme = useThemeStore()
const loading = ref(true)
const error = ref<string | null>(null)
const isFullscreen = ref(false)
let sdk: AmapNamespace | undefined
let map: AmapMap | undefined
let overlays: AmapOverlay[] = []
let markers: AmapOverlay[] = []
let resizeObserver: ResizeObserver | undefined
let generation = 0
let fittedPoints = ''

function choose(event: AmapEvent) {
  if (props.selectable)
    emit('choose', roundedCoordinate(event.lnglat.getLat(), event.lnglat.getLng()))
}

function fit() {
  if (!map) return
  if (overlays.length)
    map.setFitView(overlays, true, [50, 50, 70, 50], markers.length === 1 ? 16 : 17)
  else if (props.center) map.setZoomAndCenter(13, lngLat(props.center), true)
}

function focus(point: GeoCoordinate) {
  map?.setZoomAndCenter(16, lngLat(point), true)
}

function syncFullscreen() {
  isFullscreen.value = document.fullscreenElement === view.value
}

async function toggleFullscreen() {
  if (!view.value) return
  try {
    if (document.fullscreenElement) await document.exitFullscreen()
    else await view.value.requestFullscreen()
  } catch {
    // 浏览器未授权全屏时保留地图的其他操作。
  }
}

/** 以指针位置为锚点：把指针下的经纬度留在原像素；容器无布局或 SDK 不支持换算时退回地图中心。 */
function anchoredCenter(event: WheelEvent, zoomDelta: number): LngLatPair {
  const current = map?.getCenter()
  const fallback: LngLatPair = current ? [current.getLng(), current.getLat()] : [116.4074, 39.9042]
  const box = container.value?.getBoundingClientRect()
  if (!map || !sdk || !box?.width || !box?.height || !map.containerToLngLat) return fallback
  const anchor = cursorZoomCenter(
    { x: event.clientX - box.left, y: event.clientY - box.top },
    { width: box.width, height: box.height },
    zoomDelta,
  )
  const point = map.containerToLngLat(new sdk.Pixel(anchor.x, anchor.y))
  return point ? [point.getLng(), point.getLat()] : fallback
}

function containWheel(event: WheelEvent) {
  if (!map || loading.value || error.value) return
  if (event.cancelable) event.preventDefault()
  if (!Number.isFinite(event.deltaY) || event.deltaY === 0) return
  // 部分 SDK／浏览器组合仅开启 scrollWheel 仍不缩放。统一处理像素、行和页单位，
  // 触控板保留细小步幅，鼠标每个事件最多一级；直接更新以兼容减少动态偏好。
  const unit =
    event.deltaMode === WheelEvent.DOM_DELTA_LINE
      ? 40
      : event.deltaMode === WheelEvent.DOM_DELTA_PAGE
        ? container.value?.clientHeight || 600
        : 1
  const delta = Math.max(-1, Math.min(1, (-event.deltaY * unit) / 120))
  const current = map.getZoom()
  const zoom = Math.max(2, Math.min(20, current + delta))
  if (zoom === current) return
  map.setZoomAndCenter(zoom, anchoredCenter(event, zoom - current), true)
}

function draw() {
  if (!map || !sdk) return
  map.remove(overlays)
  const css = getComputedStyle(document.documentElement)
  const accent = css.getPropertyValue('--tf-accent').trim()
  const walking = css.getPropertyValue('--tf-chart-3').trim()
  const cycling = css.getPropertyValue('--tf-chart-2').trim()
  const muted = css.getPropertyValue('--tf-text-3').trim()
  const outline = css.getPropertyValue('--tf-surface').trim()
  const roadStyles: Record<TravelMode, { color: string; weight: number; dashed: boolean }> = {
    driving: { color: accent, weight: 5, dashed: false },
    walking: { color: walking, weight: 4, dashed: true },
    cycling: { color: cycling, weight: 5, dashed: false },
  }
  const lines = props.paths
    .filter((path) => path.points.length >= 2)
    .map((path) => {
      const road = path.kind === 'road'
      const style = road ? roadStyles[path.mode ?? 'driving'] : undefined
      return new sdk!.Polyline({
        path: path.points.map(lngLat),
        strokeColor: style?.color ?? muted,
        strokeWeight: style?.weight ?? 2,
        strokeOpacity: road ? 0.9 : 0.7,
        strokeStyle: style?.dashed || !road ? 'dashed' : 'solid',
        strokeDasharray: style?.dashed ? [3, 7] : [6, 6],
        showDir: road && path.mode !== 'walking',
        lineJoin: 'round',
        lineCap: 'round',
        isOutline: road,
        outlineColor: outline,
        borderWeight: 2,
        zIndex: path.mode === 'walking' ? 42 : path.mode === 'cycling' ? 41 : 40,
      })
    })
  markers = props.points.map((point) => {
    const kind = point.kind ?? 'other'
    const button = document.createElement('button')
    const badge = document.createElement('span')
    const icon = document.createElement('span')
    const number = document.createElement('span')
    const title = document.createElement('span')
    button.type = 'button'
    button.className = `tf-map-marker tf-itinerary-kind--${kind}`
    badge.className = 'tf-map-marker__badge'
    icon.className = 'tf-map-marker__icon'
    number.className = 'tf-map-marker__number'
    title.className = 'tf-map-marker__title'
    number.textContent = String(point.number)
    title.textContent = point.title
    render(
      h(itineraryKindIcons[kind], {
        'aria-hidden': 'true',
        focusable: 'false',
        size: 14,
        strokeWidth: 2,
      }),
      icon,
    )
    badge.append(icon, number)
    button.append(badge, title)
    button.title = `${point.number}. ${itineraryKindLabels[kind]}：${point.title}`
    button.setAttribute(
      'aria-label',
      `查看第 ${point.number} 站，${itineraryKindLabels[kind]}：${point.title}`,
    )
    button.addEventListener('click', (event) => {
      event.stopPropagation()
      emit('focusPoint', point.id)
    })
    return new sdk!.Marker({
      position: lngLat(point),
      content: button,
      anchor: 'center',
      offset: new sdk!.Pixel(0, 0),
      zIndex: 100 + point.number,
      bubble: false,
    })
  })
  overlays = [...lines, ...markers]
  map.add(overlays)
  // 路线可能绕出点位包围盒；道路返回或方式改变后重新适配，主题重绘不改变视角。
  const signature = JSON.stringify([
    props.points.map((point) => [point.id, point.latitude, point.longitude]),
    props.paths.map((path) => [path.id, path.kind, path.mode, path.points.map(lngLat)]),
  ])
  if (signature !== fittedPoints) {
    fittedPoints = signature
    fit()
  }
}

async function initialize() {
  const request = ++generation
  loading.value = true
  error.value = null
  try {
    const loaded = await loadAMap()
    if (request !== generation || !container.value) return
    sdk = loaded
    map?.destroy()
    map = new sdk.Map(container.value, {
      zoom: 11,
      center: props.center ? lngLat(props.center) : [116.4074, 39.9042],
      viewMode: '2D',
      mapStyle: 'amap://styles/fresh', // 草色青：暖绿底色，与「有机自然」主题同色系
      resizeEnable: true,
      keyboardEnable: true,
      // 由容器统一处理滚轮，避免 SDK 与容器重复响应同一个事件。
      scrollWheel: false,
      zoomEnable: true,
      zooms: [2, 20],
      animateEnable: false,
    })
    map.on('click', choose)
    fittedPoints = ''
    draw()
    resizeObserver?.disconnect()
    resizeObserver = new ResizeObserver(() => map?.resize?.())
    resizeObserver.observe(container.value)
  } catch (cause) {
    if (request === generation)
      error.value = cause instanceof Error ? cause.message : '地图暂不可用，请重试'
  } finally {
    if (request === generation) loading.value = false
  }
}

watch(() => [props.points, props.paths, theme.current], draw, { deep: true })
onMounted(() => {
  document.addEventListener('fullscreenchange', syncFullscreen)
  void initialize()
})
onBeforeUnmount(() => {
  generation++
  document.removeEventListener('fullscreenchange', syncFullscreen)
  resizeObserver?.disconnect()
  map?.off('click', choose)
  map?.destroy()
  map = undefined
})
defineExpose({ focus, fit })
</script>

<template>
  <div ref="view" class="amap-view" @wheel.capture="containWheel">
    <div
      ref="container"
      class="amap-canvas"
      role="region"
      :aria-label="
        selectable
          ? '高德选点地图，也可使用搜索或坐标输入选点'
          : '高德行程地图，点位也在路线列表中提供'
      "
    />
    <div v-if="loading" class="amap-message" role="status">正在加载地图…</div>
    <div v-if="error" class="amap-message">
      <ElAlert type="warning" :title="error" :closable="false" show-icon />
      <ElButton @click="initialize">重新加载地图</ElButton>
    </div>
    <div v-if="!loading && !error" class="amap-controls" aria-label="地图操作">
      <button
        type="button"
        class="amap-control-icon"
        :aria-label="isFullscreen ? '退出全屏' : '全屏'"
        :title="isFullscreen ? '退出全屏' : '全屏'"
        @click="toggleFullscreen"
      >
        <Minimize2 v-if="isFullscreen" aria-hidden="true" />
        <Maximize2 v-else aria-hidden="true" />
      </button>
      <button type="button" @click="fit">总览</button>
    </div>
  </div>
</template>

<style scoped>
.amap-view {
  position: relative;
  min-width: 0;
  min-height: 300px;
  background: var(--tf-surface-sunken);
  border-radius: var(--tf-radius-control);
  overflow: hidden;
}
.amap-canvas {
  position: relative;
  z-index: 0;
  width: 100%;
  height: 100%;
  min-height: inherit;
}
.amap-message,
.amap-controls {
  z-index: 1;
}
.amap-message {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 20px;
  background: var(--tf-surface);
  color: var(--tf-text-2);
}
.amap-controls {
  position: absolute;
  top: 12px;
  right: 12px;
  display: flex;
  gap: 1px;
  border-radius: var(--tf-radius-control);
  background: var(--tf-line);
  box-shadow: var(--tf-shadow-2);
  overflow: hidden;
}
.amap-controls button {
  min-width: var(--tf-control-size);
  min-height: var(--tf-control-size);
  padding: 8px 12px;
  background: var(--tf-surface);
  color: var(--tf-text-1);
  border: 0;
  cursor: pointer;
  font: inherit;
}
.amap-controls .amap-control-icon {
  display: grid;
  place-items: center;
  padding: 8px;
}
.amap-control-icon svg {
  width: 18px;
  height: 18px;
}
.amap-controls button:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: -3px;
}
:deep(.tf-map-marker) {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  width: auto;
  max-width: 180px;
  min-height: 25px;
  padding: 0 8px 0 0;
  border: 0;
  border-radius: 22px;
  background: transparent;
  color: var(--tf-text-1);
  box-shadow: none;
  cursor: pointer;
  font-family: inherit;
}
:deep(.tf-map-marker__badge) {
  position: relative;
  display: grid;
  flex: 0 0 25px;
  place-items: center;
  box-sizing: border-box;
  width: 25px;
  height: 25px;
  border: 1px solid var(--tf-surface);
  border-radius: 50%;
  background: var(--tf-itinerary-kind-color, var(--tf-accent));
  color: var(--tf-itinerary-icon);
  box-shadow: var(--tf-shadow-2);
}
:deep(.tf-map-marker__icon) {
  display: grid;
  place-items: center;
}
:deep(.tf-map-marker__icon svg) {
  width: 14px;
  height: 14px;
}
:deep(.tf-map-marker__number) {
  position: absolute;
  right: -6px;
  bottom: -6px;
  display: grid;
  width: auto;
  min-width: 20px;
  height: 20px;
  place-items: center;
  box-sizing: border-box;
  padding: 0 2px;
  border: 1px solid var(--tf-surface);
  border-radius: 50%;
  background: var(--tf-itinerary-kind-color, var(--tf-accent));
  color: var(--tf-itinerary-icon);
  font-size: 15px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  line-height: 1;
}
:deep(.tf-map-marker__title) {
  display: block;
  max-width: 122px;
  overflow: hidden;
  padding: 5px 8px;
  border: 1px solid var(--tf-line-soft);
  border-radius: 999px;
  background: var(--tf-surface-raised);
  color: var(--tf-text-1);
  font-size: 12px;
  line-height: 1.3;
  text-overflow: ellipsis;
  white-space: nowrap;
  box-shadow: var(--tf-shadow-1);
}
:deep(.tf-map-marker:focus-visible) {
  outline: 3px solid var(--tf-text-1);
  outline-offset: 3px;
}
.amap-view:fullscreen {
  width: 100vw;
  height: 100vh;
  min-height: 100vh;
  border-radius: 0;
}
.amap-view:fullscreen .amap-canvas {
  min-height: 100vh;
}
@media (hover: hover) {
  .amap-controls button:hover {
    background: var(--tf-accent-soft);
  }
}
</style>
