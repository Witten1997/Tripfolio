import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import {
  captureMembersBaseline,
  listTripMembers,
  listTripMembersWithBaseline,
  saveTripMembers,
} from './members'
import { ApiError } from './auth'
import { json, problem, receipt, tripId } from '@/shared/travel/__tests__/fixtures'
const { transport } = vi.hoisted(() => ({
  transport: vi.fn<(request: Request) => Promise<Response>>(),
}))
vi.mock('@/shared/api/client', async (original) => {
  const source = await original<typeof import('@/shared/api/client')>()
  return {
    ...source,
    api: source.createApiClient({
      baseUrl: 'http://api.test/api/v1',
      fetch: transport,
      refresh: async () => false,
    }),
  }
})
const guards = [
  { kind: 'members' as const, scope_id: tripId, revision: `sha256:${'a'.repeat(64)}` },
]
beforeEach(() => {
  setActivePinia(createPinia())
  transport.mockReset()
})
describe('成员完整基线API', () => {
  it('一次GET同时返回全部成员和不可变基线', async () => {
    transport.mockResolvedValue(json(200, { data: [], scope_revisions: guards }))
    const result = await listTripMembersWithBaseline(tripId)
    expect(result.members).toEqual([])
    expect(result.baseline.guards).toEqual(guards)
    expect(Object.isFrozen(result.baseline.guards)).toBe(true)
    expect(transport).toHaveBeenCalledTimes(1)
    expect(transport.mock.calls[0]![0].url).toBe(`http://api.test/api/v1/trips/${tripId}/members`)
  })
  it.each([
    undefined,
    [],
    [guards[0]!, guards[0]!],
    [{ ...guards[0]!, kind: 'categories' as const }],
    [{ ...guards[0]!, scope_id: '20000000-0000-4000-8000-000000000001' }],
    [{ ...guards[0]!, revision: 'bad' }],
  ])('缺失或错误scope不能成为编辑基线 %j', async (revisions) => {
    expect(() => captureMembersBaseline(tripId, revisions)).toThrow()
    transport.mockResolvedValue(json(200, { data: [], scope_revisions: revisions }))
    await expect(listTripMembersWithBaseline(tripId)).rejects.toThrow()
    expect(transport).toHaveBeenCalledTimes(1)
  })
  it('普通展示保留旧无基线列表语义', async () => {
    transport.mockResolvedValue(json(200, { data: [] }))
    await expect(listTripMembers(tripId)).resolves.toEqual([])
  })
  it('PUT保持body/key并发出捕获的精确guard，不再GET', async () => {
    transport.mockResolvedValue(json(200, { data: receipt(null) }))
    const baseline = captureMembersBaseline(tripId, guards)
    await saveTripMembers(tripId, [], 'operation', baseline)
    const req = transport.mock.calls[0]![0]
    expect(req.method).toBe('PUT')
    expect(await req.json()).toEqual({ members: [] })
    expect(req.headers.get('Idempotency-Key')).toBe('operation')
    expect(req.headers.get('X-Collection-Guards')).toBe(JSON.stringify(guards))
    expect(transport).toHaveBeenCalledTimes(1)
  })
  it('旧调用无头保持兼容，服务错误原样传递', async () => {
    transport.mockResolvedValue(problem('COLLECTION_BASE_REQUIRED', 428))
    await expect(saveTripMembers(tripId, [], 'operation')).rejects.toBeInstanceOf(ApiError)
    expect(transport.mock.calls[0]![0].headers.has('X-Collection-Guards')).toBe(false)
  })
})
