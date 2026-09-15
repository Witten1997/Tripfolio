import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

vi.mock('@/desktop/DesktopShell.vue', () => ({
  __esModule: true,
  default: { template: '<nav data-owner>账号导航</nav>' },
}))
vi.mock('@/mobile/MobileShell.vue', () => ({
  __esModule: true,
  default: { template: '<nav data-owner>移动账号导航</nav>' },
}))
vi.mock('@/share/ShareLayout.vue', () => ({
  __esModule: true,
  default: { template: '<main data-guest>访客</main>' },
}))

import App from '@/App.vue'

describe('分享布局隔离', () => {
  it.each(['desktop', 'mobile'] as const)('%s 首屏与导航均按路由分流', async (shell) => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/s/:token',
          component: { template: '<div />' },
          meta: { public: true, share: true },
        },
        { path: '/trips', component: { template: '<div />' } },
      ],
    })
    await router.push('/s/first')
    const wrapper = mount(App, { props: { shell, share: true }, global: { plugins: [router] } })
    await flushPromises()
    expect(wrapper.find('[data-owner]').exists()).toBe(false)
    expect(wrapper.find('[data-guest]').exists()).toBe(true)
    await router.push('/trips')
    await flushPromises()
    expect(wrapper.find('[data-owner]').exists()).toBe(true)
    expect(wrapper.find('[data-guest]').exists()).toBe(false)
    await router.push('/s/second')
    await flushPromises()
    expect(wrapper.find('[data-owner]').exists()).toBe(false)
    expect(wrapper.find('[data-guest]').exists()).toBe(true)
    wrapper.unmount()
  })
})
