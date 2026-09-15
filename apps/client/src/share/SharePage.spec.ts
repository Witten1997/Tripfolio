import { flushPromises, mount } from '@vue/test-utils'
import { ElSelect } from 'element-plus'
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
const item = (id: string, latitude: number | null, place = '西湖') => ({
  id,
  scheduled_on: '2026-10-01',
  sort_order: id === 'A' ? 0 : 1,
  title: id,
  kind: 'attraction',
  place_name: place,
  address: '湖滨',
  latitude,
  longitude: latitude === null ? null : 120.1,
  planned_start_local: null,
  planned_end_local: null,
  planned_duration_minutes: null,
})
const failure = (status: number, code: string) =>
  new ApiError({ type: 'about:blank', title: code, status, code, request_id: 'test' })

/** A → B 的整趟算路结果：131.2 公里、2 小时 3 分钟。 */
const drivingRoutes = {
  mode: 'driving' as const,
  failed_leg_count: 0,
  unlocated_item_count: 0,
  legs: [
    {
      from_item_id: 'A',
      to_item_id: 'B',
      distance_meters: 131200,
      duration_seconds: 7380,
      path: [
        { latitude: 30.1, longitude: 120.1 },
        { latitude: 30.2, longitude: 120.1 },
      ],
    },
  ],
}
const walkingRoutes = {
  ...drivingRoutes,
  mode: 'walking' as const,
  legs: [{ ...drivingRoutes.legs[0]!, distance_meters: 980, duration_seconds: 720 }],
}

