import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

// 桌面旅行列表在移动壳里只作为内容复用：这里换成占位组件，
// 用例集中断言移动壳这层补出来的东西，列表自身的行为由桌面用例覆盖。
vi.mock('@/desktop/pages/TripListPage.vue', () => ({
  __esModule: true,
  default: { template: '<div data-desktop-list />' },
}))
vi.mock('@/platform', () => ({ platform: { kind: 'web', isNative: false } }))

import TripListPage from './TripListPage.vue'

async function page() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/trips', name: 'trips', component: TripListPage },
      { path: '/dev/local-db', name: 'dev-local-db', component: { template: '<div />' } },
    ],
  })
  await router.push('/trips')
  await router.isReady()
  return { router, wrapper: mount(TripListPage, { global: { plugins: [router] } }) }
}

describe('移动壳的旅行列表页', () => {
  it('内嵌桌面旅行列表，不重复实现列表与筛选', async () => {
    const { wrapper } = await page()
    expect(wrapper.find('[data-desktop-list]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('原生或开发模式下保留平台自检入口，点击进入自检页', async () => {
    const { router, wrapper } = await page()
    // Vant 的 Cell 带 to／is-link 时渲染成 role="button" 的 div（点击走 router.push），不是 <a href>。
    const entry = wrapper.get('[role="button"]')
    expect(entry.text()).toContain('本地数据库自检')
    await entry.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('dev-local-db')
    wrapper.unmount()
  })
})
