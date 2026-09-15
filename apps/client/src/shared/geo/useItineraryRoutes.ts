import { computed, onScopeDispose, shallowRef, watch, type Ref } from 'vue'

import { ApiError } from '@/shared/api/auth'
import { calculateRoute, GeoRouteError, type GeoRoute, type TravelMode } from '@/shared/api/geo'
import type { ItineraryItem } from '@/shared/api/itinerary'
import {
  itineraryLegs,
  itineraryWaypoints,
  routeKey,
  routeMapPaths,
} from '@/shared/geo/itineraryRoute'

export interface RouteState {
  status: 'loading' | 'ready' | 'failed'
  route?: GeoRoute
  message?: string
}

// 只缓存供应商路线，不缓存账号、行程标题或业务记录；页面切换可复用，5 分钟失效。
const cache = new Map<string, { route: GeoRoute; expires: number }>()
const ttl = 5 * 60_000
const requestInterval = 1100
const retryBackoff = [1200, 2400]
const maxAutoWait = 8000

function routeFailure(cause: unknown) {
  let message = '暂时无法计算，请稍后重试'
  let retryAfter: number | null = 0
  let stopsQueue = false
  if (cause instanceof ApiError) {
    retryAfter = null
    if (cause.code === 'RESOURCE_NOT_FOUND') message = '此出行方式暂无可用路线'
    else if (cause.problem?.status === 401 || cause.problem?.status === 403) {
      message = '登录状态已失效，请重新登录后计算'
      stopsQueue = true
    } else if (cause.code === 'RATE_LIMITED') {
      message = '请求较多，请稍后重新计算'
      retryAfter = cause instanceof GeoRouteError ? (cause.retryAfterMs ?? 1000) : 1000
      stopsQueue = retryAfter > maxAutoWait
    } else if (cause.code === 'DEPENDENCY_UNAVAILABLE') {
      message = cause.problem?.detail || '路线服务暂不可用，请稍后重试'
      retryAfter = cause instanceof GeoRouteError ? cause.retryAfterMs : 1000
      // 后端明确没有可恢复时间：凭证、权限、配额或尚未装配。
      stopsQueue = retryAfter === null || retryAfter > maxAutoWait
    } else message = cause.message
  }
  return { message, retryAfter, stopsQueue }
}

/** 取消时清理计时器和监听器，不让旧方式／旧日期继续消耗配额。 */
function pause(ms: number, signal: AbortSignal): Promise<boolean> {
  if (signal.aborted) return Promise.resolve(false)
  if (ms <= 0) return Promise.resolve(true)
  return new Promise((resolve) => {
    const finish = (ready: boolean) => {
      clearTimeout(timer)
      signal.removeEventListener('abort', abort)
      resolve(ready)
    }
    const abort = () => finish(false)
    const timer = setTimeout(() => finish(true), ms)
    signal.addEventListener('abort', abort, { once: true })
  })
}

function cached(key: string): GeoRoute | undefined {
  const entry = cache.get(key)
  if (entry && entry.expires > Date.now()) return entry.route
  cache.delete(key)
  return undefined
}

function remember(key: string, route: GeoRoute) {
  if (cache.size >= 256) cache.delete(cache.keys().next().value!)
  cache.set(key, { route, expires: Date.now() + ttl })
}

