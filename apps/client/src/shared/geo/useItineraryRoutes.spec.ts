import { effectScope, nextTick, ref, shallowRef } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/shared/api/auth'
import {
  calculateRoute,
  calculateTripRoutes,
  GeoRouteError,
  type GeoCoordinate,
  type GeoRoute,
  type TravelMode,
} from '@/shared/api/geo'
import type { ItineraryItem } from '@/shared/api/itinerary'
import { useItineraryRoutes } from '@/shared/geo/useItineraryRoutes'

vi.mock('@/shared/api/geo', async (original) => ({
  ...(await original<typeof import('@/shared/api/geo')>()),
  calculateRoute: vi.fn(),
  calculateTripRoutes: vi.fn(),
}))
const scopes: ReturnType<typeof effectScope>[] = []
let fixtureId = 0

function items(count = 3) {
  fixtureId++
  return ['a', 'b', 'c', 'd', 'e', 'f'].slice(0, count).map(
    (id, index) =>
      ({
        id,
        title: id,
        place_name: id,
        scheduled_on: '2026-10-01',
        sort_order: index,
        latitude: 30 + fixtureId / 100 + index / 1000,
        longitude: 116 + index / 1000,
      }) as ItineraryItem,
  )
}
const route = (a: GeoCoordinate, b: GeoCoordinate, mode: TravelMode): GeoRoute => ({
  mode,
  path: [a, b],
  provider: 'amap',
  distance_meters: mode === 'walking' ? 200 : 300,
  duration_seconds: 100,
})
const settle = async () => {
  await nextTick()
  await vi.runAllTimersAsync()
  await nextTick()
}
function setup(count = 3) {
  const list = shallowRef(items(count))
  const mode = ref<TravelMode>('driving')
  const scope = effectScope()
  scopes.push(scope)
  const result = scope.run(() => useItineraryRoutes(() => list.value, mode))!
  return { list, mode, result, scope }
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.mocked(calculateTripRoutes).mockReset().mockRejectedValue(new Error('批量接口不可用'))
  vi.mocked(calculateRoute)
    .mockReset()
    .mockImplementation(async (a, b, mode) => route(a, b, mode))
})
afterEach(() => {
  scopes.forEach((s) => s.stop())
  scopes.length = 0
  vi.useRealTimers()
})

