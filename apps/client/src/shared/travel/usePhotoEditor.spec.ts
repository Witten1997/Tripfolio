import { createHash, webcrypto } from 'node:crypto'
import { TextEncoder } from 'node:util'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const { fetcher } = vi.hoisted(() => ({ fetcher: vi.fn() }))
vi.mock('@/shared/api/client', async () => ({
  api: (await import('openapi-fetch')).default({
    baseUrl: 'https://tripfolio.test/api/v1',
    fetch: fetcher,
  }),
  registerRefreshHandler: vi.fn(),
}))
import { usePhotoEditor } from './usePhotoEditor'
import type { Photo } from '@/shared/api/photos'

const trip = '11111111-1111-4111-8111-111111111111'
const epoch = '22222222-2222-4222-8222-222222222222'
const day = '2026-10-06',
  next = '2026-10-07'
const uuid = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`
const photo = (n = 1, overrides: Partial<Photo> = {}): Photo => ({
  id: uuid(n),
  trip_id: trip,
  asset_id: uuid(999),
  version: '9007199254740993',
  recorded_on: day,
  taken_at_local: `${day}T10:00:00`,
  sort_order: n,
  caption: '原说明',
  place_name: '',
  address: '',
  latitude: null,
  longitude: null,
  created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-01T00:00:00Z',
  deleted_at: null,
  ...overrides,
})
let current: Photo
let groups: Record<string, Photo[]>
let requests: Request[]
let failure: number | 'network' | null
let pageHook:
  ((request: Request, body: Record<string, unknown>) => Promise<Response> | Response) | null
function page(scope: string, items: Photo[], cursor: string | null) {
  const all = groups[scope.split('/')[1]!] ?? []
  return {
    kind: 'photo_day',
    scope_id: scope,
    sync_epoch: epoch,
    revision: `sha256:${createHash('sha256')
      .update(`${epoch}\nphoto_day\n${scope}\n${all.map((i) => `${i.id}:${i.version}\n`).join('')}`)
      .digest('hex')}`,
    expires_at: '2026-10-06T01:00:00Z',
    items,
    next_cursor: cursor,
  }
}
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-10-06T00:00:00Z'))
  vi.stubGlobal('crypto', webcrypto)
  vi.stubGlobal('TextEncoder', TextEncoder)
  current = photo()
  groups = { [day]: [current], [next]: [] }
  requests = []
  failure = null
  pageHook = null
  fetcher.mockReset().mockImplementation(async (request: Request) => {
    requests.push(request.clone())
    const url = new URL(request.url)
    if (url.pathname.endsWith('/collection-baselines')) {
      const scope = url.searchParams.get('scope_id')!
      const all = groups[scope.split('/')[1]!] ?? []
      const offset = Number(url.searchParams.get('cursor') ?? 0)
      const limit = Number(url.searchParams.get('limit'))
      const body = page(
        scope,
        all.slice(offset, offset + limit),
        offset + limit < all.length ? String(offset + limit) : null,
      )
      return pageHook ? pageHook(request, body) : Response.json(body)
    }
    if (request.method === 'GET') return Response.json({ data: current })
    if (failure === 'network') throw new TypeError('offline')
    if (failure)
      return Response.json(
        {
          status: failure,
          code:
            failure === 412
              ? 'COLLECTION_CONFLICT'
              : failure === 428
                ? 'COLLECTION_BASE_REQUIRED'
                : 'UNAVAILABLE',
        },
        { status: failure },
      )
    return Response.json({
      data: {
        data: { ...current, version: '9007199254740994' },
        warnings: [],
        scope_revisions: [
          { kind: 'photo_day', scope_id: `${trip}/${day}`, revision: `sha256:${'c'.repeat(64)}` },
        ],
      },
    })
  })
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})
const writes = () => requests.filter((r) => r.method !== 'GET')
async function editing() {
  const editor = usePhotoEditor(trip, () => day)
  await editor.open(current)
  return editor
}
async function signature(request: Request) {
  return {
    body: await request.clone().json(),
    key: request.headers.get('Idempotency-Key'),
    base: request.headers.get('If-Match'),
    guards: request.headers.get('X-Collection-Guards'),
  }
}

describe('照片完整基线编辑', () => {
  it('caption、地点和资产不依赖日期读取；无修改不提交', async () => {
    const e = await editing()
    expect(await e.save()).toBeNull()
    e.draft.caption = '独立说明'
    e.draft.asset_id = uuid(998)
    await e.save()
    expect(requests).toHaveLength(2)
    expect(writes()[0]!.headers.has('X-Collection-Guards')).toBe(false)
    expect(await writes()[0]!.json()).toEqual({ caption: '独立说明', asset_id: uuid(998) })
  })
  it('读取101项最后一页前不允许日期/排序，目标空日需完整读取', async () => {
    groups[day] = Array.from({ length: 101 }, (_, i) => photo(i + 1))
    let release!: () => void
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    pageHook = async (request, body) => {
      if (new URL(request.url).searchParams.has('cursor')) await held
      return Response.json(body)
    }
    const e = await editing()
    const loading = e.prepareSensitive()
    await vi.waitFor(() => expect(requests).toHaveLength(3))
    expect(e.sensitiveReady.value).toBe(false)
    expect(await e.save()).toBeNull()
    release()
    await loading
    expect(e.confirmed.value[0]!.items).toHaveLength(101)
    expect(e.sensitiveReady.value).toBe(true)
    await e.chooseDay(next)
    expect(e.draft.recorded_on).toBe(next)
    await e.save()
    const sig = await signature(writes()[0]!)
    expect(JSON.parse(sig.guards!)).toHaveLength(2)
    expect(sig.base).toBe('"9007199254740993"')
  })
  it.each(['partial', 'expired', 'version', 'epoch', 'duplicate'])(
    '拒绝%s且保留原草稿，说明仍可保存',
    async (kind) => {
      const e = await editing()
      e.draft.caption = '保留'
      if (kind === 'version') groups[day] = [photo(1, { version: '2' })]
      if (kind === 'duplicate') groups[next] = [photo(1, { recorded_on: next })]
      pageHook = (request, body) => {
        if (kind === 'partial') body.next_cursor = 'missing-page'
        if (kind === 'expired') body.expires_at = '2026-10-05T00:00:00Z'
        if (kind === 'epoch' && new URL(request.url).searchParams.get('scope_id')!.endsWith(next)) {
          body.sync_epoch = uuid(888)
          body.revision = `sha256:${createHash('sha256')
            .update(`${body.sync_epoch}\nphoto_day\n${body.scope_id}\n`)
            .digest('hex')}`
        }
        return Response.json(body)
      }
      await e.chooseDay(next)
      expect(e.draft.recorded_on).toBe(day)
      expect(e.draft.caption).toBe('保留')
      expect(e.dateError.value).toBeTruthy()
      expect(writes()).toHaveLength(0)
      await e.save()
      expect(await writes()[0]!.json()).toEqual({ caption: '保留' })
    },
  )
  it('未准备的程序化敏感修改也不能提交', async () => {
    const e = await editing()
    e.draft.sort_order = 5
    await e.save()
    expect(writes()).toHaveLength(0)
    expect(e.error.value).toContain('尚未完整准备')
  })
  it('已捕获基线过期不临时换新revision提交，原草稿保留', async () => {
    const e = await editing()
    await e.prepareSensitive()
    const original = e.confirmed.value
    e.draft.sort_order = 5
    vi.setSystemTime(new Date('2026-10-06T02:00:00Z'))
    await e.save()
    expect(writes()).toHaveLength(0)
    expect(requests).toHaveLength(2)
    expect(e.confirmed.value).toBe(original)
    expect(e.draft.sort_order).toBe(5)
    expect(e.error.value).toContain('过期')
  })
  it('VERSION_CONFLICT的实体latest不能绕过完整候选核对', async () => {
    const e = await editing()
    await e.prepareSensitive()
    e.draft.sort_order = 7
    const transport = fetcher.getMockImplementation()!
    fetcher.mockImplementation(async (request: Request) => {
      if (request.method === 'PATCH') {
        requests.push(request.clone())
        current = photo(1, { version: '9007199254740994' })
        return Response.json({ status: 412, code: 'VERSION_CONFLICT' }, { status: 412 })
      }
      return transport(request)
    })
    await e.save()
    expect(e.latest.value?.version).toBe('9007199254740994')
    expect(e.baseline.value?.version).toBe('9007199254740993')
    expect(e.draft.sort_order).toBe(7)
    await (e.save as (againstLatest: boolean) => Promise<unknown>)(true)
    expect(writes()).toHaveLength(1)
    e.adoptLatest()
    expect(e.baseline.value?.version).toBe('9007199254740993')
  })
  it('核对已移日照片需新实际日完整集合；核对失败不采用', async () => {
    const e = await editing()
    await e.prepareSensitive()
    e.draft.caption = '原草稿'
    current = photo(1, { recorded_on: next, version: '9007199254740994' })
    groups[day] = []
    groups[next] = [current]
    pageHook = () =>
      Response.json({ status: 412, code: 'COLLECTION_BASELINE_CHANGED' }, { status: 412 })
    await e.checkLatest()
    expect(e.candidate.value).toBeNull()
    expect(e.draft.caption).toBe('原草稿')
    pageHook = null
    await e.checkLatest()
    expect(e.draft.recorded_on).toBe(day)
    e.adoptCandidate()
    expect(e.draft.recorded_on).toBe(next)
    expect(e.baseline.value?.version).toBe('9007199254740994')
    expect(e.sensitiveReady.value).toBe(true)
  })
  it('改时间后手选原分组，PATCH仍显式保留同值日期；同日guard去重', async () => {
    const e = await editing()
    await e.prepareSensitive()
    await e.setTaken(`${next}T11:00:00`)
    await e.chooseDay(day)
    await e.save()
    expect(await writes()[0]!.json()).toEqual({
      taken_at_local: `${next}T11:00:00`,
      recorded_on: day,
    })
    expect(JSON.parse(writes()[0]!.headers.get('X-Collection-Guards')!)).toHaveLength(1)
  })
  it('清空拍摄时刻保持分组日期', async () => {
    const e = await editing()
    await e.prepareSensitive()
    await e.setTaken(null)
    await e.save()
    expect(await writes()[0]!.json()).toEqual({ taken_at_local: null, recorded_on: day })
  })
  it.each(['append', 'explicit'] as const)('创建%s模式和重试正文不漂移', async (mode) => {
    const e = usePhotoEditor(trip, () => day)
    await e.open()
    e.draft.asset_id = uuid(999)
    e.orderMode.value = mode
    failure = 'network'
    await e.save()
    const original = await signature(writes()[0]!)
    expect(e.uncertain.value).toBe(true)
    e.orderMode.value = mode === 'append' ? 'explicit' : 'append'
    e.draft.sort_order = 9
    failure = null
    await e.save()
    expect(await signature(writes()[1]!)).toEqual(original)
    expect(original.body.sort_order).toBe(mode === 'append' ? undefined : 0)
    expect(requests.every((r) => r.method === 'POST')).toBe(true)
  })
  it.each(['network', 503] as const)(
    '未知%s冻结原ID/body/guard并禁止核对或关闭',
    async (problem) => {
      const e = await editing()
      await e.prepareSensitive()
      e.draft.sort_order = 7
      failure = problem
      await e.save()
      const original = await signature(writes()[0]!)
      const count = requests.length
      expect(e.uncertainUpdate.value).toBe(true)
      e.close()
      await e.open(photo(2))
      await e.checkLatest()
      await e.chooseDay(next)
      expect(e.opened.value).toBe(true)
      expect(requests).toHaveLength(count)
      e.draft.sort_order = 8
      failure = null
      const outcome = await e.save()
      expect(await signature(writes()[1]!)).toEqual(original)
      expect(outcome?.result.scope_revisions?.[0]?.revision).toBe(`sha256:${'c'.repeat(64)}`)
      expect(e.opened.value).toBe(false)
    },
  )
  it.each([412, 428])('%s保留原草稿/guard，核对不采纳，确认后新意图换号', async (status) => {
    const e = await editing()
    await e.prepareSensitive()
    e.draft.sort_order = 7
    e.draft.caption = '草稿'
    e.draft.asset_id = uuid(998)
    const originalDays = e.confirmed.value
    failure = status
    await e.save()
    const oldKey = writes()[0]!.headers.get('Idempotency-Key')
    expect(e.rejected.value).toBe(true)
    expect(requests).toHaveLength(3)
    await e.save()
    expect(writes()).toHaveLength(1)
    current = photo(1, { version: '9007199254740994', caption: '远端' })
    groups[day] = [current]
    await e.checkLatest()
    expect(e.confirmed.value).toBe(originalDays)
    expect(e.draft.caption).toBe('草稿')
    expect(e.draft.asset_id).toBe(uuid(998))
    e.cancelCandidate()
    expect(e.draft.sort_order).toBe(7)
    await e.checkLatest()
    e.adoptCandidate()
    expect(e.draft.caption).toBe('远端')
    expect(e.rejected.value).toBe(false)
    e.draft.sort_order = 7
    failure = null
    await e.save()
    expect(writes()[1]!.headers.get('Idempotency-Key')).not.toBe(oldKey)
  })

  it.each([401, 403, 412, 428])('未知照片创建/更新后%s仍只允许原样重试', async (status) => {
    for (const kind of ['create', 'update']) {
      const e = usePhotoEditor(trip, () => day)
      await e.open(kind === 'update' ? current : undefined)
      if (kind === 'update') await e.prepareSensitive()
      e.draft.asset_id = uuid(999)
      e.draft.sort_order = 7
      failure = 'network'
      await e.save()
      const start = writes().length - 1
      const original = await signature(writes()[start]!)
      failure = status
      await e.save()
      expect(e.uncertain.value).toBe(true)
      expect(e.locked.value).toBe(true)
      const count = requests.length
      e.close()
      await e.open(photo(2))
      await e.checkLatest()
      await e.chooseDay(next)
      e.adoptCandidate()
      expect(e.opened.value).toBe(true)
      expect(requests).toHaveLength(count)
      e.draft.sort_order = 9
      failure = null
      expect(await e.save()).not.toBeNull()
      expect(await signature(writes()[start + 1]!)).toEqual(original)
      expect(await signature(writes()[start + 2]!)).toEqual(original)
      expect(e.uncertain.value).toBe(false)
      expect(e.opened.value).toBe(false)
    }
  })

  it('新核对失败清旧候选，取消在途核对与关闭后均不可采用', async () => {
    const e = await editing()
    e.draft.caption = '保留草稿'
    const original = e.baseline.value
    await e.checkLatest()
    expect(e.candidate.value).not.toBeNull()
    pageHook = () => Response.json({ status: 503, code: 'UNAVAILABLE' }, { status: 503 })
    await e.checkLatest()
    expect(e.candidate.value).toBeNull()
    e.adoptCandidate()
    expect(e.draft.caption).toBe('保留草稿')
    for (const action of ['cancel', 'close']) {
      pageHook = null
      await e.checkLatest()
      expect(e.candidate.value).not.toBeNull()
      let release!: () => void
      const held = new Promise<void>((resolve) => {
        release = resolve
      })
      pageHook = async (_, body) => {
        await held
        return Response.json(body)
      }
      const checking = e.checkLatest()
      await vi.waitFor(() => expect(e.checking.value).toBe(true))
      if (action === 'cancel') e.cancelCandidate()
      else e.close()
      release()
      await checking
      expect(e.candidate.value).toBeNull()
      e.adoptCandidate()
      expect(e.baseline.value).toBe(original)
      expect(e.draft.caption).toBe('保留草稿')
    }
  })
  it('目标读取失败或关闭后的迟到响应不改变草稿', async () => {
    const e = await editing()
    await e.prepareSensitive()
    let release!: () => void
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    pageHook = async (_, body) => {
      await held
      return Response.json(body)
    }
    const changing = e.chooseDay(next)
    expect(e.draft.recorded_on).toBe(day)
    e.close()
    await e.open()
    e.draft.caption = '新草稿'
    release()
    await changing
    expect(e.draft.recorded_on).toBe(day)
    expect(e.draft.caption).toBe('新草稿')
    expect(e.confirmed.value).toHaveLength(0)
  })
})
