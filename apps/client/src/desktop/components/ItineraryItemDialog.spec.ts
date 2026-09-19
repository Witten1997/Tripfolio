import { ElDatePicker, ElFormItem, ElMessageBox, ElSelect } from 'element-plus'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ItineraryItemDialog from './ItineraryItemDialog.vue'
import LedgerEntryDialog from './LedgerEntryDialog.vue'
import PlacePicker from './PlacePicker.vue'
import { listCategories } from '@/shared/api/categories'
import { createItineraryItem } from '@/shared/api/itinerary'
import { createLedgerEntry } from '@/shared/api/ledger'

const context = {
  tripId: 'trip',
  trip: ref({ currency_code: 'CNY', timezone: 'Asia/Shanghai', destination: '北京' }),
  today: ref('2026-09-15'),
  minorUnits: ref(2),
  reload: vi.fn(),
}
vi.mock('@/shared/travel/tripContext', () => ({ useTripContext: () => context }))
vi.mock('@/shared/api/categories', async (original) => ({
  ...(await original<typeof import('@/shared/api/categories')>()),
  listCategories: vi.fn(),
}))
vi.mock('@/shared/api/itinerary', async (original) => ({
  ...(await original<typeof import('@/shared/api/itinerary')>()),
  createItineraryItem: vi.fn(),
}))
vi.mock('@/shared/api/ledger', async (original) => ({
  ...(await original<typeof import('@/shared/api/ledger')>()),
  createLedgerEntry: vi.fn(),
}))
vi.mock('@/shared/api/members', async (original) => ({
  ...(await original<typeof import('@/shared/api/members')>()),
  listTripMembers: vi.fn(async () => [
    {
      id: 'm-self',
      trip_id: 'trip',
      name: '我',
      share_percent: '100',
      sort_order: 0,
      is_self: true,
      version: '1',
      created_at: '2026-09-01T00:00:00Z',
      updated_at: '2026-09-01T00:00:00Z',
      deleted_at: null,
    },
  ]),
}))

const Dialog = defineComponent({
  props: ['modelValue', 'title'],
  template:
    '<section v-if="modelValue" role="dialog" :aria-label="title"><slot/><footer><slot name="footer"/></footer></section>',
})
const place = {
  name: '景山公园',
  address: '景山前街',
  latitude: 39.93,
  longitude: 116.397,
  poi_id: null,
  adcode: null,
  provider: 'amap',
}
const categories = [{ id: 'cat', name: '景点', sort_order: 1, version: '1' }]
const views: VueWrapper[] = []
beforeEach(() => {
  vi.mocked(listCategories)
    .mockReset()
    .mockResolvedValue(categories as never)
  vi.mocked(createItineraryItem)
    .mockReset()
    .mockResolvedValue({ resource: null, result: { warnings: [] } } as never)
  vi.mocked(createLedgerEntry)
    .mockReset()
    .mockResolvedValue({ resource: null, result: { warnings: [] } } as never)
  context.reload.mockReset().mockResolvedValue(undefined)
  vi.spyOn(ElMessageBox, 'confirm').mockResolvedValue('confirm' as never)
})
afterEach(() => {
  views.forEach((view) => view.unmount())
  views.length = 0
  vi.restoreAllMocks()
})
async function setup() {
  const view = mount(ItineraryItemDialog, {
    attachTo: document.body,
    global: { stubs: { ElDialog: Dialog, AmapView: true } },
  })
  views.push(view)
  await view.vm.open(undefined, '2026-10-02')
  await flushPromises()
  return view
}
const button = (view: Pick<VueWrapper, 'findAll'>, label: string) =>
  view.findAll('button').find((item) => item.text() === label)!
const field = (view: Pick<VueWrapper, 'findAllComponents'>, label: string) =>
  view.findAllComponents(ElFormItem).find((item) => item.props('label') === label)!