describe('行程自动算路', () => {
  it('只请求相邻路段，刷新复用成功缓存，重排后计算新邻接关系', async () => {
    const { list, result } = setup()
    await settle()
    expect(calculateRoute).toHaveBeenCalledTimes(2)
    expect(result.totals.value.distance).toBe(600)
    await result.refresh()
    expect(calculateRoute).toHaveBeenCalledTimes(2)
    list.value = list.value.map((item, index) => ({ ...item, sort_order: 2 - index }))
    await settle()
    expect(result.legs.value.map((leg) => leg.id)).toEqual(['c>b', 'b>a'])
    expect(calculateRoute).toHaveBeenCalledTimes(4)
  })

  it('切换方式取消旧请求，旧返回不能污染新模式里程', async () => {
    let resolveOld: ((routes: GeoRoute[]) => void) | undefined
    vi.mocked(calculateTripRoutes).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveOld = resolve
        }),
    )
    const { mode, result } = setup()
    await nextTick()
    const oldCall = vi.mocked(calculateTripRoutes).mock.calls[0]
    const oldSignal = oldCall?.[2]
    mode.value = 'walking'
    await settle()
    expect(oldSignal?.aborted).toBe(true)
    const oldPoints = oldCall?.[0] ?? []
    resolveOld?.(
      oldPoints.slice(0, -1).map((point, index) => route(point, oldPoints[index + 1]!, 'driving')),
    )
    await settle()
    expect(result.totals.value.distance).toBe(400)
    expect(result.legs.value.every((leg) => leg.route?.mode === 'walking')).toBe(true)
  })

  it('驾车多段优先一次批量算路，成功时不再发逐段请求', async () => {
    vi.mocked(calculateTripRoutes).mockImplementation(async (points, mode) =>
      points.slice(0, -1).map((point, index) => route(point, points[index + 1]!, mode)),
    )
    const { result } = setup()
    await settle()
    expect(calculateTripRoutes).toHaveBeenCalledOnce()
    expect(calculateRoute).not.toHaveBeenCalled()
    expect(result.readyCount.value).toBe(2)
    expect(result.totals.value.distance).toBe(600)
  })

  it('部分路线不可用仍保留全部点位，只汇总成功路段', async () => {
    vi.mocked(calculateRoute).mockRejectedValueOnce(
      new ApiError({
        code: 'RESOURCE_NOT_FOUND',
        status: 404,
        title: '无路线',
        type: 'about:blank',
        request_id: 'test',
      }),
    )
    const { result } = setup()
    await settle()
    expect(result.points.value.length).toBe(3)
    expect(result.failedCount.value).toBe(1)
    expect(result.readyCount.value).toBe(1)
    expect(result.totals.value.distance).toBe(300)
    expect(result.paths.value.some((p) => p.kind === 'illustrative')).toBe(true)
  })

  it('离开页面取消所有在途请求', async () => {
    vi.mocked(calculateRoute).mockImplementation(() => new Promise(() => {}))
    const { scope } = setup()
    await nextTick()
    const signals = vi.mocked(calculateRoute).mock.calls.map((call) => call[3])
    scope.stop()
    expect(signals.every((signal) => signal?.aborted)).toBe(true)
  })

  it('六点五段首轮遇到两次短时限流会自动恢复，成功缓存不再请求', async () => {
    const seen = new Set<string>()
    vi.mocked(calculateRoute).mockImplementation(async (a, b, mode) => {
      const name = (a as ItineraryItem).id
      if (['b', 'd'].includes(name) && !seen.has(name)) {
        seen.add(name)
        throw new GeoRouteError(
          {
            type: 'about:blank',
            code: 'RATE_LIMITED',
            status: 429,
            title: 'QPS',
            request_id: 'test',
          },
          '1',
        )
      }
      return route(a, b, mode)
    })
    const { result } = setup(6)
    await settle()
    expect(calculateRoute).toHaveBeenCalledTimes(7)
    expect(result.readyCount.value).toBe(5)
    expect(result.failedCount.value).toBe(0)
    expect(result.totals.value.distance).toBe(1500)
    await result.refresh()
    expect(calculateRoute).toHaveBeenCalledTimes(7)
  })

  it('并发请求各路段，持续失败只重试两次且不跳过后续路段', async () => {
    vi.mocked(calculateRoute).mockImplementation(async (a, b, mode) => {
      if ((a as ItineraryItem).id === 'b')
        throw new GeoRouteError(
          {
            type: 'about:blank',
            code: 'DEPENDENCY_UNAVAILABLE',
            status: 503,
            title: '暂时繁忙',
            request_id: 'test',
          },
          '1',
        )
      return route(a, b, mode)
    })
    const { result } = setup(6)
    await settle()
    expect(calculateRoute).toHaveBeenCalledTimes(7)
    expect(result.failedCount.value).toBe(1)
    expect(result.readyCount.value).toBe(4)
  })

  it.each([
    ['DEPENDENCY_UNAVAILABLE', 503, null],
    ['RATE_LIMITED', 429, '60'],
    ['AUTH_REQUIRED', 401, null],
  ] as const)('配额／凭证错误或长时间限流不自动反复请求：%s', async (code, status, retry) => {
    vi.mocked(calculateRoute).mockRejectedValue(
      new GeoRouteError(
        { type: 'about:blank', code, status, title: '不可用', request_id: 'test' },
        retry,
      ),
    )
    const { result } = setup(6)
    await settle()
    expect(calculateRoute).toHaveBeenCalledTimes(3)
    expect(result.failedCount.value).toBe(5)
    expect(result.totals.value.distance).toBe(0)
  })

  it('尊重 Retry-After，退避等待中离开会清理计时器，不再发起重试', async () => {
    vi.mocked(calculateRoute).mockRejectedValue(
      new GeoRouteError(
        {
          type: 'about:blank',
          code: 'RATE_LIMITED',
          status: 429,
          title: 'QPS',
          request_id: 'test',
        },
        '3',
      ),
    )
    const { scope } = setup(6)
    await nextTick()
    await vi.advanceTimersByTimeAsync(2900)
    expect(calculateRoute).toHaveBeenCalledTimes(3)
    scope.stop()
    await vi.runAllTimersAsync()
    expect(calculateRoute).toHaveBeenCalledTimes(3)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('切换模式会取消已发出的旧方式请求，并立即计算新方式', async () => {
    vi.mocked(calculateRoute).mockImplementation((a, b, mode) => {
      if (mode === 'driving') return new Promise(() => {})
      return Promise.resolve(route(a, b, mode))
    })
    const { mode, result } = setup(6)
    await nextTick()
    await vi.runAllTimersAsync()
    await nextTick()
    const oldSignals = vi.mocked(calculateRoute).mock.calls.map((call) => call[3])
    expect(calculateRoute).toHaveBeenCalledTimes(3)
    mode.value = 'walking'
    await settle()
    expect(oldSignals.every((signal) => signal?.aborted)).toBe(true)
    expect(
      vi.mocked(calculateRoute).mock.calls.filter((call) => call[2] === 'driving'),
    ).toHaveLength(3)
    expect(result.readyCount.value).toBe(5)
    expect(result.legs.value.every((leg) => leg.route?.mode === 'walking')).toBe(true)
  })

  it('按每段方式拆分混合路线，只批量计算连续驾车段', async () => {
    vi.mocked(calculateTripRoutes).mockImplementation(async (points, mode) =>
      points.slice(0, -1).map((point, index) => route(point, points[index + 1]!, mode)),
    )
    const list = shallowRef(items(6))
    const modes: Record<string, TravelMode> = {
      'a>b': 'driving',
      'b>c': 'driving',
      'c>d': 'walking',
      'd>e': 'driving',
      'e>f': 'driving',
    }
    const scope = effectScope()
    scopes.push(scope)
    const result = scope.run(() =>
      useItineraryRoutes(
        () => list.value,
        (leg) => modes[leg.id] ?? 'driving',
      ),
    )!
    await settle()
    expect(calculateTripRoutes).toHaveBeenCalledTimes(2)
    expect(vi.mocked(calculateTripRoutes).mock.calls.map((call) => call[0])).toEqual([
      expect.arrayContaining([
        expect.objectContaining({ id: 'a' }),
        expect.objectContaining({ id: 'c' }),
      ]),
      expect.arrayContaining([
        expect.objectContaining({ id: 'd' }),
        expect.objectContaining({ id: 'f' }),
      ]),
    ])
    expect(calculateRoute).toHaveBeenCalledOnce()
    expect(vi.mocked(calculateRoute).mock.calls[0]?.[2]).toBe('walking')
    expect(result.legs.value.map((leg) => leg.mode)).toEqual([
      'driving',
      'driving',
      'walking',
      'driving',
      'driving',
    ])
    expect(
      result.paths.value.filter((path) => path.kind === 'road').map((path) => path.mode),
    ).toEqual(['driving', 'driving', 'walking', 'driving', 'driving'])
  })
})
