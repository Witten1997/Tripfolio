import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { ApiError } from '@/shared/api/problem'

const requests = vi.hoisted(() => ({ trip: vi.fn(), items: vi.fn(), routes: vi.fn() }))
vi.mock('./api', () => ({
  getSharedTrip: requests.trip,
  listAllSharedItineraryItems: requests.items,
  getSharedRoutes: requests.routes,
}))
vi.mock('@/desktop/components/AmapView.vue', () => ({
  __esModule: true,
  default: { name: 'AmapView', props: ['points', 'paths'], template: '<div data-map />' },
}))
import ShareLayout from './ShareLayout.vue'
import SharePage from './SharePage.vue'

const trip = {
  name: '杭州两日',
  destination: '杭州',
  start_date: '2026-10-01',
  end_date: '2026-10-02',
  timezone: 'Asia/Shanghai',
}
const item = (id: string, latitude: number | null) => ({
  id,
  scheduled_on: '2026-10-01',
  sort_order: id === 'A' ? 0 : 1,
  title: id,
  kind: 'attraction',
  place_name: '西湖',
  address: '湖滨',
  latitude,
  longitude: latitude === null ? null : 120.1,
  planned_start_local: null,
  planned_end_local: null,
  planned_duration_minutes: null,
})
const failure = (status: number, code: string) =>
  new ApiError({ type: 'about:blank', title: code, status, code, request_id: 'test' })

beforeEach(() => {
  vi.clearAllMocks()
  requests.trip.mockResolvedValue(trip)
  requests.items.mockResolvedValue([item('A', 30.1), item('B', 30.2)])
  requests.routes.mockRejectedValue(failure(503, 'DEPENDENCY_UNAVAILABLE'))
})

async function page() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/s/:token', component: SharePage, meta: { share: true, public: true } }],
  })
  await router.push('/s/first')
  const wrapper = mount(ShareLayout, { global: { plugins: [router, createPinia()] } })
  await flushPromises()
  return { router, wrapper }
}

describe('访客页面', () => {
  it.each([
    [404, 'SHARE_NOT_FOUND', '已失效'],
    [410, 'TRIP_DELETED', '已被删除'],
    [403, 'ACCOUNT_DELETING', '已失效'],
  ] as const)('%s 显示友好失效状态', async (status, code, message) => {
    requests.trip.mockRejectedValue(failure(status, code))
    const { wrapper } = await page()
    expect(wrapper.text()).toContain(message)
    expect(wrapper.find('h1').exists()).toBe(false)
    wrapper.unmount()
  })

  it('行程按天展示，进入地图后才算路；失败明确显示示意连线', async () => {
    const { wrapper } = await page()
    expect(wrapper.text()).toContain('杭州两日')
    expect(wrapper.findAll('.share-day')).toHaveLength(2)
    expect(requests.routes).not.toHaveBeenCalled()
    await wrapper.get('input[value="map"]').setValue()
    await flushPromises()
    expect(wrapper.text()).toContain('示意连线，不代表实际道路')
    expect(wrapper.text()).not.toContain('公里')
    expect(requests.routes).toHaveBeenCalledTimes(1)
    expect(wrapper.findComponent({ name: 'AmapView' }).props('paths')).toHaveLength(1)
    wrapper.unmount()
  })

  it('空行程与无定位项目提供空状态', async () => {
    requests.items.mockResolvedValue([item('A', null)])
    const { wrapper } = await page()
    await wrapper.get('input[value="map"]').setValue()
    expect(wrapper.text()).toContain('本次行程还没有定位信息')
    expect(wrapper.find('[data-map]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('同窗口换分享令牌重新加载，不能沿用旧旅行', async () => {
    const { router, wrapper } = await page()
    requests.trip.mockResolvedValue({ ...trip, name: '新的旅行' })
    await router.push('/s/second')
    await flushPromises()
    expect(requests.trip).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('新的旅行')
    expect(wrapper.text()).not.toContain('杭州两日')
    wrapper.unmount()
  })
})
