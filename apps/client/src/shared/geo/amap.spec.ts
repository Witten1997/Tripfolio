import { createPinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AmapView from '@/desktop/components/AmapView.vue'
import { amapServiceHost, cursorZoomCenter, loadAMap, type AmapNamespace } from '@/shared/geo/amap'

vi.mock('@/shared/geo/amap', async (original) => ({
  ...(await original<typeof import('@/shared/geo/amap')>()),
  loadAMap: vi.fn(),
}))

const created: Array<Record<string, unknown>> = []
const mapOptions: Array<Record<string, unknown>> = []
const destroy = vi.fn()
const fitView = vi.fn()
/** 组件交给 SDK 做像素→经纬度换算的容器像素点。 */
const pixels: Array<{ x: number; y: number }> = []
const geoOfPixel = vi.fn(() => ({ getLat: () => 40, getLng: () => 117 }))
let mapZoom = 12
const zoomAndCenter = vi.fn((zoom: number) => {
  mapZoom = zoom
})
class MapFake {
  constructor(_container: HTMLElement, options: Record<string, unknown>) {
    mapOptions.push(options)
  }
  add = vi.fn()
  remove = vi.fn()
  on = vi.fn()
  off = vi.fn()
  setFitView = fitView
  setZoomAndCenter = zoomAndCenter
  getCenter = () => ({ getLat: () => 39, getLng: () => 116 })
  getZoom = () => mapZoom
  containerToLngLat = geoOfPixel
  destroy = destroy
}
class OverlayFake {
  constructor(options: Record<string, unknown>) {
    created.push(options)
  }
  setMap = vi.fn()
}
class PixelFake {
  constructor(
    public x: number,
    public y: number,
  ) {
    pixels.push({ x, y })
  }
}
const sdk: AmapNamespace = {
  Map: MapFake,
  Marker: OverlayFake,
  Polyline: OverlayFake,
  Pixel: PixelFake,
}

beforeEach(() => {
  created.length = 0
  mapOptions.length = 0
  pixels.length = 0
  destroy.mockClear()
  fitView.mockClear()
  geoOfPixel.mockClear()
  mapZoom = 12
  zoomAndCenter.mockClear()
  vi.mocked(loadAMap).mockReset().mockResolvedValue(sdk)
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
})

describe('地图实例与安全边界', () => {
  it('代理只能是 HTTP(S) 的固定服务前缀，不接受凭证或注入查询', () => {
    expect(amapServiceHost(undefined, 'https://example.com')).toBe(
      'https://example.com/_AMapService',
    )
    expect(amapServiceHost('https://api.example.com/_AMapService/', 'https://localhost')).toBe(
      'https://api.example.com/_AMapService',
    )
    for (const host of [
      'javascript:alert(1)',
      'https://u:p@example.com/_AMapService',
      '/_AMapService?jscode=exposed',
      '/anything',
    ]) {
      expect(() => amapServiceHost(host, 'https://example.com')).toThrow()
    }
  })

  it('点位内容使用 DOM 文本与可访问按钮，卸载销毁地图', async () => {
    const title = '<img src=x onerror=alert(1)>'
    const view = mount(AmapView, {
      props: { points: [{ id: 'a', title, number: 1, latitude: 39, longitude: 116 }] },
      global: { plugins: [createPinia()] },
    })
    await flushPromises()
    const content = created.find((options) => options.content)?.content as HTMLButtonElement
    expect(content.tagName).toBe('BUTTON')
    expect(content.querySelector('img')).toBeNull()
    expect(content.getAttribute('aria-label')).toContain(title)
    content.click()
    expect(view.emitted('focusPoint')?.[0]).toEqual(['a'])
    expect(mapOptions[0]?.animateEnable).toBe(false)
    view.unmount()
    expect(destroy).toHaveBeenCalledOnce()
  })

  it('组件已离开时，迟到的 SDK 加载不能创建地图', async () => {
    let resolve!: (value: AmapNamespace) => void
    vi.mocked(loadAMap).mockImplementationOnce(
      () =>
        new Promise((r) => {
          resolve = r
        }),
    )
    const view = mount(AmapView, { global: { plugins: [createPinia()] } })
    view.unmount()
    resolve(sdk)
    await flushPromises()
    expect(mapOptions).toHaveLength(0)
  })

  it('总览与自动适配包含道路折线，迟到的绕行路线也不会被裁掉', async () => {
    const points = [
      { id: 'a', title: '起点', number: 1, latitude: 39, longitude: 116 },
      { id: 'b', title: '终点', number: 2, latitude: 39.01, longitude: 116.01 },
    ]
    const view = mount(AmapView, { props: { points }, global: { plugins: [createPinia()] } })
    await flushPromises()
    expect(fitView.mock.lastCall?.[0]).toHaveLength(2)
    await view.setProps({
      paths: [
        {
          id: 'a>b',
          kind: 'road',
          points: [points[0]!, { latitude: 39.5, longitude: 116.5 }, points[1]!],
        },
      ],
    })
    expect(fitView).toHaveBeenCalledTimes(2)
    expect(fitView.mock.lastCall?.[0]).toHaveLength(3)
    await view
      .findAll('button')
      .find((button) => button.text() === '总览')!
      .trigger('click')
    expect(fitView.mock.lastCall?.[0]).toHaveLength(3)
    view.unmount()
  })

  it('SDK 失败显示重试，成功重试后出现可访问的地图控制', async () => {
    vi.mocked(loadAMap).mockRejectedValueOnce(new Error('地图未能加载，请重试'))
    const view = mount(AmapView, { global: { plugins: [createPinia()] } })
    await flushPromises()
    expect(view.text()).toContain('重新加载地图')
    await view
      .findAll('button')
      .find((b) => b.text().includes('重新加载'))!
      .trigger('click')
    await flushPromises()
    expect(view.text()).toContain('总览')
    expect(view.find('button[aria-label="放大地图"]').exists()).toBe(false)
    view.unmount()
  })

  it('滚轮实际更新缩放级别，已加载地图区域阻止页面滚动但不截断传播', async () => {
    const view = mount(AmapView, { global: { plugins: [createPinia()] } })
    const canvas = view.find('.amap-canvas').element
    const receiveWheel = vi.fn()
    canvas.addEventListener('wheel', receiveWheel)
    const initial = new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: 100 })
    canvas.dispatchEvent(initial)
    expect(initial.defaultPrevented).toBe(false)
    expect(zoomAndCenter).not.toHaveBeenCalled()
    await flushPromises()
    expect(mapOptions[0]).toMatchObject({
      scrollWheel: false,
      zoomEnable: true,
      keyboardEnable: true,
      zooms: [2, 20],
    })
    const wheel = new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: -120 })
    canvas.dispatchEvent(wheel)
    expect(wheel.defaultPrevented).toBe(true)
    expect(zoomAndCenter).toHaveBeenLastCalledWith(13, [116, 39], true)
    // jsdom 里容器没有布局尺寸，退回按地图中心缩放。
    expect(geoOfPixel).not.toHaveBeenCalled()
    expect(receiveWheel).toHaveBeenCalledTimes(2)
    expect(view.findAll('.amap-controls button')).toHaveLength(2)
    view.unmount()
  })

  it('触控板保留细小步幅，行／页滚轮归一化，缩放不越过地图边界', async () => {
    const view = mount(AmapView, { global: { plugins: [createPinia()] } })
    await flushPromises()
    const canvas = view.find('.amap-canvas').element
    const wheel = (deltaY: number, deltaMode = 0) =>
      canvas.dispatchEvent(
        new WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY, deltaMode }),
      )
    wheel(-12)
    expect(mapZoom).toBeCloseTo(12.1)
    wheel(3, WheelEvent.DOM_DELTA_LINE)
    expect(mapZoom).toBeCloseTo(11.1)
    wheel(-1, WheelEvent.DOM_DELTA_PAGE)
    expect(mapZoom).toBeCloseTo(12.1)
    mapZoom = 19.8
    wheel(-120)
    expect(mapZoom).toBe(20)
    mapZoom = 2.2
    wheel(120)
    expect(mapZoom).toBe(2)
    zoomAndCenter.mockClear()
    wheel(0)
    expect(zoomAndCenter).not.toHaveBeenCalled()
    view.unmount()
  })

  it('滚轮以指针为锚点换算新中心，指针下的位置保持不动', async () => {
    const box = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      top: 0,
      width: 800,
      height: 600,
      right: 800,
      bottom: 600,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect)
    try {
      const view = mount(AmapView, { global: { plugins: [createPinia()] } })
      await flushPromises()
      pixels.length = 0
      const canvas = view.find('.amap-canvas').element
      // 容器中心 (400, 300)，指针在 (700, 300)：放大一级后中心应向指针移动一半，落在 (550, 300)。
      canvas.dispatchEvent(
        new WheelEvent('wheel', {
          bubbles: true,
          cancelable: true,
          deltaY: -120,
          clientX: 700,
          clientY: 300,
        }),
      )
      expect(pixels).toEqual([{ x: 550, y: 300 }])
      expect(zoomAndCenter).toHaveBeenLastCalledWith(13, [117, 40], true)
      view.unmount()
    } finally {
      box.mockRestore()
    }
  })

  it('已到缩放边界时不再重设视角，滚轮仍不带动页面滚动', async () => {
    const view = mount(AmapView, { global: { plugins: [createPinia()] } })
    await flushPromises()
    mapZoom = 20
    const wheel = new WheelEvent('wheel', {
      bubbles: true,
      cancelable: true,
      deltaY: -120,
      clientX: 700,
      clientY: 300,
    })
    view.find('.amap-canvas').element.dispatchEvent(wheel)
    expect(wheel.defaultPrevented).toBe(true)
    expect(zoomAndCenter).not.toHaveBeenCalled()
    view.unmount()
  })
})

describe('滚轮锚点换算', () => {
  const size = { width: 800, height: 600 }

  it('指针在中心或缩放倍数不变时，中心保持不动', () => {
    expect(cursorZoomCenter({ x: 400, y: 300 }, size, 1)).toEqual({ x: 400, y: 300 })
    expect(cursorZoomCenter({ x: 700, y: 100 }, size, 0)).toEqual({ x: 400, y: 300 })
  })

  it('放大一级中心向指针移动一半，缩小一级反向等距移动', () => {
    expect(cursorZoomCenter({ x: 700, y: 300 }, size, 1)).toEqual({ x: 550, y: 300 })
    expect(cursorZoomCenter({ x: 700, y: 300 }, size, -1)).toEqual({ x: 100, y: 300 })
    expect(cursorZoomCenter({ x: 700, y: 300 }, size, 0.1).x).toBeCloseTo(420.09, 1)
  })
})
