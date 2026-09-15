import { describe, expect, it, vi } from 'vitest'

import { ApiError, type Problem } from '@/shared/api/problem'

import type { PublicItineraryItem, PublicRoutes, PublicTrip } from './api'
import type { ShareClient } from './shareClient'
import { classifySharedError, useSharedTrip, type SharedTripDeps } from './useSharedTrip'

const client = {} as ShareClient
const trip: PublicTrip = {
  name: '东京',
  destination: '日本东京',
  start_date: '2026-10-01',
  end_date: '2026-10-07',
  timezone: 'Asia/Tokyo',
}
const routes: PublicRoutes = {
  mode: 'driving',
  legs: [],
  failed_leg_count: 0,
  unlocated_item_count: 0,
}

function apiError(status: number, code: string) {
  return new ApiError({
    type: 'about:blank',
    title: code,
    status,
    code,
    request_id: 'r',
  } as Problem)
}

function fakeDeps(overrides: Partial<SharedTripDeps> = {}): SharedTripDeps {
  return {
    trip: vi.fn(async () => trip),
    items: vi.fn(async (): Promise<PublicItineraryItem[]> => []),
    routes: vi.fn(async () => routes),
    ...overrides,
  }
}

describe('classifySharedError', () => {
  it('把接口错误归为失效、已删除或网络', () => {
    expect(classifySharedError(apiError(404, 'SHARE_NOT_FOUND'))).toBe('invalid')
    expect(classifySharedError(apiError(403, 'ACCOUNT_DELETING'))).toBe('invalid')
    expect(classifySharedError(apiError(401, 'AUTH_REQUIRED'))).toBe('invalid')
    expect(classifySharedError(apiError(410, 'TRIP_DELETED'))).toBe('deleted')
    expect(classifySharedError(apiError(500, 'INTERNAL_ERROR'))).toBe('network')
    expect(classifySharedError(new TypeError('offline'))).toBe('network')
  })
})

describe('useSharedTrip', () => {
  it('并行加载旅行与行程', async () => {
    const s = useSharedTrip(client, fakeDeps())
    await s.load()
    expect(s.trip.value?.name).toBe('东京')
    expect(s.error.value).toBeNull()
    expect(s.loading.value).toBe(false)
  })

  it('失效链接进入 invalid，已删除进入 deleted', async () => {
    const invalid = useSharedTrip(
      client,
      fakeDeps({
        trip: vi.fn(async () => {
          throw apiError(404, 'SHARE_NOT_FOUND')
        }),
      }),
    )
    await invalid.load()
    expect(invalid.error.value).toBe('invalid')
    const deleted = useSharedTrip(
      client,
      fakeDeps({
        items: vi.fn(async () => {
          throw apiError(410, 'TRIP_DELETED')
        }),
      }),
    )
    await deleted.load()
    expect(deleted.error.value).toBe('deleted')
  })

  it('算路成功为 ready，失败降级为 unavailable 且清空结果', async () => {
    const s = useSharedTrip(client, fakeDeps())
    await s.loadRoutes('driving')
    expect(s.routesState.value).toBe('ready')
    expect(s.routes.value).toEqual(routes)
    const failing = useSharedTrip(
      client,
      fakeDeps({
        routes: vi.fn(async () => {
          throw apiError(503, 'DEPENDENCY_UNAVAILABLE')
        }),
      }),
    )
    await failing.loadRoutes('walking')
    expect(failing.routesState.value).toBe('unavailable')
    expect(failing.routes.value).toBeNull()
  })
})
