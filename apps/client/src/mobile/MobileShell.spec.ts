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

describe('移动壳顶栏', () => {
  it('用项目 Logo 图片替代文字品牌名', async () => {
    const { wrapper } = await shell('/trips')
    const logo = wrapper.get('.van-nav-bar__title img')
    expect(logo.attributes('alt')).toBe('Tripfolio')
    expect(logo.attributes('src')).toContain('logo.png')
    expect(wrapper.get('.van-nav-bar__title').text()).toBe('')
    wrapper.unmount()
  })

  it('登录后右上角给出主题中心、回收站与账号三个图标入口', async () => {
    useSessionStore().setAccessToken('token', 3600)
    const { wrapper } = await shell('/trips')
    expect(wrapper.get('[aria-label="主题中心"]').attributes('href')).toBe('/themes')
    expect(wrapper.get('[aria-label="回收站"]').attributes('href')).toBe('/recycle-bin')
    expect(wrapper.get('[aria-label="账号"]').attributes('href')).toBe('/account')
    wrapper.unmount()
  })

  it('未登录时只保留主题中心，隐藏回收站与账号', async () => {
    const { wrapper } = await shell('/themes')
    expect(wrapper.find('[aria-label="主题中心"]').exists()).toBe(true)
    expect(wrapper.find('[aria-label="回收站"]').exists()).toBe(false)
    expect(wrapper.find('[aria-label="账号"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('根页面不显示返回，进入子页面后可返回；无历史时退回旅行列表', async () => {
    useSessionStore().setAccessToken('token', 3600)
    const { router, wrapper } = await shell('/trips')
    expect(wrapper.find('.van-nav-bar__arrow').exists()).toBe(false)

    await router.push('/account')
    await flushPromises()
    expect(wrapper.find('.van-nav-bar__arrow').exists()).toBe(true)

    // 内存历史不带 back 游标，等同于从链接直接进入子页面，应退回旅行列表而不是留在原地。
    await wrapper.get('.van-nav-bar__left').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('trips')
    wrapper.unmount()
  })
})
