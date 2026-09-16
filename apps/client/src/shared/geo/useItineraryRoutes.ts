import { computed, onScopeDispose, shallowRef, watch, type Ref } from 'vue'

import { ApiError } from '@/shared/api/auth'
import {
  calculateRoute,
  calculateTripRoutes,
  GeoRouteError,
  type GeoRoute,
  type TravelMode,
} from '@/shared/api/geo'
import type { ItineraryItem } from '@/shared/api/itinerary'
import {
  itineraryLegs,
  itineraryWaypoints,
  routeKey,
  routeMapPaths,
} from '@/shared/geo/itineraryRoute'
import type { RouteLeg as ItineraryRouteLeg } from '@/shared/geo/itineraryRoute'

export interface RouteState {
  status: 'loading' | 'ready' | 'failed'
  route?: GeoRoute
  message?: string
}

// 只缓存供应商路线，不缓存账号、行程标题或业务记录；页面切换可复用，5 分钟失效。
const cache = new Map<string, { route: GeoRoute; expires: number }>()
const ttl = 5 * 60_000
const retryBackoff = [1200, 2400]
const maxAutoWait = 8000
// 与服务端批量算路接口的坐标上限一致；超出时退回逐段算路。
const batchMaxPoints = 50
const requestConcurrency = 3

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

type ModeSource = Ref<TravelMode> | ((leg: ItineraryRouteLeg) => TravelMode)

export function useItineraryRoutes(items: () => ItineraryItem[], modeSource: ModeSource) {
  const points = computed(() => itineraryWaypoints(items()))
  const segments = computed(() => itineraryLegs(points.value))
  const selectedSegments = computed(() =>
    segments.value.map((leg) => {
      const mode = typeof modeSource === 'function' ? modeSource(leg) : modeSource.value
      return { ...leg, mode, key: routeKey(leg.from, leg.to, mode) }
    }),
  )
  const states = shallowRef(new Map<string, RouteState>())
  const missingCount = computed(() => items().length - points.value.length)
  let generation = 0
  let controller: AbortController | undefined

  const legs = computed(() =>
    selectedSegments.value.map((leg) => ({
      ...leg,
      ...(states.value.get(leg.key) ?? {
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
    const requested = selectedSegments.value
    const next = new Map<string, RouteState>()
    const pending = new Map<string, (typeof requested)[number]>()
    requested.forEach((leg) => {
      if (next.has(leg.key)) return
      const route = cached(leg.key)
      next.set(leg.key, route ? { status: 'ready', route } : { status: 'loading' })
      if (!route) pending.set(leg.key, leg)
    })
    states.value = next
    let unavailable: string | null = null
    function update(key: string, value: RouteState) {
      if (request !== generation || signal.aborted) return
      states.value = new Map(states.value).set(key, value)
    }
    async function requestLeg(leg: (typeof requested)[number]): Promise<GeoRoute> {
      for (let attempt = 0; ; attempt++) {
        if (signal.aborted) throw new DOMException('请求已取消', 'AbortError')
        try {
          return await calculateRoute(leg.from, leg.to, leg.mode, signal)
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
    async function worker(queue: (typeof requested)[number][], cursor: { value: number }) {
      while (cursor.value < queue.length && !signal.aborted) {
        const leg = queue[cursor.value++]!
        const key = leg.key
        if (unavailable) {
          pending.delete(key)
          update(key, { status: 'failed', message: unavailable })
          continue
        }
        try {
          const route = await requestLeg(leg)
          if (signal.aborted || request !== generation) return
          remember(key, route)
          pending.delete(key)
          update(key, { status: 'ready', route })
        } catch (cause) {
          if (signal.aborted || request !== generation) return
          const failure = routeFailure(cause)
          if (failure.stopsQueue) unavailable = failure.message
          pending.delete(key)
          update(key, { status: 'failed', message: failure.message })
        }
      }
    }

    // 只合并连续驾车段。交通方式切换点自然成为分组边界，步行和骑行保持单段算路。
    const drivingGroups: (typeof requested)[] = []
    let run: typeof requested = []
    function flushDrivingRun() {
      for (let start = 0; start < run.length;) {
        const remaining = run.length - start
        let size = Math.min(batchMaxPoints - 1, remaining)
        if (remaining - size === 1 && size > 2) size--
        const group = run.slice(start, start + size)
        if (group.length >= 2 && group.some((leg) => pending.has(leg.key)))
          drivingGroups.push(group)
        start += size
      }
      run = []
    }
    requested.forEach((leg) => {
      if (leg.mode === 'driving') run.push(leg)
      else flushDrivingRun()
    })
    flushDrivingRun()

    const batchedKeys = new Set(drivingGroups.flatMap((group) => group.map((leg) => leg.key)))
    const directQueue = [...pending.values()].filter((leg) => !batchedKeys.has(leg.key))
    async function runWorkers(queue: (typeof requested)[number][]) {
      const cursor = { value: 0 }
      await Promise.all(
        Array.from({ length: Math.min(requestConcurrency, queue.length) }, () =>
          worker(queue, cursor),
        ),
      )
    }
    const batchRequests = drivingGroups.map(async (group) => {
      try {
        const groupPoints = [group[0]!.from, ...group.map((leg) => leg.to)]
        const routes = await calculateTripRoutes(groupPoints, 'driving', signal)
        if (routes.length !== group.length) throw new Error('批量路线段数不匹配')
        if (request !== generation || signal.aborted) return
        group.forEach((leg, index) => {
          const route = routes[index]!
          remember(leg.key, route)
          pending.delete(leg.key)
          update(leg.key, { status: 'ready', route })
        })
      } catch {
        // 老服务端或批量结果不可拆分时，保留待处理项并退回单段请求。
      }
    })
    await Promise.all([Promise.all(batchRequests), runWorkers(directQueue)])
    if (signal.aborted || request !== generation) return

    await runWorkers([...pending.values()])
  }

  watch(
    () =>
      JSON.stringify([
        selectedSegments.value.map((leg) => [
          leg.id,
          leg.mode,
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
