import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { archiveTrip, listTrips, trashTrip } from '@/shared/api/trips'
import { createWriteIntent, writeWarnings } from '@/shared/api/writes'
import { json, receipt, trip } from '@/shared/travel/__tests__/fixtures'

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

describe('旅行列表与状态写入契约', () => {
  it('查询传递服务器筛选/排序/游标，读取裸分页与服务器阶段', async () => {
    transport.mockImplementation(async (request) => {
      expect(Object.fromEntries(new URL(request.url).searchParams)).toEqual({
        q: '京都',
        phase: 'ongoing',
        archived: 'true',
        sort: 'updated_at_desc',
        cursor: 'opaque-cursor',
        limit: '30',
      })
      return json(200, {
        items: [trip({ phase: 'ongoing', start_date: '2099-01-01' })],
        next_cursor: 'next',
      })
    })
    const page = await listTrips({
      q: '京都',
      phase: 'ongoing',
      archived: 'true',
      sort: 'updated_at_desc',
      cursor: 'opaque-cursor',
      limit: 30,
    })
    expect(page.next_cursor).toBe('next')
    expect(page.items[0]!.phase).toBe('ongoing')
  })

  it('归档只改变独立的归档状态，回收站请求均携带版本和操作号', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      return json(200, { data: receipt(null, ['MERGED_WITH_NEWER_VERSION'], true) })
    })
    const base = trip()
    const archiveId = crypto.randomUUID()
    const trashId = crypto.randomUUID()
    const outcome = await archiveTrip(base.id, base.version, true, archiveId)
    await trashTrip(base.id, base.version, trashId)
    expect(await requests[0]!.json()).toEqual({ archived: true })
    expect(requests[0]!.headers.get('Idempotency-Key')).toBe(archiveId)
    expect(requests[1]!.method).toBe('DELETE')
    expect(requests[1]!.headers.get('Idempotency-Key')).toBe(trashId)
    expect(requests.every((request) => request.headers.get('If-Match') === '"3"')).toBe(true)
    expect(outcome.resource).toBeNull()
    expect(writeWarnings(outcome.result)[0]).toContain('合并本次改动')
  })

  it('等价请求键序不同仍沿用操作号，版本或 nullable 字段变化则换号', () => {
    const intent = createWriteIntent()
    const key = intent.key({ version: '3', patch: { budget_amount: null, name: '旅行' } })
    expect(intent.key({ patch: { name: '旅行', budget_amount: null }, version: '3' })).toBe(key)
    const changed = intent.key({ version: '4', patch: { name: '旅行', budget_amount: null } })
    expect(changed).not.toBe(key)
    expect(intent.key({ version: '4', patch: { name: '旅行' } })).not.toBe(changed)
  })
})
