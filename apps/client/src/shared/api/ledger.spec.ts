import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import {
  createLedgerEntry,
  updateLedgerEntry,
  deleteLedgerEntry,
  requiresLedgerMembers,
  type LedgerCreate,
  type LedgerPatch,
  type LedgerWriteContext,
} from './ledger'
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
const guard = { kind: 'members' as const, scope_id: tripId, revision: `sha256:${'a'.repeat(64)}` }
const context = { guards: [guard] }
const body: LedgerCreate = {
  id: '22222222-2222-4222-8222-222222222222',
  kind: 'expense',
  amount: '10.00',
  currency_code: 'CNY',
  category_id: 'cat',
  occurred_on: '2026-10-06',
  payer_member_id: 'member',
  split_mode: 'personal',
  participant_member_ids: ['member'],
}
beforeEach(() => {
  setActivePinia(createPinia())
  transport.mockReset().mockImplementation(async () => json(200, { data: receipt(null) }))
})
describe('账目冻结成员条件API', () => {
  it.each(['expense', 'refund'] as const)('%s创建正文、操作号、成员条件同时传递', async (kind) => {
    await createLedgerEntry(tripId, { ...body, kind }, 'op', context)
    const req = transport.mock.calls[0]![0]
    expect(req.method).toBe('POST')
    expect(req.url).toBe(`http://api.test/api/v1/trips/${tripId}/ledger-entries`)
    expect(await req.json()).toEqual({ ...body, kind })
    expect(req.headers.get('Idempotency-Key')).toBe('op')
    expect(req.headers.get('X-Collection-Guards')).toBe(JSON.stringify(context.guards))
    expect(transport).toHaveBeenCalledOnce()
  })
  it.each([
    { amount: '10.00' },
    { currency_code: 'CNY' },
    { payer_member_id: 'member' },
    { split_mode: 'personal' },
    { participant_member_ids: [] },
  ] satisfies LedgerPatch[])('字段出现即敏感，包括同值和空数组 %j', async (patch) => {
    expect(requiresLedgerMembers(patch)).toBe(true)
    await updateLedgerEntry(tripId, body.id, '9007199254740993', patch, 'op', context)
    const req = transport.mock.calls[0]![0]
    expect(req.method).toBe('PATCH')
    expect(req.headers.get('If-Match')).toBe('"9007199254740993"')
    expect(req.headers.get('Idempotency-Key')).toBe('op')
    expect(req.headers.get('X-Collection-Guards')).toBe(JSON.stringify(context.guards))
    expect(await req.json()).toEqual(patch)
  })
  it('只判断自有字段，undefined自有字段也不能伪装成非敏感', () => {
    expect(requiresLedgerMembers(Object.create({ amount: '10' }))).toBe(false)
    expect(requiresLedgerMembers({ amount: undefined })).toBe(true)
  })
  it.each([
    { notes: '备注' },
    { category_id: 'cat2' },
    { occurred_on: '2026-10-07' },
    { attachment_asset_ids: [] },
    { refunded_entry_id: null },
  ] satisfies LedgerPatch[])('非财务字段无集合头 %j', async (patch) => {
    expect(requiresLedgerMembers(patch)).toBe(false)
    await updateLedgerEntry(tripId, body.id, '1', patch, 'op', context)
    expect(transport.mock.calls[0]![0].headers.has('X-Collection-Guards')).toBe(false)
  })
  it.each([
    { guards: [] },
    { guards: [guard, guard] },
    { guards: [{ ...guard, kind: 'categories' }] },
    { guards: [{ ...guard, scope_id: body.id }] },
    { guards: [{ ...guard, revision: 'bad' }] },
  ] satisfies LedgerWriteContext[])('显式错误条件在网络前拒绝 %j', async (invalid) => {
    await expect(createLedgerEntry(tripId, body, 'op', invalid)).rejects.toThrow()
    await expect(
      updateLedgerEntry(tripId, body.id, '1', { amount: '11' }, 'op', invalid),
    ).rejects.toThrow()
    expect(transport).not.toHaveBeenCalled()
  })
  it('旧调用和删除兼容，删除保留实体条件', async () => {
    await createLedgerEntry(tripId, body, 'op')
    await updateLedgerEntry(tripId, body.id, '1', { amount: '12' }, 'op')
    await deleteLedgerEntry(tripId, body.id, '1', 'op')
    expect(transport.mock.calls.every(([r]) => !r.headers.has('X-Collection-Guards'))).toBe(true)
    expect(transport.mock.calls[2]![0].headers.get('If-Match')).toBe('"1"')
  })
  it('保留原回执facts和nullable data，不以当前资源重造事实', async () => {
    const result = { ...receipt(null), scope_revisions: [guard], replayed: true }
    transport.mockResolvedValue(json(200, { data: result }))
    expect(await createLedgerEntry(tripId, body, 'op', context)).toEqual({ resource: null, result })
    const newer = {
      id: body.id,
      version: '99',
      kind: 'expense',
      amount: '99',
      occurred_on: '2026-10-06',
    }
    transport.mockResolvedValue(json(200, { data: { ...result, data: newer } }))
    expect(
      await updateLedgerEntry(tripId, body.id, '1', { notes: 'a' }, 'op', { guards: [] }),
    ).toEqual({ resource: newer, result: { ...result, data: newer } })
  })
  it.each([
    ['COLLECTION_BASE_REQUIRED', 428],
    ['COLLECTION_CONFLICT', 412],
  ] as const)('服务错误%s原样抛出', async (code, status) => {
    transport.mockResolvedValue(problem(code, status))
    await expect(createLedgerEntry(tripId, body, 'op', context)).rejects.toMatchObject({
      code,
      problem: { status },
    })
  })
})
