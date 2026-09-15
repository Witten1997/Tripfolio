import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import ActionIcon from './ActionIcon.vue'
import IconAction from './IconAction.vue'
import { actionIcons, type ActionIconName } from './actionIcons'

describe('图标操作', () => {
  it.each(Object.keys(actionIcons) as ActionIconName[])(
    '%s 使用同一套 24px 网格并以 20px 显示，不带可见文字或独立颜色',
    (name) => {
      const view = mount(ActionIcon, { props: { name } })
      const svg = view.get('svg')
      expect(svg.attributes('viewBox')).toBe('0 0 24 24')
      expect(svg.attributes('width')).toBe('20')
      expect(svg.attributes('height')).toBe('20')
      expect(svg.attributes('stroke')).toBe('currentColor')
      expect(svg.attributes('stroke-width')).toBe('1.5')
      expect(svg.attributes('aria-hidden')).toBe('true')
      expect(svg.text()).toBe('')
      expect(view.findComponent(actionIcons[name]).exists()).toBe(true)
      expect(view.findAll('path, circle, line, polyline, rect').length).toBeGreaterThan(0)
      view.unmount()
    },
  )

  it('动作使用有名称的原生按钮，装饰图标不重复朗读，属性与事件透传', async () => {
    const click = vi.fn()
    const view = mount(IconAction, {
      props: { label: '刷新行程', icon: 'refresh' },
      attrs: { onClick: click, 'data-action': 'refresh' },
    })
    const button = view.get('button')
    expect(button.attributes('type')).toBe('button')
    expect(button.attributes('aria-label')).toBe('刷新行程')
    expect(button.attributes('data-action')).toBe('refresh')
    expect(button.text()).toBe('')
    expect(button.get('svg').attributes('aria-hidden')).toBe('true')
    expect(button.get('svg').attributes('focusable')).toBe('false')
    await button.trigger('click')
    expect(click).toHaveBeenCalledTimes(1)
    view.unmount()
  })

  it('加载及禁用状态阻止重复动作，加载前后名称不变', async () => {
    const click = vi.fn()
    const view = mount(IconAction, {
      props: { label: '刷新账单', icon: 'refresh', loading: true },
      attrs: { onClick: click },
    })
    const button = view.get('button')
    expect((button.element as HTMLButtonElement).disabled).toBe(true)
    expect(button.attributes('aria-label')).toBe('刷新账单')
    expect(button.attributes('aria-busy')).toBe('true')
    expect(view.getComponent(ActionIcon).props('name')).toBe('loading')
    await button.trigger('click')
    expect(click).not.toHaveBeenCalled()
    await view.setProps({ loading: false, disabled: true })
    await button.trigger('click')
    expect(click).not.toHaveBeenCalled()
    await view.setProps({ disabled: false })
    expect(button.attributes('aria-label')).toBe('刷新账单')
    expect(button.attributes('aria-busy')).toBeUndefined()
    await button.trigger('click')
    expect(click).toHaveBeenCalledTimes(1)
    view.unmount()
  })

  it('导航保持真实链接及 Ctrl 点击行为，不嵌套按钮', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/trips', component: { template: '<div />' } },
        { path: '/themes', component: { template: '<div />' } },
      ],
    })
    await router.push('/trips')
    await router.isReady()
    const view = mount(IconAction, {
      props: { to: '/themes', label: '主题', icon: 'theme', variant: 'navigation' },
      global: { plugins: [router] },
    })
    const link = view.get('a')
    expect(link.attributes('href')).toBe('/themes')
    expect(link.attributes('aria-label')).toBe('主题')
    expect(link.classes()).toContain('icon-action--navigation')
    expect(view.find('button').exists()).toBe(false)
    let navigationPrevented = true
    // 在 RouterLink 处理之后记录默认行为，再阻止 jsdom 执行它尚未实现的新文档导航。
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
    expect(router.currentRoute.value.path).toBe('/trips')
    await link.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/themes')
    expect(link.attributes('aria-current')).toBe('page')
    view.unmount()
  })

  it.each(['Enter', 'NumpadEnter', 'Space'])('提示层不会拦截按钮的 %s 激活键', (code) => {
    const view = mount(IconAction, { props: { label: '编辑旅行', icon: 'edit' } })
    const event = new KeyboardEvent('keydown', {
      key: code === 'Space' ? ' ' : 'Enter',
      code,
      bubbles: true,
      cancelable: true,
    })
    view.get('button').element.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
    view.unmount()
  })

  it('地图入口采用按钮外观，仍通过真实链接保留旅行与出行方式', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/trips', component: { template: '<div />' } },
        { path: '/trips/:tripId/map', name: 'trip-map', component: { template: '<div />' } },
      ],
    })
    await router.push('/trips')
    await router.isReady()
    const view = mount(IconAction, {
      props: {
        icon: 'globe',
        label: '地图视图',
        to: { name: 'trip-map', params: { tripId: 'trip-1' }, query: { mode: 'walking' } },
      },
      global: { plugins: [router] },
    })
    const link = view.get('a')
    expect(link.attributes('href')).toBe('/trips/trip-1/map?mode=walking')
    expect(link.attributes('aria-label')).toBe('地图视图')
    expect(link.classes()).toContain('icon-action--link')
    expect(link.classes()).not.toContain('icon-action--navigation')
    expect(link.text()).toBe('')
    expect(view.find('button').exists()).toBe(false)
    await link.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.params.tripId).toBe('trip-1')
    expect(router.currentRoute.value.query.mode).toBe('walking')
    view.unmount()
  })

  it('删除图标透传危险操作样式与点击，加载后不能重复触发', async () => {
    const click = vi.fn()
    const view = mount(IconAction, {
      props: { icon: 'trash', label: '删除待办：订机票' },
      attrs: { type: 'danger', text: true, onClick: click },
    })
    const button = view.get('button')
    expect(button.classes()).toContain('el-button--danger')
    expect(button.classes()).toContain('is-text')
    expect(button.attributes('aria-label')).toBe('删除待办：订机票')
    await button.trigger('click')
    expect(click).toHaveBeenCalledTimes(1)
    await view.setProps({ loading: true })
    await button.trigger('click')
    expect(click).toHaveBeenCalledTimes(1)
    expect(button.attributes('aria-busy')).toBe('true')
    expect(button.attributes('aria-label')).toBe('删除待办：订机票')
    view.unmount()
  })
})