describe('行程选点、详情与独立记账', () => {
  it('没有手工标题／地址／费用字段，选点在类型之后、计划时间之前，标题自动随 POI 更新', async () => {
    const view = await setup()
    const labels = view.findAllComponents(ElFormItem).map((item) => item.props('label'))
    expect(labels).not.toContain('标题')
    expect(labels).not.toContain('地点')
    expect(labels).not.toContain('地址')
    expect(labels).not.toContain('预计费用')
    expect(view.html().indexOf('搜索地点')).toBeGreaterThan(view.html().indexOf('所属日期'))
    expect(view.html().indexOf('搜索地点')).toBeLessThan(view.html().indexOf('计划时间'))
    const picker = view.getComponent(PlacePicker)
    picker.vm.$emit('select', { ...place, name: '旧地点' })
    picker.vm.$emit('select', place)
    await button(view, '添加行程').trigger('click')
    await flushPromises()
    expect(createItineraryItem).toHaveBeenCalledWith(
      'trip',
      expect.objectContaining({
        title: '景山公园',
        place_name: '景山公园',
        address: '景山前街',
        scheduled_on: '2026-10-02',
        estimated_amount: null,
      }),
      expect.any(String),
    )
  })

  it('缺少选点时在搜索区域显示错误且聚焦搜索，不向后台创建空记录', async () => {
    const view = await setup()
    await button(view, '添加行程').trigger('click')
    await flushPromises()
    expect(createItineraryItem).not.toHaveBeenCalled()
    expect(view.get('.place-error').text()).toContain('选点')
    expect(view.get('input[id$="-keyword"]').attributes('aria-invalid')).toBe('true')
    expect(document.activeElement).toBe(view.get('input[id$="-keyword"]').element)
  })

  it('详情默认折叠，展开与收起保留内容，隐藏字段校验失败时自动展开', async () => {
    const view = await setup()
    view.getComponent(PlacePicker).vm.$emit('select', place)
    const expand = button(view, '展开详情')
    expect(expand.attributes('aria-expanded')).toBe('false')
    expect(field(view, '备注').isVisible()).toBe(false)
    await expand.trigger('click')
    await field(view, '备注').get('textarea').setValue('看日落')
    field(view, '实际开始')
      .getComponent(ElDatePicker)
      .vm.$emit('update:modelValue', '2026-10-02 12:00')
    field(view, '实际结束')
      .getComponent(ElDatePicker)
      .vm.$emit('update:modelValue', '2026-10-02 10:00')
    await button(view, '收起详情').trigger('click')
    expect(field(view, '备注').isVisible()).toBe(false)
    await button(view, '添加行程').trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(field(view, '实际结束').text()).toContain('不能早于'))
    expect(field(view, '备注').isVisible()).toBe(true)
    expect((field(view, '备注').get('textarea').element as HTMLTextAreaElement).value).toBe(
      '看日落',
    )
    expect(button(view, '收起详情').attributes('aria-expanded')).toBe('true')
  })

  it('加号复用记账表单并预填地点／所属日期，确认账单不会顺带创建行程', async () => {
    const view = await setup()
    view.getComponent(PlacePicker).vm.$emit('select', place)
    await view.get('button[aria-label="记录该地点花费"]').trigger('click')
    await flushPromises()
    const ledger = view.getComponent(LedgerEntryDialog)
    expect(field(ledger, '实际日期').getComponent(ElDatePicker).props('modelValue')).toBe(
      '2026-10-02',
    )
    expect((field(ledger, '备注').get('textarea').element as HTMLTextAreaElement).value).toBe(
      '景山公园',
    )
    await field(ledger, '金额').get('input').setValue('12.50')
    field(ledger, '账单分类').getComponent(ElSelect).vm.$emit('update:modelValue', 'cat')
    await button(ledger, '保存').trigger('click')
    await flushPromises()
    expect(createLedgerEntry).toHaveBeenCalledWith(
      'trip',
      expect.objectContaining({
        notes: '景山公园',
        occurred_on: '2026-10-02',
        amount: '12.50',
        category_id: 'cat',
        kind: 'expense',
        payer_member_id: 'm-self',
        split_mode: 'even',
        participant_member_ids: ['m-self'],
      }),
      expect.any(String),
    )
    expect(createItineraryItem).not.toHaveBeenCalled()
    expect(context.reload).toHaveBeenCalledOnce()
    expect(view.text()).toContain('账目已保存')
    expect(view.text()).toContain('取消行程不会撤销已保存的账单')
  })

  it('关闭行程时取消待打开的记账意图，迟到分类结果不会打开孤立弹窗', async () => {
    let finish!: (value: never) => void
    vi.mocked(listCategories).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    const view = await setup()
    view.getComponent(PlacePicker).vm.$emit('select', place)
    await view.get('button[aria-label="记录该地点花费"]').trigger('click')
    await button(view, '取消').trigger('click')
    await flushPromises()
    finish(categories as never)
    await flushPromises()
    expect(view.find('[role="dialog"]').exists()).toBe(false)
    expect(createLedgerEntry).not.toHaveBeenCalled()
  })
})
