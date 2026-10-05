import { createHash, webcrypto } from 'node:crypto'
import { TextEncoder } from 'node:util'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ElMessageBox, type MessageBoxData } from 'element-plus'
import { createPinia } from 'pinia'
import { ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ItineraryItem } from '@/shared/api/itinerary'
import {
  deferred,
  json,
  problem,
  trip as tripFixture,
  tripId,
} from '@/shared/travel/__tests__/fixtures'
import ItineraryTab from './ItineraryTab.vue'

const { transport } = vi.hoisted(() => ({
  transport: vi.fn<(request: Request) => Promise<Response>>(),
}))
vi.mock('@/shared/api/client', async (original) => {
  const module = await original<typeof import('@/shared/api/client')>()
  return {
    ...module,
    api: module.createApiClient({
      baseUrl: 'http://api.test/api/v1',
      fetch: transport,
      refresh: async () => false,
    }),
  }
})
vi.mock('@/shared/travel/tripContext', () => ({ useTripContext: () => context }))
vi.mock('@/shared/api/routePlan', () => ({
  getRoutePlan: vi.fn(async () => ({ summary: { revision: '1', status: 'empty' }, legs: [] })),
  recalculateRoutePlan: vi.fn(),
  updateRouteLegMode: vi.fn(),
}))
vi.mock('vue-draggable-plus', async () => {
  const { defineComponent } = await import('vue')
  return {
    VueDraggable: defineComponent({
      name: 'VueDraggable',
      props: ['modelValue', 'disabled'],
      emits: ['update:modelValue', 'start', 'end'],
      template: '<div><slot /></div>',
    }),
  }
})

const day1 = '2026-10-01',
  day2 = '2026-10-02'
const context = {
  tripId,
  trip: ref(tripFixture({ start_date: day1, end_date: day2 })),
  today: ref(day1),
  replace: vi.fn(),
  minorUnits: ref(2),
}
const epoch = '22222222-2222-4222-8222-222222222222'
function item(n: number, date: string, order: number): ItineraryItem {
  return {
    id: `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`,
    trip_id: tripId,
    version: '9007199254740993',
    title: `行程${n}`,
    scheduled_on: date,
    sort_order: order,
    poi_id: '',
    footprint_excluded: false,
    kind: 'other',
    status: 'pending',
    place_name: '',
    address: '',
    latitude: null,
    longitude: null,
    estimated_amount: null,
    currency_code: null,
    notes: '',
    planned_start_local: null,
    planned_end_local: null,
    planned_duration_minutes: null,
    actual_start_local: null,
    actual_end_local: null,
    actual_notes: '',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    deleted_at: null,
  }
}
let rows: ItineraryItem[], requests: Request[], views: VueWrapper[]
let writeResponse: () => Promise<Response>
let readOverride: ((r: Request) => Response | Promise<Response> | undefined) | undefined
function page(request: Request) {
  const scope_id = new URL(request.url).searchParams.get('scope_id')!
  const items = rows
    .filter((i) => i.scheduled_on === scope_id.split('/')[1])
    .sort((a, b) => a.id.localeCompare(b.id))
  return {
    kind: 'itinerary_day',
    scope_id,
    sync_epoch: epoch,
    expires_at: '2026-10-05T00:10:00Z',
    revision: `sha256:${createHash('sha256')
      .update(
        `${epoch}\nitinerary_day\n${scope_id}\n${items.map((i) => `${i.id}:${i.version}\n`).join('')}`,
      )
      .digest('hex')}`,
    items,
    next_cursor: null,
  }
}
function success() {
  return json(200, {
    data: {
      operation_id: 'ok',
      primary: null,
      affected: [],
      commit_cursor: null,
      warnings: [],
      replayed: true,
      data: null,
    },
  })
}
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-10-05T00:00:00Z'))
  vi.stubGlobal('crypto', webcrypto)
  vi.stubGlobal('TextEncoder', TextEncoder)
  rows = [item(1, day1, 0), item(2, day1, 1), item(3, day2, 0)]
  requests = []
  views = []
  readOverride = undefined
  writeResponse = async () => problem('COLLECTION_CONFLICT', 412)
  transport.mockReset().mockImplementation(async (request) => {
    requests.push(request.clone())
    if (request.method === 'POST') return writeResponse()
    const override = readOverride?.(request)
    if (override) return override
    return request.url.includes('collection-baselines')
      ? json(200, page(request))
      : json(200, { items: rows, next_cursor: null })
  })
})
afterEach(() => {
  views.forEach((view) => view.unmount())
  vi.restoreAllMocks()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})
function render() {
  const view = mount(ItineraryTab, {
    global: { plugins: [createPinia()], stubs: { ItineraryItemDialog: true } },
  })
  views.push(view)
  return view
}
async function ready(view: VueWrapper) {
  await flushPromises()
  await vi.waitFor(() => {
    const lists = view.findAllComponents({ name: 'VueDraggable' })
    expect(lists).toHaveLength(2)
    expect(lists.every((d) => d.props('disabled') === false)).toBe(true)
  })
}
const titles = (view: VueWrapper, date: string) =>
  view.findAll(`[data-date="${date}"] .item-title strong`).map((node) => node.text())
const button = (view: VueWrapper, text: string) =>
  view.findAll('button').find((button) => button.text() === text)!
