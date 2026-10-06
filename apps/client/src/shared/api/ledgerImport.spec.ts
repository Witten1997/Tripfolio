import { File as NodeFile } from 'node:buffer'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const { fetcher } = vi.hoisted(() => ({ fetcher: vi.fn() }))
vi.mock('./client', async () => ({
  api: (await import('openapi-fetch')).default({
    baseUrl: 'https://tripfolio.test/api/v1',
    fetch: fetcher,
  }),
  registerRefreshHandler: vi.fn(),
}))
import {
  commitLedgerImport,
  prepareLedgerImport,
  previewLedgerImport,
  type LedgerImportPreview,
} from './ledgerImport'
import { CollectionBaseline } from './collectionGuards'

const trip = '11111111-1111-4111-8111-111111111111'
const other = '22222222-2222-4222-8222-222222222222'
const guard = { kind: 'members' as const, scope_id: trip, revision: `sha256:${'b'.repeat(64)}` }
const row = {
  row: 5,
  amount: '12.50',
  category: '餐饮',
  split_mode: 'personal',
  participants: ['我'],
  occurred_on: '2026-10-06',
  notes: '',
  payer: '我',
  splits: [{ member: '我', amount: '12.50' }],
}
let preview: LedgerImportPreview
let calls: Request[]
const bytes = [80, 75, 0, 255, 1, 42]
const file = () => new File([new Uint8Array(bytes)], '账单.xlsx')
beforeEach(() => {
  vi.stubGlobal('File', NodeFile)
  preview = {
    scope_revisions: [guard],
    digest: 'a'.repeat(64),
    count: 1,
    valid_count: 1,
    total_amount: '12.50',
    currency_code: 'CNY',
    rows: [structuredClone(row)],
    errors: [],
    warnings: [],
  }
  calls = []
  fetcher.mockReset().mockImplementation(async (request: Request) => {
    calls.push(request.clone())
    return Response.json({ data: preview })
  })
})
afterEach(() => vi.unstubAllGlobals())
describe('账单导入文件与预览绑定', () => {
  it('一次预览绑定同File、摘要和members基线，提交保持原bytes/query/header', async () => {
    const original = file()
    const batch = await prepareLedgerImport(trip, original)
    expect(batch.file).toBe(original)
    expect(batch.baseline.guards).toEqual([guard])
    expect(calls).toHaveLength(1)
    expect(new Uint8Array(await calls[0]!.arrayBuffer())).toEqual(new Uint8Array(bytes))
    expect(Object.isFrozen(batch)).toBe(true)
    expect(Object.isFrozen(batch.preview.rows[0]!.splits[0])).toBe(true)
    expect(Reflect.set(batch.preview.rows[0]!, 'amount', '99')).toBe(false)
    await commitLedgerImport(
      batch.tripId,
      batch.file,
      batch.preview.digest,
      'original-key',
      batch.baseline,
    )
    const request = calls[1]!
    expect(new URL(request.url).searchParams.get('preview_digest')).toBe('a'.repeat(64))
    expect(request.headers.get('Content-Type')).toBe('application/octet-stream')
    expect(request.headers.get('Idempotency-Key')).toBe('original-key')
    expect(JSON.parse(request.headers.get('X-Collection-Guards')!)).toEqual([guard])
    expect(new Uint8Array(await request.arrayBuffer())).toEqual(new Uint8Array(bytes))
    expect(calls.every((r) => r.method === 'POST')).toBe(true)
  })
  it.each(['missing', 'duplicate', 'wrong-trip', 'wrong-kind', 'revision'])(
    '拒绝%s成员基线，不另GET成员补齐',
    async (kind) => {
      if (kind === 'missing') delete preview.scope_revisions
      if (kind === 'duplicate') preview.scope_revisions = [guard, guard]
      if (kind === 'wrong-trip') preview.scope_revisions = [{ ...guard, scope_id: other }]
      if (kind === 'wrong-kind') preview.scope_revisions = [{ ...guard, kind: 'categories' }]
      if (kind === 'revision') preview.scope_revisions = [{ ...guard, revision: 'invalid' }]
      await expect(prepareLedgerImport(trip, file())).rejects.toThrow()
      expect(calls).toHaveLength(1)
      expect(calls[0]!.url).toContain('ledger-import-preview')
    },
  )
  it.each(['digest', 'count', 'row', 'splits', 'valid-count'])('拒绝%s不完整预览', async (kind) => {
    if (kind === 'digest') preview.digest = 'A'.repeat(64)
    if (kind === 'count') preview.count = 2
    if (kind === 'row') preview.rows[0]!.payer = null as never
    if (kind === 'splits') preview.rows[0]!.splits = null as never
    if (kind === 'valid-count') preview.valid_count = 0
    await expect(prepareLedgerImport(trip, file())).rejects.toThrow('不完整')
  })
  it('相同文件名和大小不能替代真实文件字节', async () => {
    const first = await prepareLedgerImport(trip, file())
    const changed = new File([new Uint8Array([80, 75, 0, 255, 1, 43])], '账单.xlsx')
    preview.digest = 'c'.repeat(64)
    const second = await prepareLedgerImport(trip, changed)
    await commitLedgerImport(trip, first.file, first.preview.digest, 'first', first.baseline)
    await commitLedgerImport(trip, second.file, second.preview.digest, 'second', second.baseline)
    expect(new Uint8Array(await calls[2]!.arrayBuffer())).toEqual(new Uint8Array(bytes))
    expect(new Uint8Array(await calls[3]!.arrayBuffer())).toEqual(
      new Uint8Array([80, 75, 0, 255, 1, 43]),
    )
    expect(new URL(calls[2]!.url).searchParams.get('preview_digest')).not.toBe(
      new URL(calls[3]!.url).searchParams.get('preview_digest'),
    )
  })
  it('旧preview返回形状与四参commit仍兼容，原结果含nullable/当前data不改写', async () => {
    const result = await previewLedgerImport(trip, file())
    expect(result.digest).toBe(preview.digest)
    const receipt = {
      scope_revisions: [guard],
      affected: [{ entity_type: 'ledger_entry', entity_id: other, version: '3' }],
      replayed: true,
      warnings: [],
      data: null,
    }
    fetcher.mockImplementation(async (request: Request) => {
      calls.push(request.clone())
      return Response.json({ data: receipt })
    })
    expect(await commitLedgerImport(trip, file(), preview.digest, 'key')).toEqual(receipt)
    expect(calls[1]!.headers.has('X-Collection-Guards')).toBe(false)
    const latest = { ...receipt, data: { id: other, version: '99' } }
    fetcher.mockResolvedValue(Response.json({ data: latest }))
    expect(await commitLedgerImport(trip, file(), preview.digest, 'key')).toEqual(latest)
  })
  it('提交拒绝错scope/空/重复members，错误请求不发出', async () => {
    for (const guards of [
      [],
      [{ ...guard, scope_id: other }],
      [{ ...guard, kind: 'categories' as const }],
      [guard, { ...guard, scope_id: other }],
    ]) {
      await expect(
        commitLedgerImport(
          trip,
          file(),
          preview.digest,
          'key',
          new CollectionBaseline({ complete: true, guards }),
        ),
      ).rejects.toThrow('不匹配')
    }
    expect(calls).toHaveLength(0)
  })
  it('取消预览不生成可提交批次，业务错误原样透传', async () => {
    const controller = new AbortController()
    controller.abort()
    await expect(prepareLedgerImport(trip, file(), controller.signal)).rejects.toMatchObject({
      name: 'AbortError',
    })
    fetcher.mockResolvedValue(
      Response.json({ status: 428, code: 'COLLECTION_BASE_REQUIRED' }, { status: 428 }),
    )
    await expect(commitLedgerImport(trip, file(), preview.digest, 'key')).rejects.toMatchObject({
      problem: { status: 428 },
    })
  })
})