export function useItineraryRoutes(items: () => ItineraryItem[], mode: Ref<TravelMode>) {
  const points = computed(() => itineraryWaypoints(items()))
  const segments = computed(() => itineraryLegs(points.value))
  const states = shallowRef(new Map<string, RouteState>())
  const missingCount = computed(() => items().length - points.value.length)
  let generation = 0
  let controller: AbortController | undefined

  const legs = computed(() =>
    segments.value.map((leg) => ({
      ...leg,
      ...(states.value.get(routeKey(leg.from, leg.to, mode.value)) ?? {
        status: 'loading' as const,
      }),
    })),
  )
  const loading = computed(() => legs.value.some((leg) => leg.status === 'loading'))
  const failedCount = computed(() => legs.value.filter((leg) => leg.status === 'failed').length)
  const readyCount = computed(() => legs.value.filter((leg) => leg.status === 'ready').length)
  const totals = computed(() =>
    legs.value.reduce(
      (sum, leg) => ({
        distance: sum.distance + (leg.route?.distance_meters ?? 0),
        duration: sum.duration + (leg.route?.duration_seconds ?? 0),
      }),
      { distance: 0, duration: 0 },
    ),
  )
  const paths = computed(() => legs.value.flatMap((leg) => routeMapPaths(leg, leg.route)))
  const byOrigin = computed(() => new Map(legs.value.map((leg) => [leg.from.id, leg])))

  async function refresh() {
    const request = ++generation
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal
    const travelMode = mode.value
    const next = new Map<string, RouteState>()
    const queue = segments.value.filter((leg) => {
      const key = routeKey(leg.from, leg.to, travelMode)
      if (next.has(key)) return false
      const route = cached(key)
      next.set(key, route ? { status: 'ready', route } : { status: 'loading' })
      return !route
    })
    states.value = next
    let cursor = 0
    let unavailable: string | null = null
    let lastStarted = -Infinity
    function update(key: string, value: RouteState) {
      if (request !== generation || signal.aborted) return
      states.value = new Map(states.value).set(key, value)
    }
    async function requestLeg(leg: (typeof queue)[number]): Promise<GeoRoute> {
      for (let attempt = 0; ; attempt++) {
        if (!(await pause(requestInterval - (Date.now() - lastStarted), signal)))
          throw new DOMException('请求已取消', 'AbortError')
        if (signal.aborted) throw new DOMException('请求已取消', 'AbortError')
        lastStarted = Date.now()
        try {
          return await calculateRoute(leg.from, leg.to, travelMode, signal)
        } catch (cause) {
          if (signal.aborted) throw cause
          const failure = routeFailure(cause)
          const backoff = retryBackoff[attempt]
          if (
            backoff === undefined ||
            failure.retryAfter === null ||
            failure.retryAfter > maxAutoWait
          )
            throw cause
          // 少量抖动避免多个页面同一时刻重试；后端仍按 Key 做最终节奏保护。
          const wait = Math.max(backoff, failure.retryAfter) + Math.floor(Math.random() * 200)
          if (!(await pause(wait, signal))) throw new DOMException('请求已取消', 'AbortError')
        }
      }
    }
    // 串行、平滑请求；单段短暂失败仅重试该段，不跳过后续未请求的路段。
    async function worker() {
      while (cursor < queue.length && !signal.aborted) {
        const leg = queue[cursor++]!
        const key = routeKey(leg.from, leg.to, travelMode)
        if (unavailable) {
          update(key, { status: 'failed', message: unavailable })
          continue
        }
        try {
          const route = await requestLeg(leg)
          if (signal.aborted || request !== generation) return
          remember(key, route)
          update(key, { status: 'ready', route })
        } catch (cause) {
          if (signal.aborted || request !== generation) return
          const failure = routeFailure(cause)
          if (failure.stopsQueue) unavailable = failure.message
          update(key, { status: 'failed', message: failure.message })
        }
      }
    }
    await worker()
  }

  watch(
    () =>
      JSON.stringify([
        mode.value,
        segments.value.map((leg) => [
          leg.id,
          leg.from.latitude,
          leg.from.longitude,
          leg.to.latitude,
          leg.to.longitude,
        ]),
      ]),
    () => {
      void refresh()
    },
    { immediate: true },
  )
  onScopeDispose(() => {
    generation++
    controller?.abort()
  })
  return {
    points,
    legs,
    paths,
    byOrigin,
    missingCount,
    loading,
    failedCount,
    readyCount,
    totals,
    refresh,
  }
}
