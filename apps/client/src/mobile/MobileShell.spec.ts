import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { useSessionStore } from '@/shared/stores/session'

import MobileShell from './MobileShell.vue'

let pinia = createPinia()

async function shell(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/trips', name: 'trips', component: { template: '<div data-page="trips" />' } },
      {
        path: '/trips/:tripId/itinerary',
        name: 'trip-itinerary',
        component: { template: '<div />' },
      },
      { path: '/themes', name: 'themes', component: { template: '<div />' } },
      { path: '/recycle-bin', name: 'recycle-bin', component: { template: '<div />' } },
      { path: '/account', name: 'account', component: { template: '<div />' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(MobileShell, { global: { plugins: [router, pinia] } })
  await flushPromises()
  return { router, wrapper }
}

beforeEach(() => {
  pinia = createPinia()
  setActivePinia(pinia)
})

describe('移动壳底栏', () => {
  it('登录后显示旅行、新建与我的三个入口，不显示顶栏', async () => {
    useSessionStore().setAccessToken('token', 3600)
    const { wrapper } = await shell('/trips')
    expect(wrapper.find('.van-nav-bar').exists()).toBe(false)
    expect(wrapper.get('[aria-label="旅行"]').attributes('href')).toBe('/trips')
    expect(wrapper.get('[aria-label="新建旅行"]').element.tagName).toBe('BUTTON')
    expect(wrapper.get('[aria-label="我的"]').attributes('href')).toBe('/account')
    wrapper.unmount()
  })

  it('未登录时隐藏业务底栏', async () => {
    const { wrapper } = await shell('/themes')
    expect(wrapper.find('.mobile-bottom-bar').exists()).toBe(false)
    wrapper.unmount()
  })

  it('旅行详情保持旅行入口高亮，我的页面高亮我的入口', async () => {
    useSessionStore().setAccessToken('token', 3600)
    const { router, wrapper } = await shell('/trips/1/itinerary')
    expect(wrapper.get('[aria-label="旅行"]').classes()).toContain('is-active')

    await router.push('/account')
    await flushPromises()
    expect(wrapper.get('[aria-label="我的"]').classes()).toContain('is-active')
    wrapper.unmount()
  })

  it('点击中间加号进入旅行列表并携带新建指令', async () => {
    useSessionStore().setAccessToken('token', 3600)
    const { router, wrapper } = await shell('/account')
    await wrapper.get('[aria-label="新建旅行"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('trips')
    expect(router.currentRoute.value.query.create).toBeTypeOf('string')
    wrapper.unmount()
  })
})