beforeEach(() => {
  vi.clearAllMocks()
  requests.trip.mockResolvedValue(trip)
  requests.items.mockResolvedValue([item('A', 30.1), item('B', 30.2)])
  requests.routes.mockResolvedValue(drivingRoutes)
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

  it('行程按天展示站间距离，与主人侧同一套文案', async () => {
    const { wrapper } = await page()
    expect(wrapper.text()).toContain('杭州两日')
    expect(wrapper.findAll('.share-day')).toHaveLength(2)
    // 拿到行程即算路，不再等访客切到地图视图。
    expect(requests.routes).toHaveBeenCalledTimes(1)
    expect(requests.routes).toHaveBeenCalledWith(expect.anything(), 'driving')
    expect(wrapper.text()).toContain('相邻地点路程')
    expect(wrapper.text()).toContain('下一站 · 西湖')
    expect(wrapper.text()).toContain('驾车 131.2 公里 · 2 小时 3 分钟')
    wrapper.unmount()
  })

  it('切换出行方式重算并更新站间距离', async () => {
    requests.routes.mockResolvedValueOnce(drivingRoutes).mockResolvedValueOnce(walkingRoutes)
    const { wrapper } = await page()
    expect(wrapper.text()).toContain('驾车 131.2 公里 · 2 小时 3 分钟')
    await wrapper.get('input[value="walking"]').setValue()
    await flushPromises()
    expect(requests.routes).toHaveBeenCalledTimes(2)
    expect(requests.routes).toHaveBeenLastCalledWith(expect.anything(), 'walking')
    expect(wrapper.text()).toContain('步行 980 米 · 12 分钟')
    wrapper.unmount()
  })

  it('地图视图给出沿途各站与每段距离，点击站点在地图上定位', async () => {
    const { wrapper } = await page()
    await wrapper.get('input[value="map"]').setValue()
    await flushPromises()
    expect(wrapper.text()).toContain('沿途各站')
    expect(wrapper.text()).toContain('2 个地点 · 按行程顺序')
    expect(wrapper.text()).toContain('驾车 131.2 公里 · 2 小时 3 分钟')
    expect(wrapper.text()).toContain('131.2 公里')
    expect(wrapper.findAll('.share-stop')).toHaveLength(2)
    expect(wrapper.findComponent({ name: 'AmapView' }).props('paths')).toHaveLength(1)
    wrapper.unmount()
  })

  it('跨日接续与中间未定位项目在行程与地图两个视图都标注', async () => {
    requests.items.mockResolvedValue([
      item('A', 30.1, '西湖'),
      item('B', null, '服务区'),
      { ...item('C', 30.3, '千岛湖'), scheduled_on: '2026-10-02' },
    ])
    requests.routes.mockResolvedValue({
      ...drivingRoutes,
      unlocated_item_count: 1,
      legs: [{ ...drivingRoutes.legs[0]!, from_item_id: 'A', to_item_id: 'C' }],
    })
    const { wrapper } = await page()
    expect(wrapper.text()).toContain('下一站 · 千岛湖')
    expect(wrapper.text()).toContain('（跨日接续）')
    expect(wrapper.text()).toContain('（中间 1 项尚未定位）')
    await wrapper.get('input[value="map"]').setValue()
    await flushPromises()
    expect(wrapper.text()).toContain('跨日接续')
    expect(wrapper.text()).toContain('中间 1 项尚未定位')
    wrapper.unmount()
  })

  it('算路失败时行程保留顺序并说明不可用，地图降级为示意连线', async () => {
    requests.routes.mockRejectedValue(failure(503, 'DEPENDENCY_UNAVAILABLE'))
    const { wrapper } = await page()
    expect(wrapper.text()).toContain('路线服务暂不可用')
    expect(wrapper.text()).not.toContain('公里')
    expect(wrapper.findAll('.share-day')).toHaveLength(2)
    await wrapper.get('input[value="map"]').setValue()
    await flushPromises()
    expect(wrapper.text()).toContain('示意连线，不代表实际道路')
    expect(wrapper.text()).not.toContain('公里')
    expect(wrapper.findComponent({ name: 'AmapView' }).props('paths')).toHaveLength(1)
    wrapper.unmount()
  })

  it('地图按日期筛选：单日只显示该天站点，跨日路段消失且不额外请求算路', async () => {
    requests.items.mockResolvedValue([
      item('A', 30.1, '西湖'),
      { ...item('B', 30.2, '千岛湖'), scheduled_on: '2026-10-02' },
    ])
    requests.routes.mockResolvedValue({
      ...drivingRoutes,
      unlocated_item_count: 0,
      legs: [
        {
          from_item_id: 'A',
          to_item_id: 'B',
          distance_meters: 131200,
          duration_seconds: 7380,
          path: [
            { latitude: 30.1, longitude: 120.1 },
            { latitude: 30.2, longitude: 120.1 },
          ],
        },
      ],
    })
    const { wrapper } = await page()
    await wrapper.get('input[value="map"]').setValue()
    await flushPromises()
    const select = wrapper.findComponent(ElSelect)
    expect(select.props('modelValue')).toBe('all')
    expect(wrapper.findAll('.share-stop')).toHaveLength(2)
    expect(wrapper.text()).toContain('跨日接续')
    expect(wrapper.text()).toContain('2 / 2 站')

    select.vm.$emit('update:modelValue', '2026-10-02')
    await flushPromises()
    expect(wrapper.findAll('.share-stop')).toHaveLength(1)
    expect(wrapper.text()).toContain('1 / 1 站')
    expect(wrapper.text()).not.toContain('跨日接续')
    // 整趟路段服务端一次返回，按天筛选在本地完成。
    expect(requests.routes).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('右下角悬浮按钮在行程与地图之间切换，标签随之反转', async () => {
    const { wrapper } = await page()
    expect(wrapper.find('.share-days').exists()).toBe(true)
    expect(wrapper.get('.map-toggle').attributes('aria-label')).toBe('看地图')
    await wrapper.get('.map-toggle').trigger('click')
    await flushPromises()
    expect(wrapper.find('.share-days').exists()).toBe(false)
    expect(wrapper.text()).toContain('沿途各站')
    expect(wrapper.get('.map-toggle').attributes('aria-label')).toBe('看行程')
    await wrapper.get('.map-toggle').trigger('click')
    await flushPromises()
    expect(wrapper.find('.share-days').exists()).toBe(true)
    // 顶部切换仍然可用，两个入口共享同一个视图状态。
    await wrapper.get('input[value="map"]').setValue()
    await flushPromises()
    expect(wrapper.get('.map-toggle').attributes('aria-label')).toBe('看行程')
    wrapper.unmount()
  })

  it('空行程与无定位项目提供空状态', async () => {
    requests.items.mockResolvedValue([item('A', null)])
    const { wrapper } = await page()
    expect(requests.routes).not.toHaveBeenCalled()
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
