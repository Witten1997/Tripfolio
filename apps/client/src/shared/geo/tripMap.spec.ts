import { ElSelect } from 'element-plus'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AmapView from '@/desktop/components/AmapView.vue'
import TripMapTab from '@/desktop/pages/trip/TripMapTab.vue'
import { calculateRoute } from '@/shared/api/geo'
import { listAllItineraryItems, type ItineraryItem } from '@/shared/api/itinerary'
import { getRoutePlan, type RoutePlan } from '@/shared/api/routePlan'

vi.mock('@/shared/travel/tripContext', () => ({ useTripContext: () => ({ tripId: 'trip' }) }))
vi.mock('@/shared/api/itinerary', async (original) => ({
  ...(await original<typeof import('@/shared/api/itinerary')>()),
  listAllItineraryItems: vi.fn(),
}))
vi.mock('@/shared/api/routePlan', async (original) => ({
  ...(await original<typeof import('@/shared/api/routePlan')>()),
  getRoutePlan: vi.fn(),
}))
vi.mock('@/shared/api/geo', async (original) => ({
  ...(await original<typeof import('@/shared/api/geo')>()),
  calculateRoute: vi.fn(),
}))

let sequence = 0
let items: ItineraryItem[]
beforeEach(() => {
  vi.useFakeTimers()
  sequence++
  items = ['c', 'b', 'a'].map(
    (id, index) =>
      ({
        id,
        title: id,
        place_name: id,
        scheduled_on: id === 'c' ? '2026-10-02' : '2026-10-01',
        sort_order: 2 - index,
        latitude: 35 + sequence / 100 + index / 1000,
        longitude: 116 + index / 1000,
      }) as ItineraryItem,
  )
  vi.mocked(listAllItineraryItems)
    .mockReset()
    .mockImplementation(async () => items)
  vi.mocked(getRoutePlan)
    .mockReset()
    .mockResolvedValue({
      preference: { short_mode: 'walking', short_distance_meters: 1500 },
      summary: {
        revision: '1',
        status: 'ready',
        total_distance_meters: 200,
        total_duration_seconds: 120,
        ready_leg_count: 2,
        total_leg_count: 2,
        missing_point_count: 0,
        calculated_at: '2026-10-01T00:00:00Z',
      },
      legs: [
        {
          id: 'leg-a-b',
          from_item_id: 'a',
          to_item_id: 'b',
          version: '1',
          mode: 'driving',
          mode_source: 'preference',
          direct_distance_meters: 100,
          route_distance_meters: 100,
          route_duration_seconds: 60,
          status: 'ready',
          error_code: null,
          calculated_at: '2026-10-01T00:00:00Z',
        },
        {
          id: 'leg-b-c',
          from_item_id: 'b',
          to_item_id: 'c',
          version: '1',
          mode: 'cycling',
          mode_source: 'manual',
          direct_distance_meters: 100,
          route_distance_meters: 100,
          route_duration_seconds: 60,
          status: 'ready',
          error_code: null,
          calculated_at: '2026-10-01T00:00:00Z',
        },
      ],
    } as RoutePlan)
  vi.mocked(calculateRoute)
    .mockReset()
    .mockImplementation(async (from, to, mode) => ({
      mode,
      path: [from, to],
      provider: 'amap',
      distance_meters: 100,
      duration_seconds: 60,
    }))
})
afterEach(() => vi.useRealTimers())

async function settleRoutes() {
  await flushPromises()
  await vi.advanceTimersByTimeAsync(2500)
  await flushPromises()
}

function setup() {
  return mount(TripMapTab, {
    global: { stubs: { AmapView: true, ItineraryItemDialog: true, RouterLink: true } },
  })
}

describe('行程地图视图', () => {
  it('默认明确显示整趟旅行，按日期和顺序展示所有点；单日筛选不连到次日', async () => {
    const view = setup()
    await settleRoutes()
    expect(view.text()).toContain('整趟旅行')
    expect(view.findComponent(ElSelect).props('modelValue')).toBe('all')
    expect(
      (view.findComponent(AmapView).props('points') ?? []).map((point: { id: string }) => point.id),
    ).toEqual(['a', 'b', 'c'])
    expect(view.text()).toContain('跨日接续')
    view.findComponent(ElSelect).vm.$emit('update:modelValue', '2026-10-01')
    await settleRoutes()
    expect(
      (view.findComponent(AmapView).props('points') ?? []).map((point: { id: string }) => point.id),
    ).toEqual(['a', 'b'])
    expect(view.text()).not.toContain('跨日接续')
    expect(view.text()).toContain('1 / 1 段已计算')
    view.unmount()
  })

  it('缺坐标的项目单独提示，不把示意距离计入里程', async () => {
    items[1] = { ...items[1]!, latitude: null, longitude: null }
    const view = setup()
    await settleRoutes()
    expect(view.findComponent(AmapView).props('points')).toHaveLength(2)
    expect(view.text()).toContain('1 项待补充位置')
    expect(view.text()).toContain('中间 1 项尚未定位')
    expect(view.text()).toContain('已算路段里程')
    expect(view.text()).toContain('100 米')
    view.unmount()
  })

  it('按路线计划为每段使用不同交通方式', async () => {
    const view = setup()
    await settleRoutes()
    expect(vi.mocked(calculateRoute).mock.calls.map((call) => call[2])).toEqual([
      'walking',
      'cycling',
    ])
    expect(view.text()).toContain('步行 100 米')
    expect(view.text()).toContain('骑行 100 米')
    expect(view.text()).toContain('道路预计用时')
    view.unmount()
  })
})
