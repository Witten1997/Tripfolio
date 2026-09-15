import type { GeoCoordinate } from '@/shared/api/geo'

export type LngLatPair = [number, number]
export interface AmapEvent {
  lnglat: { getLat(): number; getLng(): number }
}
export interface AmapOverlay {
  setMap(map: AmapMap | null): void
}
export interface AmapMap {
  add(overlays: AmapOverlay[]): void
  remove(overlays: AmapOverlay[]): void
  destroy(): void
  resize?(): void
  on(event: string, handler: (event: AmapEvent) => void): void
  off(event: string, handler: (event: AmapEvent) => void): void
  setFitView(
    overlays?: AmapOverlay[] | null,
    immediately?: boolean,
    avoid?: number[],
    maxZoom?: number,
  ): void
  setZoomAndCenter(zoom: number, center: LngLatPair, immediately?: boolean): void
  getCenter(): { getLat(): number; getLng(): number }
  getZoom(): number
  /** 容器像素转经纬度；SDK 未提供时组件退回按地图中心缩放。 */
  containerToLngLat?(pixel: unknown): { getLat(): number; getLng(): number } | null
}
export interface AmapNamespace {
  Map: new (container: HTMLElement, options: Record<string, unknown>) => AmapMap
  Marker: new (options: Record<string, unknown>) => AmapOverlay
  Polyline: new (options: Record<string, unknown>) => AmapOverlay
  Pixel: new (x: number, y: number) => unknown
}

declare global {
  interface Window {
    _AMapSecurityConfig?: { serviceHost: string }
  }
}

let loading: Promise<AmapNamespace> | undefined

export function amapServiceHost(raw: string | undefined, origin: string): string {
  const url = new URL(raw?.trim() || '/_AMapService', origin)
  if (
    !['http:', 'https:'].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    !url.pathname.replace(/\/$/, '').endsWith('/_AMapService')
  ) {
    throw new Error('地图服务暂不可用，请稍后重试')
  }
  return url.toString().replace(/\/$/, '')
}

export function loadAMap(): Promise<AmapNamespace> {
  if (loading) return loading
  const key = import.meta.env.VITE_AMAP_JS_KEY?.trim()
  if (!key) return Promise.reject(new Error('地图服务暂未启用，仍可通过搜索地点选择位置'))
  loading = (async () => {
    const { default: loader } = await import('@amap/amap-jsapi-loader')
    window._AMapSecurityConfig = {
      serviceHost: amapServiceHost(import.meta.env.VITE_AMAP_SERVICE_HOST, window.location.origin),
    }
    let timer: ReturnType<typeof setTimeout> | undefined
    try {
      return await Promise.race([
        loader.load({ key, version: '2.0', plugins: [] }) as Promise<AmapNamespace>,
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => reject(new Error('地图加载超时，请检查网络后重试')), 15_000)
        }),
      ])
    } catch {
      if ('reset' in loader && typeof loader.reset === 'function') loader.reset()
      throw new Error('地图未能加载，请检查网络后重试；也可以通过搜索地点选择位置')
    } finally {
      if (timer) clearTimeout(timer)
    }
  })().catch((error: unknown) => {
    loading = undefined
    throw error
  })
  return loading
}

export function lngLat(point: GeoCoordinate): LngLatPair {
  return [point.longitude, point.latitude]
}

/**
 * 滚轮以指针为锚点缩放时，缩放后地图中心在「缩放前容器像素坐标」中的位置。
 * 记容器中心 O、指针 P、缩放倍数 S＝2^Δzoom，中心需沿 O→P 移动 (P−O)×(1−1/S)，
 * 这样指针下的地理位置在缩放前后落在同一像素；Δzoom 为 0 时结果即容器中心。
 */
export function cursorZoomCenter(
  cursor: { x: number; y: number },
  size: { width: number; height: number },
  zoomDelta: number,
): { x: number; y: number } {
  const ratio = 1 - 2 ** -zoomDelta
  return {
    x: size.width / 2 + (cursor.x - size.width / 2) * ratio,
    y: size.height / 2 + (cursor.y - size.height / 2) * ratio,
  }
}
export function roundedCoordinate(latitude: number, longitude: number): GeoCoordinate {
  return {
    latitude: Math.round(latitude * 1e6) / 1e6,
    longitude: Math.round(longitude * 1e6) / 1e6,
  }
}
