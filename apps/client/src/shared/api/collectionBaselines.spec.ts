import { createHash, webcrypto } from 'node:crypto'
import { TextEncoder } from 'node:util'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  loadCollectionBaseline,
  type CollectionPageRequest,
  type ReadableCollectionKind,
} from './collectionBaselines'

const account = '11111111-1111-4111-8111-111111111111'
const trip = '22222222-2222-4222-8222-222222222222'
const epoch = '33333333-3333-4333-8333-333333333333'
const day = '2028-02-29'
const now = '2026-10-05T00:00:00Z'
const expires = '2026-10-05T00:10:00Z'

function resource(kind: ReadableCollectionKind, index: number): Record<string, unknown> {
  const common = {
    id: `00000000-0000-4000-8000-${index.toString(16).padStart(12, '0')}`,
    version: '9007199254740993',
    sort_order: 1000 - index,
    created_at: now,
    updated_at: now,
    deleted_at: null,
  }
  if (kind === 'categories')
    return { ...common, name: `分类${index}`, icon: null, is_preset: false }
  const location = { trip_id: trip, place_name: '', address: '', latitude: null, longitude: null }
  if (kind === 'photo_day')
    return {
      ...common,
      ...location,
      asset_id: account,
      recorded_on: day,
      taken_at_local: null,
      caption: '',
    }
  return {
    ...common,
    ...location,
    scheduled_on: day,
    title: '行程',
    poi_id: '',
    footprint_excluded: false,
    kind: 'other',
    status: 'pending',
    planned_start_local: null,
    planned_end_local: null,
    actual_start_local: null,
    actual_end_local: null,
    planned_duration_minutes: null,
    estimated_amount: null,
    currency_code: null,
    notes: '',
    actual_notes: '',
  }
}
function fixture(kind: ReadableCollectionKind = 'categories', count = 3) {
  const scope = { kind, scope_id: kind === 'categories' ? account : `${trip}/${day}` }
  const items = Array.from({ length: count }, (_, index) => resource(kind, index + 1))
  const revision = `sha256:${createHash('sha256')
    .update(
      `${epoch}\n${kind}\n${scope.scope_id}\n${items.map((i) => `${i.id}:${i.version}\n`).join('')}`,
    )
    .digest('hex')}`
  const page = (request: CollectionPageRequest<ReadableCollectionKind>) => {
    const start = request.cursor ? Number(request.cursor) : 0
    const end = Math.min(start + request.limit, items.length)
    return {
      ...scope,
      sync_epoch: epoch,
      revision,
      expires_at: expires,
      items: items.slice(start, end),
      next_cursor: end < items.length ? String(end) : null,
    }
  }
  return { scope, items, revision, page }
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(now))
  vi.stubGlobal('crypto', webcrypto)
  vi.stubGlobal('TextEncoder', TextEncoder)
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('loadCollectionBaseline', () => {
  it.each(['categories', 'itinerary_day', 'photo_day'] as const)(
    'loads every %s page and binds immutable resources to the verified guard',
    async (kind) => {
      const f = fixture(kind, 105)
      const read = vi.fn(async (request: CollectionPageRequest<typeof kind>) => f.page(request))
      const result = await loadCollectionBaseline(f.scope, read, { limit: 100 })
      expect(read).toHaveBeenCalledTimes(2)
      expect(read.mock.calls[1]![0].cursor).toBe('100')
      expect(result.complete).toBe(true)
      expect(result.items).toHaveLength(105)
      expect(result.items[0]!.version).toBe('9007199254740993')
      expect(result.baseline.guards).toEqual([{ ...f.scope, revision: f.revision }])
      expect(Object.isFrozen(result)).toBe(true)
      expect(Object.isFrozen(result.items)).toBe(true)
      expect(Object.isFrozen(result.items[0])).toBe(true)
      expect(Object.isFrozen(read.mock.calls[0]![0])).toBe(true)
      f.items[0]!.version = '1'
      expect(result.items[0]!.version).toBe('9007199254740993')
    },
  )

  it.each(['categories', 'itinerary_day', 'photo_day'] as const)(
    'accepts a verified empty %s scope',
    async (kind) => {
      const f = fixture(kind, 0)
      const result = await loadCollectionBaseline(f.scope, async (request) => f.page(request))
      expect(result.items).toEqual([])
      expect(result.baseline.guards[0]!.revision).toBe(f.revision)
    },
  )

  it.each([
    'missing-item',
    'premature-terminal',
    'duplicate',
    'version',
    'numeric-version',
    'overflow',
    'epoch',
    'revision',
    'scope',
    'kind',
    'expiry',
    'expired',
    'cursor-loop',
    'empty-nonterminal',
    'deleted',
  ])('rejects %s without exposing a complete baseline', async (failure) => {
    const f = fixture()
    let calls = 0
    const read = vi.fn(async (request: CollectionPageRequest<ReadableCollectionKind>) => {
      const page = f.page(request)
      calls++
      if (failure === 'missing-item' && calls === 2) page.items = []
      if (failure === 'premature-terminal' && calls === 1) page.next_cursor = null
      if (failure === 'duplicate' && calls === 2) page.items = [f.items[0]!]
      if (failure === 'version') page.items[0] = { ...page.items[0], version: '9007199254740994' }
      if (failure === 'numeric-version') page.items[0] = { ...page.items[0], version: 2 }
      if (failure === 'overflow')
        page.items[0] = { ...page.items[0], version: '9223372036854775808' }
      if (failure === 'epoch' && calls === 2) page.sync_epoch = account
      if (failure === 'revision' && calls === 2) page.revision = `sha256:${'a'.repeat(64)}`
      if (failure === 'scope') page.scope_id = trip
      if (failure === 'kind') page.kind = 'photo_day'
      if (failure === 'expiry' && calls === 2) page.expires_at = '2026-10-05T00:11:00Z'
      if (failure === 'expired') page.expires_at = now
      if (failure === 'cursor-loop') {
        page.items = [resource('categories', calls)]
        page.next_cursor = 'same'
      }
      if (failure === 'empty-nonterminal') page.items = []
      if (failure === 'deleted') page.items[0] = { ...page.items[0], deleted_at: now }
      return page
    })
    await expect(
      loadCollectionBaseline(f.scope, read, { limit: failure === 'cursor-loop' ? 1 : 2 }),
    ).rejects.toThrow('集合资料')
    expect(calls).toBeLessThanOrEqual(2)
  })

  it.each(['trip_id', 'scheduled_on', 'recorded_on'] as const)(
    'rejects a resource from a different %s scope',
    async (field) => {
      const f = fixture(field === 'recorded_on' ? 'photo_day' : 'itinerary_day', 1)
      f.items[0]![field] = field === 'trip_id' ? account : '2028-03-01'
      await expect(
        loadCollectionBaseline(f.scope, async (request) => f.page(request)),
      ).rejects.toThrow()
    },
  )

  it('does not restart after a page fails or replace an existing baseline', async () => {
    const f = fixture()
    const error = new Error('412 COLLECTION_BASELINE_CHANGED')
    const read = vi.fn(async (request: CollectionPageRequest<ReadableCollectionKind>) => {
      if (request.cursor) throw error
      return f.page(request)
    })
    await expect(loadCollectionBaseline(f.scope, read, { limit: 2 })).rejects.toBe(error)
    expect(read).toHaveBeenCalledTimes(2)
  })

  it('supports cancellation before and after the reader resolves', async () => {
    const f = fixture()
    const controller = new AbortController()
    const read = vi.fn(
      async (request: CollectionPageRequest<ReadableCollectionKind>, signal?: AbortSignal) => {
        expect(signal).toBe(controller.signal)
        controller.abort()
        return f.page(request)
      },
    )
    await expect(
      loadCollectionBaseline(f.scope, read, { signal: controller.signal }),
    ).rejects.toThrow()
    await expect(
      loadCollectionBaseline(f.scope, read, { signal: controller.signal }),
    ).rejects.toThrow()
    expect(read).toHaveBeenCalledTimes(1)
  })

  it('rejects getter responses without invoking the getter', async () => {
    const f = fixture()
    const page = f.page({ ...f.scope, limit: 50 })
    const get = vi.fn(() => '1')
    Object.defineProperty(page.items[0], 'version', { enumerable: true, get })
    await expect(loadCollectionBaseline(f.scope, async () => page)).rejects.toThrow()
    expect(get).not.toHaveBeenCalled()
  })

  it.each(['kind', 'status'])('does not coerce resource %s into a schema enum', async (field) => {
    const f = fixture('itinerary_day', 1)
    f.items[0]![field] = [f.items[0]![field]]
    await expect(
      loadCollectionBaseline(f.scope, async (request) => f.page(request)),
    ).rejects.toThrow()
  })

  it.each(['01', '1\n', '+1', '0'])(
    'rejects noncanonical version %j even with a matching digest',
    async (version) => {
      const f = fixture('categories', 1)
      f.items[0]!.version = version
      const page = f.page({ ...f.scope, limit: 50 })
      page.revision = `sha256:${createHash('sha256').update(`${epoch}\ncategories\n${account}\n${f.items[0]!.id}:${version}\n`).digest('hex')}`
      await expect(loadCollectionBaseline(f.scope, async () => page)).rejects.toThrow()
    },
  )

  it('rejects missing crypto, invalid page size and impossible scope dates before reading', async () => {
    const f = fixture()
    const read = vi.fn(async (request: CollectionPageRequest<ReadableCollectionKind>) =>
      f.page(request),
    )
    for (const limit of [0, 101, 1.5])
      await expect(loadCollectionBaseline(f.scope, read, { limit })).rejects.toThrow()
    await expect(
      loadCollectionBaseline({ kind: 'photo_day', scope_id: `${trip}/2026-02-29` }, read),
    ).rejects.toThrow()
    vi.stubGlobal('crypto', {})
    await expect(loadCollectionBaseline(f.scope, read)).rejects.toThrow('安全连接')
    expect(read).not.toHaveBeenCalled()
  })

  it('rechecks expiry after the asynchronous digest', async () => {
    const f = fixture()
    vi.stubGlobal('crypto', {
      subtle: {
        digest: async (...args: Parameters<typeof webcrypto.subtle.digest>) => {
          const result = await webcrypto.subtle.digest(...args)
          vi.setSystemTime(new Date(expires))
          return result
        },
      },
    })
    await expect(
      loadCollectionBaseline(f.scope, async (request) => f.page(request)),
    ).rejects.toThrow()
  })
})
