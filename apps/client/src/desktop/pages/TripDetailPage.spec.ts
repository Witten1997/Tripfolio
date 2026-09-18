import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

const getTrip = vi.hoisted(() => vi.fn())
vi.mock('@/shared/api/trips', async (original) => ({
  ...(await original<typeof import('@/shared/api/trips')>()),
  getTrip,
}))
vi.mock('@/shared/stores/metadata', () => ({
  useMetadataStore: () => ({ status: 'ready', load: vi.fn(), minorUnits: () => 2 }),
}))

import TripDetailPage from './TripDetailPage.vue'

const trip = {
  id: 't1',
  name: '大兴安岭',
  destination: '大兴安岭',
  start_date: '2026-09-20',
  end_date: '2026-09-28',
  timezone: 'Asia/Shanghai',
  currency_code: 'CNY',
}

beforeEach(() => {
  getTrip.mockReset().mockResolvedValue(trip)
})

async function page(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/trips', name: 'trips', component: { template: '<div />' } },
      { path: '/recycle-bin', name: 'recycle-bin', component: { template: '<div />' } },
      {
        path: '/trips/:tripId',
        component: TripDetailPage,
        children: [
          ['trip-itinerary', 'itinerary'],
          ['trip-ledger', 'ledger'],
          ['trip-map', 'map'],
          ['trip-packing', 'packing'],
          ['trip-album', 'album'],
          ['trip-todos', 'todos'],
        ].map(([name, path]) => ({ path, name, component: { template: '<div />' } })),
      },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(TripDetailPage, {
    global: {
      plugins: [router, createPinia()],
      stubs: { TripEditorDialog: true, TripShareDialog: true },
    },
  })
  await flushPromises()
  return { router, wrapper }
}

describe('旅行详情页的地图悬浮按钮', () => {
  it('只在行程与地图页签显示，并指向另一个视图', async () => {
    const { router, wrapper } = await page('/trips/t1/itinerary')
    expect(wrapper.get('.map-toggle').attributes('aria-label')).toBe('看地图')
    expect(wrapper.get('.map-toggle').attributes('href')).toBe('/trips/t1/map')
    expect(wrapper.get('.map-toggle').classes()).toContain('map-toggle')

    await router.push('/trips/t1/map')
    await flushPromises()
    expect(wrapper.get('.map-toggle').attributes('aria-label')).toBe('看行程')
    expect(wrapper.get('.map-toggle').attributes('href')).toBe('/trips/t1/itinerary')

    await router.push('/trips/t1/ledger')
    await flushPromises()
    expect(wrapper.find('.map-toggle').exists()).toBe(false)
    wrapper.unmount()
  })

  it('旅行加载失败时不显示按钮', async () => {
    getTrip.mockRejectedValue(new Error('网络错误'))
    const { wrapper } = await page('/trips/t1/itinerary')
    expect(wrapper.find('.map-toggle').exists()).toBe(false)
    wrapper.unmount()
  })
})
