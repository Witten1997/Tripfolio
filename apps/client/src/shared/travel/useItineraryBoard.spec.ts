import { createHash, webcrypto } from 'node:crypto'
import { TextEncoder } from 'node:util'
import { createPinia, setActivePinia } from 'pinia'
import { effectScope, ref, type EffectScope } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ItineraryItem, ItineraryReorder } from '@/shared/api/itinerary'
import { deferred, json, problem } from '@/shared/travel/__tests__/fixtures'
import { useItineraryBoard, type TripSpan } from '@/shared/travel/useItineraryBoard'

const { transport } = vi.hoisted(() => ({
  transport: vi.fn<(request: Request) => Promise<Response>>(),
}))
vi.mock('@/shared/api/client', async (original) => {
  const module = await original<typeof import('@/shared/api/client')>()
  return {
    ...module,
    api: module.createApiClient({
      baseUrl: 'http://api.test/api/v1',
      fetch: transport,
      refresh: async () => false,
    }),
  }
})

const trip = '11111111-1111-4111-8111-111111111111'
const epoch = '22222222-2222-4222-8222-222222222222'
const otherEpoch = '33333333-3333-4333-8333-333333333333'
const day1 = '2026-10-01',
  day2 = '2026-10-02',
  day3 = '2026-10-03',
  outside = '2026-10-20'
