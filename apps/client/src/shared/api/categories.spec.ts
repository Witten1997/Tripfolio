import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { CollectionBaseline } from './collectionGuards'
import { updateCategory } from './categories'
import { useSessionStore, type Account } from '@/shared/stores/session'
import { json, receipt } from '@/shared/travel/__tests__/fixtures'

const { transport } = vi.hoisted(() => ({
  transport: vi.fn<(request: Request) => Promise<Response>>(),
}))
vi.mock('./client', async (original) => {
  const module = await original<typeof import('./client')>()
  return {
    ...module,
    api: module.createApiClient({
      baseUrl: 'http://api.test/api/v1',
      fetch: transport,
      refresh: async () => false,
    }),
  }
})
const owner = '11111111-1111-4111-8111-111111111111'
const baseline = (scope = owner) =>
  new CollectionBaseline({
    complete: true,
    guards: [{ kind: 'categories', scope_id: scope, revision: `sha256:${'a'.repeat(64)}` }],
  })
beforeEach(() => {
  setActivePinia(createPinia())
  useSessionStore().account = { id: owner } as Account
  transport.mockReset()
})

describe('分类排序请求基线', () => {
  it('透传原版本、0、操作号和guard，并保留原facts与nullable data', async () => {
    const base = baseline()
    const result = { ...receipt(null, [], true), scope_revisions: [...base.guards] }
    let captured!: Request
    transport.mockImplementation(async (request) => {
      captured = request.clone()
      return json(200, { data: result })
    })
    const out = await updateCategory(owner, '9007199254740993', { sort_order: 0 }, owner, base)
    expect(captured.headers.get('If-Match')).toBe('"9007199254740993"')
    expect(captured.headers.get('Idempotency-Key')).toBe(owner)
    expect(captured.headers.get('X-Collection-Guards')).toBe(base.headers['X-Collection-Guards'])
    expect(await captured.json()).toEqual({ sort_order: 0 })
    expect(out.result).toEqual(result)
  })
  it('普通改名兼容四参数，无无关guard', async () => {
    transport.mockImplementation(async () => json(200, { data: receipt(null) }))
    await updateCategory(owner, '1', { name: '交通' }, owner)
    expect(transport.mock.calls[0]![0].headers.has('X-Collection-Guards')).toBe(false)
    await expect(updateCategory(owner, '1', { name: '交通' }, owner, baseline())).rejects.toThrow()
    expect(transport).toHaveBeenCalledTimes(1)
  })
  it('拒绝其他账号和多余范围', async () => {
    await expect(
      updateCategory(
        owner,
        '1',
        { sort_order: 0 },
        owner,
        baseline('22222222-2222-4222-8222-222222222222'),
      ),
    ).rejects.toThrow()
    await expect(
      updateCategory(
        owner,
        '1',
        { sort_order: 0 },
        owner,
        new CollectionBaseline({ complete: true, guards: [] }),
      ),
    ).rejects.toThrow()
    expect(transport).not.toHaveBeenCalled()
  })
})
