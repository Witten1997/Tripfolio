import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AmapView from '@/desktop/components/AmapView.vue'
import PlacePicker from '@/desktop/components/PlacePicker.vue'
import { reverseGeocode, searchPlaces, type GeoPlace } from '@/shared/api/geo'

vi.mock('@/shared/api/geo', () => ({ reverseGeocode: vi.fn(), searchPlaces: vi.fn() }))

const coordinate = { latitude: 39.918058, longitude: 116.397026 }
const place: GeoPlace = {
  ...coordinate,
  name: '故宫',
  address: '景山前街',
  provider: 'amap',
  poi_id: null,
  adcode: null,
}
const empty = () => ({ place_name: '', address: '', latitude: null, longitude: null })
beforeEach(() => {
  vi.mocked(reverseGeocode).mockReset().mockResolvedValue(place)
  vi.mocked(searchPlaces).mockReset().mockResolvedValue([place])
})

async function setup() {
  const view = mount(PlacePicker, {
    props: { value: empty(), city: '北京' },
    global: { stubs: { AmapView: true } },
  })
  await view
    .findAll('button')
    .find((button) => button.text() === '地图选点')!
    .trigger('click')
  return view
}

describe('行程地点选择', () => {
  it('坐标输入校验后查询，选点等待期间向表单报告忙状态', async () => {
    const view = await setup()
    await view.find('input[id$="-lng"]').setValue('181')
    await view.find('input[id$="-lat"]').setValue('39')
    const useCoordinate = view.findAll('button').find((button) => button.text() === '使用坐标')!
    await useCoordinate.trigger('click')
    expect(reverseGeocode).not.toHaveBeenCalled()
    expect(view.text()).toContain('请输入有效坐标')
    await view.find('input[id$="-lng"]').setValue('116.3970261')
    await view.find('input[id$="-lat"]').setValue('39.9180581')
    await useCoordinate.trigger('click')
    await flushPromises()
    expect(reverseGeocode).toHaveBeenCalledWith(coordinate, expect.any(AbortSignal))
    expect(view.emitted('select')?.[0]).toEqual([place])
    expect(view.emitted('locating')).toEqual([[true], [false]])
    view.unmount()
  })

  it('逆地理编码失败也保留本次点击坐标，不伪造地址', async () => {
    vi.mocked(reverseGeocode).mockRejectedValueOnce(new Error('服务不可用'))
    const view = await setup()
    view.findComponent(AmapView).vm.$emit('choose', coordinate)
    await flushPromises()
    expect(view.emitted('select')?.[0]).toEqual([
      {
        ...coordinate,
        name: '地图选点',
        address: '',
        adcode: null,
        poi_id: null,
        provider: 'amap',
      },
    ])
    expect(view.text()).toContain('已保留选点位置')
    expect(view.emitted('locating')?.at(-1)).toEqual([false])
    view.unmount()
  })

  it('地点被改写后取消逆地理编码，迟到结果不能覆盖新地点', async () => {
    let complete!: (place: GeoPlace) => void
    vi.mocked(reverseGeocode).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          complete = resolve
        }),
    )
    const view = await setup()
    view.findComponent(AmapView).vm.$emit('choose', coordinate)
    const signal = vi.mocked(reverseGeocode).mock.calls[0]![1]
    await view.setProps({
      value: { place_name: '另一个地点', address: '', latitude: null, longitude: null },
    })
    expect(signal?.aborted).toBe(true)
    complete(place)
    await flushPromises()
    expect(view.emitted('select')).toBeUndefined()
    expect(view.emitted('locating')?.at(-1)).toEqual([false])
    view.unmount()
  })

  it('表单禁用时不允许地图事件修改选点', async () => {
    const view = await setup()
    await view.setProps({ disabled: true })
    view.findComponent(AmapView).vm.$emit('choose', coordinate)
    await flushPromises()
    expect(reverseGeocode).not.toHaveBeenCalled()
    expect(view.emitted('select')).toBeUndefined()
    view.unmount()
  })
})
