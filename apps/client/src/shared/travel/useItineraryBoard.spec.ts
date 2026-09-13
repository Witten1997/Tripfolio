import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { ItineraryItem } from '@/shared/api/itinerary'
import { deferred, json, problem } from '@/shared/travel/__tests__/fixtures'
import { useItineraryBoard } from '@/shared/travel/useItineraryBoard'

const { transport } = vi.hoisted(() => ({
  transport: vi.fn<(request: Request) => Promise<Response>>(),
}))
vi.mock('@/shared/api/client', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/shared/api/client')>()
  return {
    ...original,
    api: original.createApiClient({
      baseUrl: 'http://api.test/api/v1',
      fetch: transport,
      refresh: async () => false,
    }),
  }
})

function item(overrides: Partial<ItineraryItem>): ItineraryItem {
  return {
    id: crypto.randomUUID(),
    trip_id: 'trip',
    title: 'x',
    kind: 'other',
    scheduled_on: '2026-10-01',
    sort_order: 0,
    planned_start_local: null,
    planned_end_local: null,
    planned_duration_minutes: null,
    place_name: '',
    address: '',
    latitude: null,
    longitude: null,
    estimated_amount: null,
    currency_code: null,
    notes: '',
    status: 'pending',
    actual_start_local: null,
    actual_end_local: null,
    actual_notes: '',
    version: '1',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    deleted_at: null,
    ...overrides,
  }
}

const a = item({ id: 'a', scheduled_on: '2026-10-01', sort_order: 0 })
const b = item({ id: 'b', scheduled_on: '2026-10-01', sort_order: 1, version: '2' })
const c = item({ id: 'c', scheduled_on: '2026-10-02', sort_order: 0 })

function listResponse(items: ItineraryItem[]) {
  return json(200, { items, next_cursor: null })
}

beforeEach(() => {
  setActivePinia(createPinia())
  transport.mockReset()
})

describe('useItineraryBoard', () => {
  it('加载后按天分组并铺满旅行日期', async () => {
    transport.mockResolvedValue(listResponse([b, a, c]))
    const board = useItineraryBoard('trip', () => ({ start: '2026-10-01', end: '2026-10-03' }))
    await board.reload()
    expect(board.days.value.map((d) => d.date)).toEqual(['2026-10-01', '2026-10-02', '2026-10-03'])
    expect(board.days.value[0]!.items.map((i) => i.id)).toEqual(['a', 'b'])
  })

  it('跨日拖动提交源日与目标日的完整集合与各自版本，成功后按响应重载', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      if (request.method === 'GET') return listResponse([a, b, c])
      return json(200, {
        data: {
          operation_id: 'op',
          primary: null,
          affected: [],
          commit_cursor: null,
          warnings: [],
          replayed: false,
          data: null,
        },
      })
    })
    const board = useItineraryBoard('trip', () => ({ start: '2026-10-01', end: '2026-10-03' }))
    await board.reload()
    // 把 a 从 10-01 移到 10-02 的最前面
    await board.moveItem('a', '2026-10-02', 0)
    const reorder = requests.find((r) => r.method === 'POST')!
    expect(new URL(reorder.url).pathname).toBe('/api/v1/trips/trip/itinerary-items/reorder')
    expect(reorder.headers.get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    expect(await reorder.json()).toEqual({
      days: [
        { date: '2026-10-01', items: [{ id: 'b', base_version: '2' }] },
        {
          date: '2026-10-02',
          items: [
            { id: 'a', base_version: '1' },
            { id: 'c', base_version: '1' },
          ],
        },
      ],
    })
    expect(requests.filter((r) => r.method === 'GET')).toHaveLength(2)
  })

  it('同日拖动只提交那一天；目标位置与当前相同时不请求', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      if (request.method === 'GET') return listResponse([a, b, c])
      return json(200, {
        data: {
          operation_id: 'op',
          primary: null,
          affected: [],
          commit_cursor: null,
          warnings: [],
          replayed: false,
          data: null,
        },
      })
    })
    const board = useItineraryBoard('trip', () => ({ start: '2026-10-01', end: '2026-10-03' }))
    await board.reload()
    await board.moveItem('b', '2026-10-01', 0)
    const reorder = requests.find((r) => r.method === 'POST')!
    expect(await reorder.json()).toEqual({
      days: [
        {
          date: '2026-10-01',
          items: [
            { id: 'b', base_version: '2' },
            { id: 'a', base_version: '1' },
          ],
        },
      ],
    })
    // 重载后 mock 仍返回原顺序：a 在 0 位，放回 0 位不产生请求
    await board.moveItem('a', '2026-10-01', 0)
    expect(requests.filter((r) => r.method === 'POST')).toHaveLength(1)
  })

  it('ORDER_CHANGED 或 412 时提示并重新加载，拖动期间拒绝并发拖动', async () => {
    const slow = deferred<Response>()
    let posts = 0
    transport.mockImplementation(async (request) => {
      if (request.method === 'GET') return listResponse([a, b, c])
      posts++
      return posts === 1 ? slow.promise : problem('ORDER_CHANGED', 409)
    })
    const board = useItineraryBoard('trip', () => ({ start: '2026-10-01', end: '2026-10-03' }))
    await board.reload()
    const first = board.moveItem('a', '2026-10-02', 0)
    expect(board.reordering.value).toBe(true)
    await board.moveItem('b', '2026-10-02', 0)
    expect(posts).toBe(1)
    slow.resolve(problem('VERSION_CONFLICT', 412))
    await first
    expect(board.reordering.value).toBe(false)
    expect(board.actionFailure.value).toContain('其他设备')
    await board.moveItem('a', '2026-10-02', 0)
    expect(board.actionFailure.value).toContain('已经变化')
  })
})
