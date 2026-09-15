import { ElCheckbox, ElSelect } from 'element-plus'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import PackingTab from './PackingTab.vue'
import { listAllPackingItems, updatePackingItem, type PackingItem } from '@/shared/api/packing'

vi.mock('@/shared/travel/tripContext', () => ({ useTripContext: () => ({ tripId: 'trip' }) }))
vi.mock('@/shared/api/packing', async (original) => ({
  ...(await original<typeof import('@/shared/api/packing')>()),
  listAllPackingItems: vi.fn(),
  updatePackingItem: vi.fn(),
}))

const items = ['packed', 'ready', 'pending'].map((status, index) => ({
  id: String(index),
  trip_id: 'trip',
  name: ['护照', '相机', '充电器'][index],
  category: 'electronics',
  status,
  quantity: 1,
  notes: '',
  version: '1',
})) as PackingItem[]
beforeEach(() => {
  vi.mocked(listAllPackingItems).mockReset().mockResolvedValue(items)
  vi.mocked(updatePackingItem)
    .mockReset()
    .mockImplementation(
      async (_trip, id, _version, patch) =>
        ({
          resource: { ...items.find((item) => item.id === id)!, ...patch, version: '2' },
          result: { warnings: [] },
        }) as never,
    )
})
async function setup(attach = false) {
  const view = mount(PackingTab, {
    ...(attach ? { attachTo: document.body } : {}),
    global: { stubs: { PackingItemDialog: true, PackingLibraryDialog: true } },
  })
  await flushPromises()
  return view
}

describe('两态行李清单', () => {
  it('历史三态以两个准备状态展示、筛选和统计，圆框点击提交 ready 或 pending', async () => {
    const view = await setup()
    expect(view.text()).toContain('已准备 2 / 3')
    expect(view.get('[role="progressbar"]').attributes('aria-label')).toBe('行李准备进度')
    expect(view.get('[role="progressbar"]').attributes('aria-valuetext')).toBe('已准备 2 / 3 件')
    expect(view.text()).not.toContain('已装包')
    expect(view.text()).not.toContain('已备齐')
    const checks = view.findAllComponents(ElCheckbox)
    expect(checks.map((check) => check.props('modelValue'))).toEqual([true, true, false])
    await checks[2]!.get('input').setValue(true)
    await flushPromises()
    expect(updatePackingItem).toHaveBeenLastCalledWith(
      'trip',
      '2',
      '1',
      { status: 'ready' },
      expect.any(String),
    )
    expect(view.text()).toContain('已准备 3 / 3')
    expect(view.get('[role="progressbar"]').attributes('aria-valuetext')).toBe('已准备 3 / 3 件')
    await checks[0]!.get('input').setValue(false)
    await flushPromises()
    expect(updatePackingItem).toHaveBeenLastCalledWith(
      'trip',
      '0',
      '1',
      { status: 'pending' },
      expect.any(String),
    )
    const status =
      view
        .findAllComponents(ElSelect)
        .find((select) => select.attributes('aria-label') === '状态') ??
      view.findAllComponents(ElSelect)[1]!
    status.vm.$emit('update:modelValue', 'ready')
    await flushPromises()
    expect(view.findAll('.item')).toHaveLength(2)
    view.unmount()
  })

  it('保存过程中禁用勾选，失败保留原状态和重试入口', async () => {
    let fail!: (error: Error) => void
    vi.mocked(updatePackingItem).mockImplementationOnce(
      () =>
        new Promise((_resolve, reject) => {
          fail = reject
        }),
    )
    const view = await setup(true)
    const input = view.findAllComponents(ElCheckbox)[2]!.get('input')
    ;(input.element as HTMLInputElement).focus()
    await input.setValue(true)
    ;(input.element as HTMLInputElement).blur()
    expect(view.findAllComponents(ElCheckbox).every((check) => check.props('disabled'))).toBe(true)
    expect(view.findAll('.item')[2]!.attributes('aria-busy')).toBe('true')
    fail(new Error('断网'))
    await flushPromises()
    const checkbox = view.findAllComponents(ElCheckbox)[2]!
    expect(checkbox.props('modelValue')).toBe(false)
    expect((checkbox.get('input').element as HTMLInputElement).checked).toBe(false)
    expect(checkbox.props('disabled')).toBe(false)
    expect(document.activeElement).toBe(input.element)
    expect(view.text()).toContain('结果尚未确认')
    view.unmount()
  })

  it('筛选使已准备项消失时将焦点移到下一个物品，不抢走用户主动移开的焦点', async () => {
    const view = await setup(true)
    const filter = view.findAllComponents(ElSelect)[1]!
    filter.vm.$emit('update:modelValue', 'ready')
    await flushPromises()
    const input = view.findAllComponents(ElCheckbox)[0]!.get('input')
    ;(input.element as HTMLInputElement).focus()
    await input.setValue(false)
    await flushPromises()
    expect(document.activeElement).toBe(view.findComponent(ElCheckbox).get('input').element)
    view.unmount()

    let finish!: () => void
    const pending = new Promise<void>((resolve) => {
      finish = resolve
    })
    vi.mocked(updatePackingItem).mockImplementationOnce(async (_trip, id, _version, patch) => {
      await pending
      return {
        resource: { ...items.find((item) => item.id === id)!, ...patch, version: '2' },
        result: { warnings: [] },
      } as never
    })
    const second = await setup(true)
    const check = second.findAllComponents(ElCheckbox)[2]!.get('input')
    ;(check.element as HTMLInputElement).focus()
    await check.setValue(true)
    const state = second.findAllComponents(ElSelect)[1]!.get('input')
    ;(state.element as HTMLInputElement).focus()
    finish()
    await flushPromises()
    expect(document.activeElement).toBe(state.element)
    second.unmount()
  })
})