async function drag(view: VueWrapper) {
  const [from, to] = view.findAllComponents({ name: 'VueDraggable' })
  from!.vm.$emit('update:modelValue', [rows[1]])
  to!.vm.$emit('update:modelValue', [rows[0], rows[2]])
  await flushPromises()
  to!.vm.$emit('end', { from: from!.element, to: to!.element, data: rows[0], newIndex: 0 })
  await flushPromises()
  await vi.waitFor(() => expect(view.find('[aria-label="待处理的行程排序"]').exists()).toBe(true))
}
const posts = () => requests.filter((r) => r.method === 'POST')

describe('行程排序入口', () => {
  it('完整集合未就绪不挂可拖动列表，加载完成后才允许排序', async () => {
    const slow = deferred<Response>()
    readOverride = (r) =>
      r.url.includes('collection-baselines') && r.url.includes(day1) ? slow.promise : undefined
    const view = render()
    await vi.waitFor(() =>
      expect(requests.some((r) => r.url.includes('collection-baselines'))).toBe(true),
    )
    expect(view.findAll('.drag-handle')).toHaveLength(0)
    expect(posts()).toHaveLength(0)
    slow.resolve(json(200, page(requests.find((r) => r.url.includes(day1))!)))
    await ready(view)
  })

  it('失败继续显示拖动草稿，禁其他修改且不自动读取或恢复镜像', async () => {
    const view = render()
    await ready(view)
    const count = requests.length
    await drag(view)
    expect(titles(view, day1)).toEqual(['行程2'])
    expect(titles(view, day2)).toEqual(['行程1', '行程3'])
    expect(requests).toHaveLength(count + 1)
    expect(view.findAllComponents({ name: 'VueDraggable' }).every((d) => d.props('disabled'))).toBe(
      true,
    )
    expect(
      view.findAll('button.drag-handle').every((b) => (b.element as HTMLButtonElement).disabled),
    ).toBe(true)
    expect(
      view
        .findAll(
          'button[aria-label^="编辑行程"], button[aria-label^="删除行程"], button[aria-label^="为 "]',
        )
        .every((b) => (b.element as HTMLButtonElement).disabled),
    ).toBe(true)
    view.findComponent({ name: 'VueDraggable' }).vm.$emit('end', {})
    await flushPromises()
    expect(requests).toHaveLength(count + 1)
    expect(titles(view, day2)).toEqual(['行程1', '行程3'])
  })

  it('手动核对不改草稿，取消确认仍保留，明确采用后才替换', async () => {
    const view = render()
    await ready(view)
    await drag(view)
    rows[1] = { ...rows[1]!, title: '其他设备新标题', version: '9007199254740994' }
    await button(view, '核对最新安排').trigger('click')
    await vi.waitFor(() =>
      expect(view.find('table[aria-label="行程排序核对"]').exists()).toBe(true),
    )
    expect(view.get('table').text()).toContain('其他设备新标题')
    expect(titles(view, day1)).toEqual(['行程2'])
    expect(titles(view, day2)).toEqual(['行程1', '行程3'])
    const confirm = vi.spyOn(ElMessageBox, 'confirm').mockRejectedValueOnce('cancel')
    await button(view, '采用最新安排并重新排序').trigger('click')
    await flushPromises()
    expect(titles(view, day2)).toEqual(['行程1', '行程3'])
    confirm.mockResolvedValueOnce('confirm' as MessageBoxData)
    await button(view, '采用最新安排并重新排序').trigger('click')
    await flushPromises()
    expect(titles(view, day1)).toEqual(['行程1', '其他设备新标题'])
    expect(titles(view, day2)).toEqual(['行程3'])
    expect(view.find('[aria-label="待处理的行程排序"]').exists()).toBe(false)
    expect(posts()).toHaveLength(1)
  })

  it('网络结果未知时只提供原样重试，发送完全相同的请求', async () => {
    writeResponse = async () => {
      throw new TypeError('offline')
    }
    const view = render()
    await ready(view)
    await drag(view)
    expect(button(view, '原样重试')).toBeDefined()
    expect(button(view, '核对最新安排')).toBeUndefined()
    writeResponse = async () => success()
    await button(view, '原样重试').trigger('click')
    await vi.waitFor(() =>
      expect(view.find('[aria-label="待处理的行程排序"]').exists()).toBe(false),
    )
    expect(posts()).toHaveLength(2)
    expect(posts()[1]!.headers.get('Idempotency-Key')).toBe(
      posts()[0]!.headers.get('Idempotency-Key'),
    )
    expect(posts()[1]!.headers.get('X-Collection-Guards')).toBe(
      posts()[0]!.headers.get('X-Collection-Guards'),
    )
    expect(await posts()[1]!.json()).toEqual(await posts()[0]!.json())
  })

  it('核对读取失败时保留原排序，不开放采用按钮', async () => {
    const view = render()
    await ready(view)
    await drag(view)
    await button(view, '核对最新安排').trigger('click')
    await vi.waitFor(() => expect(button(view, '采用最新安排并重新排序')).toBeDefined())
    readOverride = () => problem('DEPENDENCY_UNAVAILABLE', 503)
    await button(view, '核对最新安排').trigger('click')
    await flushPromises()
    expect(titles(view, day2)).toEqual(['行程1', '行程3'])
    expect(view.text()).toContain('当前显示的是待保存的排序')
    expect(button(view, '采用最新安排并重新排序')).toBeUndefined()
    expect(posts()).toHaveLength(1)
  })
})