function item(n: number, scheduled_on = day1, sort_order = n - 1): ItineraryItem {
  return {
    id: `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`,
    trip_id: trip,
    title: `行程${n}`,
    kind: 'other',
    scheduled_on,
    sort_order,
    poi_id: '',
    footprint_excluded: false,
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
    version: '9007199254740993',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    deleted_at: null,
  }
}
let rows: ItineraryItem[], discovery: ItineraryItem[] | undefined, requests: Request[]
let getOverride: ((request: Request) => Response | Promise<Response> | undefined) | undefined
let postOverride: ((request: Request) => Promise<Response>) | undefined
let scopes: EffectScope[]
function baselinePage(request: Request, sync_epoch = epoch) {
  const query = new URL(request.url).searchParams
  const scope_id = query.get('scope_id')!
  const all = rows
    .filter((row) => row.scheduled_on === scope_id.split('/')[1])
    .sort((a, b) => a.id.localeCompare(b.id))
  const revision = `sha256:${createHash('sha256')
    .update(
      `${sync_epoch}\nitinerary_day\n${scope_id}\n${all.map((row) => `${row.id}:${row.version}\n`).join('')}`,
    )
    .digest('hex')}`
  const offset = Number(query.get('cursor') ?? 0),
    limit = Number(query.get('limit'))
  return {
    kind: 'itinerary_day',
    scope_id,
    sync_epoch,
    revision,
    expires_at: '2026-10-05T00:10:00Z',
    items: all.slice(offset, offset + limit),
    next_cursor: offset + limit < all.length ? String(offset + limit) : null,
  }
}
function success() {
  return json(200, {
    data: {
      operation_id: 'success',
      primary: null,
      affected: [],
      commit_cursor: null,
      warnings: [],
      replayed: false,
      data: null,
    },
  })
}
async function respond(request: Request): Promise<Response> {
  requests.push(request.clone())
  if (request.method === 'POST') {
    if (postOverride) return postOverride(request)
    const body = (await request.json()) as ItineraryReorder
    rows = rows.map((row) => {
      const day = body.days.find((day) => day.items.some((i) => i.id === row.id))
      return day
        ? {
            ...row,
            scheduled_on: day.date,
            sort_order: day.items.findIndex((i) => i.id === row.id),
            version: (BigInt(row.version) + 1n).toString(),
          }
        : row
    })
    return success()
  }
  const override = getOverride?.(request)
  if (override) return override
  return new URL(request.url).pathname.endsWith('/collection-baselines')
    ? json(200, baselinePage(request))
    : json(200, { items: discovery ?? rows, next_cursor: null })
}
function board(span = ref<TripSpan>({ start: day1, end: day3 })) {
  const scope = effectScope()
  scopes.push(scope)
  return { model: scope.run(() => useItineraryBoard(trip, () => span.value))!, scope, span }
}
const posts = () => requests.filter((request) => request.method === 'POST')
beforeEach(() => {
  setActivePinia(createPinia())
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-10-05T00:00:00Z'))
  vi.stubGlobal('crypto', webcrypto)
  vi.stubGlobal('TextEncoder', TextEncoder)
  rows = [item(1), item(2), item(3, day2, 0)]
  discovery = undefined
  requests = []
  scopes = []
  getOverride = undefined
  postOverride = undefined
  transport.mockReset().mockImplementation(respond)
})
afterEach(() => {
  scopes.forEach((scope) => scope.stop())
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('完整基线行程看板', () => {
  it('普通列表只发现日期，采用完整集合的资源并包含空日和区间外日期', async () => {
    rows.push(item(4, outside, 0))
    discovery = rows.map((row) => ({ ...row, version: '1', title: '陈旧展示' }))
    const { model } = board()
    const loaded = await model.reload()
    expect(loaded, model.error.value ?? '').toBe(true)
    expect(model.days.value.map((day) => day.date)).toEqual([day1, day2, day3, outside])
    expect(model.items.value[0]!.title).toBe('行程1')
    expect(model.items.value[0]!.version).toBe('9007199254740993')
    expect(model.confirmed.value!.collections[day3]!.items).toEqual([])
    expect(model.canReorder.value).toBe(true)
  })

  it('101条跨页终页完成前不安装半集合、不允许拖动', async () => {
    rows = Array.from({ length: 101 }, (_, i) => item(i + 1))
    const terminal = deferred<Response>()
    getOverride = (request) =>
      new URL(request.url).searchParams.get('cursor') === '100' ? terminal.promise : undefined
    const { model } = board()
    const loading = model.reload()
    await vi.waitFor(() =>
      expect(requests.some((r) => new URL(r.url).searchParams.get('cursor') === '100')).toBe(true),
    )
    expect(model.items.value).toHaveLength(0)
    expect(await model.moveItem(rows[0]!.id, day2, 0)).toBe(false)
    expect(posts()).toHaveLength(0)
    terminal.resolve(
      json(
        200,
        baselinePage(requests.find((r) => new URL(r.url).searchParams.get('cursor') === '100')!),
      ),
    )
    expect(await loading).toBe(true)
    expect(model.items.value).toHaveLength(101)
  })

  it.each(['HTTP', 'digest', 'epoch', 'duplicate'] as const)(
    '%s错误不降级到普通列表或部分日期',
    async (mode) => {
      if (mode === 'duplicate') rows.push({ ...rows[0]!, scheduled_on: day2 })
      getOverride = (request) => {
        if (!request.url.includes('collection-baselines')) return undefined
        if (mode === 'HTTP') return problem('COLLECTION_BASELINE_CHANGED', 412)
        const page = baselinePage(
          request,
          mode === 'epoch' && request.url.includes(day2) ? otherEpoch : epoch,
        )
        if (mode === 'digest') page.items = []
        return json(200, page)
      }
      const { model } = board()
      expect(await model.reload()).toBe(false)
      expect(model.confirmed.value).toBeNull()
      expect(model.canReorder.value).toBe(false)
      expect(posts()).toHaveLength(0)
    },
  )

  it('跨日提交原完整成员、两个guard与精确版本，成功后才安装新完整基线', async () => {
    const { model } = board()
    await model.reload()
    const old = model.confirmed.value!
    expect(await model.moveItem(item(1).id, day2, 0)).toBe(true)
    const sent = posts()[0]!
    expect(await sent.json()).toEqual({
      days: [
        { date: day1, items: [{ id: item(2).id, base_version: '9007199254740993' }] },
        {
          date: day2,
          items: [1, 3].map((n) => ({ id: item(n).id, base_version: '9007199254740993' })),
        },
      ],
    })
    expect(JSON.parse(sent.headers.get('X-Collection-Guards')!)).toEqual(
      [day1, day2].flatMap((day) => old.collections[day]!.baseline.guards),
    )
    expect(sent.headers.get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    expect(model.confirmed.value).not.toBe(old)
    expect(model.hasPending.value).toBe(false)
    expect(model.items.value.find((row) => row.id === item(1).id)!.version).toBe('9007199254740994')
  })

  it('区间外来源移空与原空目标也必须携带两日基线；未展示目标拒绝', async () => {
    rows = [item(1, outside, 0)]
    const { model } = board()
    await model.reload()
    expect(await model.moveItem(item(1).id, '2026-10-21', 0)).toBe(false)
    expect(await model.moveItem(item(1).id, day3, 0)).toBe(true)
    expect(await posts()[0]!.json()).toEqual({
      days: [
        { date: outside, items: [] },
        { date: day3, items: [{ id: item(1).id, base_version: '9007199254740993' }] },
      ],
    })
    expect(
      JSON.parse(posts()[0]!.headers.get('X-Collection-Guards')!).map(
        (g: { scope_id: string }) => g.scope_id,
      ),
    ).toEqual([`${trip}/${day3}`, `${trip}/${outside}`])
  })

  it('同位置不请求，同日移动只有一个scope', async () => {
    const { model } = board()
    await model.reload()
    expect(await model.moveItem(item(1).id, day1, 0)).toBe(false)
    expect(posts()).toHaveLength(0)
    await model.moveItem(item(2).id, day1, 0)
    expect(JSON.parse(posts()[0]!.headers.get('X-Collection-Guards')!)).toHaveLength(1)
  })

  it.each([
    ['COLLECTION_CONFLICT', 412],
    ['VERSION_CONFLICT', 412],
    ['COLLECTION_BASE_REQUIRED', 428],
    ['ORDER_CHANGED', 409],
  ] as const)('%s保留草稿和原基线，不自动读取或重提', async (code, status) => {
    const { model } = board()
    await model.reload()
    const old = model.confirmed.value,
      reads = requests.length
    postOverride = async () => problem(code, status)
    await model.moveItem(item(1).id, day2, 0)
    expect(requests).toHaveLength(reads + 1)
    expect(model.confirmed.value).toBe(old)
    expect(model.days.value.find((day) => day.date === day2)!.items.map((i) => i.id)).toEqual([
      item(1).id,
      item(3).id,
    ])
    expect(model.hasPending.value).toBe(true)
    expect(model.canReorder.value).toBe(false)
    expect(await model.reload()).toBe(false)
    expect(await model.moveItem(item(2).id, day2, 0)).toBe(false)
    expect(requests).toHaveLength(reads + 1)
  })

  it('手动核对只装候选；采用后才能新排序，基线变化更换意图', async () => {
    const { model } = board()
    await model.reload()
    const old = model.confirmed.value
    postOverride = async () => problem('COLLECTION_CONFLICT', 412)
    await model.moveItem(item(1).id, day2, 0)
    const draft = model.draft.value,
      firstKey = posts()[0]!.headers.get('Idempotency-Key')
    rows[1] = { ...rows[1]!, version: '9007199254740994', title: '最新内容' }
    await model.loadLatest()
    expect(model.confirmed.value).toBe(old)
    expect(model.draft.value).toBe(draft)
    expect(model.latest.value!.items.find((i) => i.id === item(2).id)!.title).toBe('最新内容')
    expect(posts()).toHaveLength(1)
    expect(model.adoptLatest()).toBe(true)
    await model.moveItem(item(1).id, day2, 0)
    expect(posts()[1]!.headers.get('Idempotency-Key')).not.toBe(firstKey)
    expect(posts()[1]!.headers.get('X-Collection-Guards')).not.toBe(
      posts()[0]!.headers.get('X-Collection-Guards'),
    )
  })

  it.each(['network', '500'])('未知%s结果只原样重试，阻止刷新/核对/新拖动', async (failure) => {
    const { model } = board()
    await model.reload()
    postOverride = async () => {
      if (failure === 'network') throw new TypeError('offline')
      return problem('INTERNAL_ERROR', 500)
    }
    await model.moveItem(item(1).id, day2, 0)
    const count = requests.length
    expect(model.uncertain.value).toBe(true)
    expect(Object.isFrozen(model.draft.value)).toBe(true)
    expect(await model.loadLatest()).toBe(false)
    expect(await model.reload()).toBe(false)
    expect(model.adoptLatest()).toBe(false)
    expect(requests).toHaveLength(count)
    postOverride = async () => success()
    await model.retryPending()
    expect(posts()[1]!.headers.get('Idempotency-Key')).toBe(
      posts()[0]!.headers.get('Idempotency-Key'),
    )
    expect(posts()[1]!.headers.get('X-Collection-Guards')).toBe(
      posts()[0]!.headers.get('X-Collection-Guards'),
    )
    expect(await posts()[1]!.json()).toEqual(await posts()[0]!.json())
    expect(model.hasPending.value).toBe(false)
  })

  it('在途写入禁止并发拖动，成功后读取失败保持已保存状态且不再次写入', async () => {
    const { model } = board()
    await model.reload()
    const response = deferred<Response>()
    postOverride = () => response.promise
    const saving = model.moveItem(item(1).id, day2, 0)
    expect(await model.moveItem(item(2).id, day2, 0)).toBe(false)
    getOverride = () => problem('DEPENDENCY_UNAVAILABLE', 503)
    response.resolve(success())
    expect(await saving).toBe(true)
    expect(model.actionFailure.value).toContain('已保存')
    expect(model.savedNeedsReload.value).toBe(true)
    expect(model.hasPending.value).toBe(false)
    expect(model.canReorder.value).toBe(false)
    expect(await model.retryPending()).toBe(false)
    expect(posts()).toHaveLength(1)
  })

  it('未知提交的重试被拒绝后仍不允许换基线，直到原请求确认成功', async () => {
    const { model } = board()
    await model.reload()
    postOverride = async () => {
      throw new TypeError('offline')
    }
    await model.moveItem(item(1).id, day2, 0)
    postOverride = async () => problem('UNAUTHORIZED', 401)
    await model.retryPending()
    expect(model.uncertain.value).toBe(true)
    expect(await model.loadLatest()).toBe(false)
    expect(model.adoptLatest()).toBe(false)
    postOverride = async () => success()
    expect(await model.retryPending()).toBe(true)
    expect(model.uncertain.value).toBe(false)
    expect(posts()).toHaveLength(3)
    for (const sent of posts().slice(1)) {
      expect(sent.headers.get('Idempotency-Key')).toBe(posts()[0]!.headers.get('Idempotency-Key'))
      expect(sent.headers.get('X-Collection-Guards')).toBe(
        posts()[0]!.headers.get('X-Collection-Guards'),
      )
      expect(await sent.json()).toEqual(await posts()[0]!.clone().json())
    }
  })

  it('再次核对失败不允许采用旧候选，卸载后也不得采用', async () => {
    const { model, scope } = board()
    await model.reload()
    postOverride = async () => problem('COLLECTION_CONFLICT', 412)
    await model.moveItem(item(1).id, day2, 0)
    const old = model.confirmed.value,
      draft = model.draft.value
    expect(await model.loadLatest()).toBe(true)
    getOverride = () => problem('DEPENDENCY_UNAVAILABLE', 503)
    expect(await model.loadLatest()).toBe(false)
    expect(model.latest.value).toBeNull()
    expect(model.adoptLatest()).toBe(false)
    expect(model.confirmed.value).toBe(old)
    expect(model.draft.value).toBe(draft)
    getOverride = undefined
    expect(await model.loadLatest()).toBe(true)
    scope.stop()
    expect(model.adoptLatest()).toBe(false)
  })

  it('重新读取失败保留旧展示但不允许继续编辑', async () => {
    const { model } = board()
    await model.reload()
    const old = model.confirmed.value
    getOverride = () => problem('DEPENDENCY_UNAVAILABLE', 503)
    await model.reload()
    expect(model.confirmed.value).toBe(old)
    expect(model.canReorder.value).toBe(false)
  })

  it('迟到读取和卸载不会覆盖新基线', async () => {
    const { model, scope } = board()
    const slow = deferred<Response>()
    getOverride = () => slow.promise
    const first = model.reload()
    getOverride = undefined
    await model.reload()
    const newer = model.confirmed.value
    slow.resolve(json(200, { items: [], next_cursor: null }))
    await first
    expect(model.confirmed.value).toBe(newer)
    const late = deferred<Response>()
    getOverride = () => late.promise
    const closing = model.reload()
    scope.stop()
    late.resolve(json(200, { items: [], next_cursor: null }))
    await closing
    expect(model.confirmed.value).toBe(newer)
    expect(await model.moveItem(item(1).id, day2, 0)).toBe(false)
  })

  it('待处理排序时旅行范围变化不读取或覆盖草稿', async () => {
    const { model, span } = board()
    await model.reload()
    postOverride = async () => problem('COLLECTION_CONFLICT', 412)
    await model.moveItem(item(1).id, day2, 0)
    const old = model.confirmed.value,
      draft = model.draft.value,
      count = requests.length
    span.value = { start: day2, end: outside }
    expect(requests).toHaveLength(count)
    expect(model.confirmed.value).toBe(old)
    expect(model.draft.value).toBe(draft)
    expect(await model.moveItem(item(1).id, outside, 0)).toBe(false)
  })
})
