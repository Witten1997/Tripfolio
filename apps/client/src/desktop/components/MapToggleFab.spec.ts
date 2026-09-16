import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import source from './MapToggleFab.vue?raw'
import ActionIcon from './ActionIcon.vue'
import MapToggleFab from './MapToggleFab.vue'

function tripRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/trips/:tripId/itinerary',
        name: 'trip-itinerary',
        component: { template: '<div />' },
      },
      { path: '/trips/:tripId/map', name: 'trip-map', component: { template: '<div />' } },
    ],
  })
}

describe('地图切换悬浮按钮', () => {
  it('按钮形态随视图反转：行程时看地图，地图时看行程', async () => {
    const view = mount(MapToggleFab, { props: { onMap: false } })
    const button = view.get('button')
    expect(button.attributes('type')).toBe('button')
    expect(button.attributes('aria-label')).toBe('看地图')
    expect(button.text()).toBe('看地图')
    expect(view.getComponent(ActionIcon).props('name')).toBe('map')
    await button.trigger('click')
    expect(view.emitted('toggle')).toHaveLength(1)

    await view.setProps({ onMap: true })
    expect(view.get('button').attributes('aria-label')).toBe('看行程')
    expect(view.getComponent(ActionIcon).props('name')).toBe('list')
    view.unmount()
  })

  it('主人侧渲染为真实链接，可中键／Ctrl 点击在新标签打开，且不嵌套按钮', async () => {
    const router = tripRouter()
    await router.push('/trips/t1/itinerary')
    await router.isReady()
    const view = mount(MapToggleFab, {
      props: { onMap: false, to: { name: 'trip-map', params: { tripId: 't1' } } },
      global: { plugins: [router] },
    })
    const link = view.get('a')
    expect(link.attributes('href')).toBe('/trips/t1/map')
    expect(link.attributes('aria-label')).toBe('看地图')
    expect(view.find('button').exists()).toBe(false)
    let navigationPrevented = true
    link.element.addEventListener(
      'click',
      (event) => {
        navigationPrevented = event.defaultPrevented
        event.preventDefault()
      },
      { once: true },
    )
    await link.trigger('click', { ctrlKey: true })
    await flushPromises()
    expect(navigationPrevented).toBe(false)

    await view.setProps({ onMap: true, to: { name: 'trip-itinerary', params: { tripId: 't1' } } })
    expect(view.get('a').attributes('href')).toBe('/trips/t1/itinerary')
    expect(view.get('a').attributes('aria-label')).toBe('看行程')
    await view.get('a').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('trip-itinerary')
    view.unmount()
  })

  it('固定在右下角并避开手机安全区，同时为最后一屏留出流内空白', () => {
    const style = source.match(/<style[^>]*>([\s\S]*?)<\/style>/)?.[1] ?? ''
    expect(style).toMatch(/position: fixed/)
    expect(style).toMatch(/right: 16px/)
    expect(style).toMatch(
      /bottom: calc\(var\(--tf-map-toggle-bottom, 16px\) \+ env\(safe-area-inset-bottom/,
    )
    expect(style).toMatch(/min-height: var\(--tf-control-size\)/)
    expect(style).toMatch(/\.map-toggle__space/)
  })
})
