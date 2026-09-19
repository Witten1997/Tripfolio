import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { json, problem, receipt } from '@/shared/travel/__tests__/fixtures'
import {
  distributeEvenly,
  formatPercent,
  percentGap,
  percentUnits,
  useTripMembers,
} from '@/shared/travel/useTripMembers'

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

beforeEach(() => {
  setActivePinia(createPinia())
  transport.mockReset()
})

const tripId = '10000000-0000-4000-8000-000000000001'
const self = {
  id: '40000000-0000-4000-8000-000000000001',
  trip_id: tripId,
  name: '我',
  share_percent: '100',
  sort_order: 0,
  is_self: true,
  version: '1',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
  deleted_at: null,
}

describe('百分比工具', () => {
  it('解析与差值', () => {
    expect(percentUnits('33.33')).toBe(3333)
    expect(percentUnits('100')).toBe(10000)
    expect(percentUnits('100.01')).toBeNull()
    expect(percentUnits('1.234')).toBeNull()
    expect(percentUnits('-1')).toBeNull()
    expect(percentGap([{ share_percent: '60' }, { share_percent: '40' }])).toBe(0)
    expect(percentGap([{ share_percent: '60' }, { share_percent: '30.5' }])).toBe(950)
    expect(percentGap([{ share_percent: 'x' }])).toBeNull()
  })

  it('格式化与平均分配', () => {
    expect(formatPercent(950)).toBe('9.5')
    expect(formatPercent(3333)).toBe('33.33')
    expect(formatPercent(10000)).toBe('100')
    expect(distributeEvenly(3)).toEqual(['33.34', '33.33', '33.33'])
    expect(percentGap(distributeEvenly(7).map((share_percent) => ({ share_percent })))).toBe(0)
  })
})

describe('useTripMembers', () => {
  it('加载后只有「我」；添加成员后总和不为 100 不能保存，平均分配后可保存并整体 PUT', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      if (request.method === 'GET') return json(200, { data: [self] })
      return json(200, { data: receipt(null) })
    })
    const m = useTripMembers(tripId)
    await m.open()
    expect(m.rows.length).toBe(1)
    expect(m.rows[0]!.is_self).toBe(true)
    expect(m.canSave.value).toBe(false)

    m.add()
    m.rows[1]!.name = '小王'
    expect(m.gap.value).toBe(0)
    expect(m.canSave.value).toBe(true)
    m.rows[1]!.share_percent = '10'
    expect(m.gap.value).toBe(-1000)
    expect(m.canSave.value).toBe(false)
    m.equalize()
    expect(m.rows.map((r) => r.share_percent)).toEqual(['50', '50'])
    expect(m.canSave.value).toBe(true)

    expect(await m.save()).toBe(true)
    const put = requests.find((r) => r.method === 'PUT')!
    expect(put.url).toContain(`/trips/${tripId}/members`)
    expect(put.headers.get('Idempotency-Key')).toBeTruthy()
    const body = (await put.json()) as { members: Array<{ name: string; share_percent: string }> }
    expect(body.members.map((x) => [x.name, x.share_percent])).toEqual([
      ['我', '50'],
      ['小王', '50'],
    ])
    expect(m.feedback.value).toContain('成员已保存')
  })

  it('「我」不能删除，可以上下移动；名称重复与非法百分比阻止保存', async () => {
    transport.mockResolvedValue(json(200, { data: [self] }))
    const m = useTripMembers(tripId)
    await m.open()
    m.remove(0)
    expect(m.rows.length).toBe(1)
    m.add()
    m.rows[1]!.name = ' 我 '
    m.rows[1]!.share_percent = '0'
    expect(m.invalidRows.value['1.name']).toContain('重复')
    m.rows[1]!.name = '小李'
    m.rows[1]!.share_percent = '1.234'
    expect(m.invalidRows.value['1.share_percent']).toBeTruthy()
    m.move(1, -1)
    expect(m.rows[0]!.name).toBe('小李')
    expect(m.rows[1]!.is_self).toBe(true)
  })

  it('被账目引用的成员删除失败时展示 MEMBER_IN_USE 提示且保留输入', async () => {
    const friend = {
      ...self,
      id: '40000000-0000-4000-8000-000000000002',
      name: '小王',
      share_percent: '0',
      is_self: false,
    }
    transport.mockImplementation(async (request) =>
      request.method === 'GET'
        ? json(200, { data: [self, friend] })
        : problem('MEMBER_IN_USE', 409, { detail: '成员「小王」仍被 2 条账目引用，不能删除' }),
    )
    const m = useTripMembers(tripId)
    await m.open()
    m.remove(1)
    expect(m.canSave.value).toBe(true)
    expect(await m.save()).toBe(false)
    expect(m.error.value).toContain('小王')
    expect(m.rows.length).toBe(1)
  })
})
