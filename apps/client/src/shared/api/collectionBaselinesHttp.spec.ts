import { createHash, webcrypto } from 'node:crypto'
import { TextEncoder } from 'node:util'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('./client', () => ({ api: { GET: get } }))

import { loadCollectionBaselineHttp, readCollectionBaselinePage } from './collectionBaselinesHttp'
import { ApiError } from './problem'

const scope = { kind: 'categories' as const, scope_id: '11111111-1111-4111-8111-111111111111' }
const epoch = '22222222-2222-4222-8222-222222222222'
const now = '2026-10-05T00:00:00Z'
function pages() {
  const items = [1, 2].map((n) => ({
    id: `00000000-0000-4000-8000-00000000000${n}`,
    version: '9007199254740993',
    name: `分类${n}`,
    icon: null,
    sort_order: n,
    is_preset: false,
    created_at: now,
    updated_at: now,
    deleted_at: null,
  }))
  const revision = `sha256:${createHash('sha256')
    .update(
      `${epoch}\n${scope.kind}\n${scope.scope_id}\n${items.map((i) => `${i.id}:${i.version}\n`).join('')}`,
    )
    .digest('hex')}`
  return items.map((item, index) => ({
    ...scope,
    sync_epoch: epoch,
    revision,
    expires_at: '2026-10-05T00:10:00Z',
    items: [item],
    next_cursor: index === 0 ? 'signed-cursor' : null,
  }))
}
beforeEach(() => {
  get.mockReset()
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(now))
  vi.stubGlobal('crypto', webcrypto)
  vi.stubGlobal('TextEncoder', TextEncoder)
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('collection baseline HTTP adapter', () => {
  it('passes query and signal to the generated GET and returns the direct page', async () => {
    const page = pages()[0]
    const signal = new AbortController().signal
    const request = { ...scope, limit: 1, cursor: 'original-cursor' }
    get.mockResolvedValue({ data: page })
    expect(await readCollectionBaselinePage(request, signal)).toBe(page)
    expect(get).toHaveBeenCalledExactlyOnceWith('/collection-baselines', {
      params: { query: request },
      signal,
    })
  })

  it('uses the existing complete loader across all pages and preserves exact versions', async () => {
    const [first, last] = pages()
    get.mockResolvedValueOnce({ data: first }).mockResolvedValueOnce({ data: last })
    const signal = new AbortController().signal
    const result = await loadCollectionBaselineHttp(scope, { limit: 1, signal })
    expect(result.complete).toBe(true)
    expect(result.items.map((i) => i.version)).toEqual(['9007199254740993', '9007199254740993'])
    expect(Object.isFrozen(result.items[0])).toBe(true)
    expect(get.mock.calls.map((c) => c[1])).toEqual([
      { params: { query: { ...scope, limit: 1 } }, signal },
      { params: { query: { ...scope, limit: 1, cursor: 'signed-cursor' } }, signal },
    ])
  })

  it.each([401, 412, 503])('preserves API errors without adapter retries: %s', async (status) => {
    const problem = {
      status,
      code: status === 412 ? 'COLLECTION_BASELINE_CHANGED' : 'UNAVAILABLE',
      title: '读取失败',
    }
    get.mockResolvedValue({ error: problem })
    const error = await loadCollectionBaselineHttp(scope).catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).problem).toBe(problem)
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('propagates transport and abort errors without retrying', async () => {
    const abort = new DOMException('aborted', 'AbortError')
    get.mockRejectedValue(abort)
    await expect(readCollectionBaselinePage({ ...scope, limit: 50 })).rejects.toBe(abort)
    expect(get).toHaveBeenCalledTimes(1)
    get.mockReset()
    const controller = new AbortController()
    controller.abort()
    await expect(
      loadCollectionBaselineHttp(scope, { signal: controller.signal }),
    ).rejects.toMatchObject({ name: 'AbortError' })
    expect(get).not.toHaveBeenCalled()
  })

  it('does not return a partial collection or restart after a later-page failure', async () => {
    get
      .mockResolvedValueOnce({ data: pages()[0] })
      .mockResolvedValueOnce({ error: { status: 412, code: 'COLLECTION_BASELINE_CHANGED' } })
    await expect(loadCollectionBaselineHttp(scope, { limit: 1 })).rejects.toMatchObject({
      code: 'COLLECTION_BASELINE_CHANGED',
    })
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('leaves completeness and digest validation to the existing loader', async () => {
    get.mockResolvedValue({ data: { ...pages()[0], next_cursor: null } })
    await expect(loadCollectionBaselineHttp(scope, { limit: 1 })).rejects.toThrow('集合资料不完整')
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('rejects missing response data through ApiError', async () => {
    get.mockResolvedValue({})
    await expect(readCollectionBaselinePage({ ...scope, limit: 50 })).rejects.toBeInstanceOf(
      ApiError,
    )
  })
})
