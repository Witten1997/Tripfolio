import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { TripCreate } from '@/shared/api/trips'
import { useMetadataStore } from '@/shared/stores/metadata'
import { changedTripFields, draftFromTrip, validateTripDraft } from '@/shared/travel/tripDraft'
import { useTripEditor } from '@/shared/travel/useTripEditor'
import {
  deferred,
  json,
  metadata,
  problem,
  receipt,
  trip,
} from '@/shared/travel/__tests__/fixtures'

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

function readyMetadata() {
  const store = useMetadataStore()
  store.metadata = metadata
  store.status = 'ready'
}

beforeEach(() => {
  setActivePinia(createPinia())
  transport.mockReset()
  readyMetadata()
})

describe('旅行草稿和契约边界', () => {
  it('预算清空只 PATCH 显式 null，并携带引号版本与 UUID 操作号', async () => {
    const base = trip({ archived_at: '2026-09-01T00:00:00Z' })
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      return request.method === 'GET'
        ? json(200, { data: base })
        : json(200, { data: receipt(trip({ budget_amount: null, version: '4' })) })
    })
    const editor = useTripEditor()
    await editor.open(base)
    expect(editor.dirty.value).toBe(false)
    editor.draft.budget_amount = ''
    await editor.save()
    expect(await requests[1]!.json()).toEqual({ budget_amount: null })
    expect(requests[1]!.headers.get('If-Match')).toBe('"3"')
    expect(requests[1]!.headers.get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    expect(editor.opened.value).toBe(false)
  })

  it('零预算与未设置不同，精确金额不经过浮点数', () => {
    const base = trip({ budget_amount: null })
    const draft = draftFromTrip(base)
    draft.budget_amount = '0'
    expect(changedTripFields(validateTripDraft(draft, metadata), base)).toEqual({
      budget_amount: '0.00',
    })
    draft.budget_amount = '99999999999999.99'
    expect(validateTripDraft(draft, metadata).budget_amount).toBe('99999999999999.99')
    draft.budget_amount = '5.000'
    expect(() => validateTripDraft(draft, metadata)).toThrow('小数位不能超过 2 位')
    draft.currency_code = 'JPY'
    draft.budget_amount = '0.0'
    expect(() => validateTripDraft(draft, metadata)).toThrow('小数位不能超过 0 位')
    draft.currency_code = 'KWD'
    draft.budget_amount = '0.001'
    expect(validateTripDraft(draft, metadata).budget_amount).toBe('0.001')
  })

  it('元数据未成功加载时不以默认精度提交', async () => {
    const editor = useTripEditor()
    await editor.open()
    Object.assign(editor.draft, draftFromTrip(trip()))
    useMetadataStore().status = 'error'
    expect(await editor.save()).toBeNull()
    expect(editor.errors.value.currency_code).toContain('加载币种')
    expect(transport).not.toHaveBeenCalled()
  })

  it('无效日期和结束早于开始在请求前被拦截', () => {
    const draft = draftFromTrip(trip())
    draft.start_date = '2026-02-30'
    expect(() => validateTripDraft(draft, metadata)).toThrow('开始日期')
    draft.start_date = '2026-10-09'
    expect(() => validateTripDraft(draft, metadata)).toThrow('结束日期不能早于')
  })

  it('创建已提交但响应丢失时锁定入口，草稿被程序改变仍重放原请求且不重复创建', async () => {
    const requests: Request[] = []
    let committed: { body: TripCreate; key: string | null } | null = null
    let creations = 0
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      const body = (await request.json()) as TripCreate
      const key = request.headers.get('Idempotency-Key')
      if (!committed) {
        creations++
        committed = { body, key }
        throw new TypeError('response lost after commit')
      }
      if (key !== committed.key || JSON.stringify(body) !== JSON.stringify(committed.body)) {
        return problem('ID_ALREADY_USED', 409)
      }
      return json(201, { data: receipt(trip({ ...committed.body }), [], true) })
    })
    const editor = useTripEditor()
    await editor.open()
    Object.assign(editor.draft, draftFromTrip(trip({ budget_amount: null })))
    await editor.save()
    expect(editor.opened.value).toBe(true)
    expect(editor.uncertainCreate.value).toBe(true)
    expect(editor.draft.name).toBe('秋日杭州')
    editor.draft.name = ''
    editor.draft.budget_amount = 'invalid'
    useMetadataStore().status = 'error'
    await editor.open(trip({ name: '不应切换到这趟旅行' }))
    await editor.load()
    editor.close()
    expect(editor.opened.value).toBe(true)
    expect(editor.baseline.value).toBeNull()
    expect(editor.draft.name).toBe('')
    const outcome = await editor.save()
    const bodies = await Promise.all(requests.map((request) => request.json()))
    expect(requests).toHaveLength(2)
    expect(bodies[0].id).toMatch(/^[0-9a-f-]{36}$/)
    expect(bodies[0]).toEqual(bodies[1])
    expect(requests[0]!.headers.get('Idempotency-Key')).toBe(
      requests[1]!.headers.get('Idempotency-Key'),
    )
    expect(creations).toBe(1)
    expect(outcome?.resource?.name).toBe('秋日杭州')
    expect(outcome?.result.replayed).toBe(true)
    expect(editor.uncertainCreate.value).toBe(false)
    expect(editor.opened.value).toBe(false)
  })

  it('创建收到 5xx 时保留原请求并允许原样重试', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      return requests.length === 1
        ? problem('INTERNAL_ERROR', 503)
        : json(201, { data: receipt(trip()) })
    })
    const editor = useTripEditor()
    await editor.open()
    Object.assign(editor.draft, draftFromTrip(trip()))
    await editor.save()
    expect(editor.uncertainCreate.value).toBe(true)
    editor.draft.name = '后来改变的草稿'
    await editor.save()
    expect(await requests[0]!.json()).toEqual(await requests[1]!.json())
    expect(requests[0]!.headers.get('Idempotency-Key')).toBe(
      requests[1]!.headers.get('Idempotency-Key'),
    )
  })

  it.each([422, 409])('明确的 %i 创建错误仍允许修改草稿后重新提交', async (status) => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      return requests.length === 1
        ? problem('VALIDATION_FAILED', status)
        : json(201, { data: receipt(trip()) })
    })
    const editor = useTripEditor()
    await editor.open()
    Object.assign(editor.draft, draftFromTrip(trip()))
    await editor.save()
    expect(editor.uncertainCreate.value).toBe(false)
    editor.draft.name = '修正后的名称'
    await editor.save()
    const first = await requests[0]!.json()
    const second = await requests[1]!.json()
    expect(second.id).toBe(first.id)
    expect(second.name).toBe('修正后的名称')
    expect(requests[0]!.headers.get('Idempotency-Key')).not.toBe(
      requests[1]!.headers.get('Idempotency-Key'),
    )
  })

  it('普通 PATCH 结果不明时仍可修改草稿，并用新操作号提交改动', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      if (request.method === 'GET') return json(200, { data: trip() })
      requests.push(request.clone())
      if (requests.length === 1) throw new TypeError('network')
      return json(200, { data: receipt(trip({ name: '第二次修改', version: '4' })) })
    })
    const editor = useTripEditor()
    await editor.open(trip())
    editor.draft.name = '第一次修改'
    await editor.save()
    expect(editor.uncertainCreate.value).toBe(false)
    editor.draft.name = '第二次修改'
    await editor.save()
    expect(await requests[1]!.json()).toEqual({ name: '第二次修改' })
    expect(requests[0]!.headers.get('Idempotency-Key')).not.toBe(
      requests[1]!.headers.get('Idempotency-Key'),
    )
  })

  it('提交中的重复点击只发出一次创建请求', async () => {
    const pending = deferred<Response>()
    transport.mockReturnValue(pending.promise)
    const editor = useTripEditor()
    await editor.open()
    Object.assign(editor.draft, draftFromTrip(trip()))
    const first = editor.save()
    await editor.open(trip({ name: '不应切换' }))
    await editor.load()
    editor.close()
    expect(editor.isEditing.value).toBe(false)
    expect(editor.opened.value).toBe(true)
    expect(await editor.save()).toBeNull()
    pending.resolve(json(201, { data: receipt(trip()) }))
    await first
    expect(transport).toHaveBeenCalledTimes(1)
  })

  it('冲突保留草稿；明确重新提交时仅覆盖主动修改字段，保留最新的无关字段', async () => {
    const base = trip()
    const latest = trip({
      version: '4',
      name: '另一台设备的名称',
      destination: '新的目的地',
      budget_amount: '6000.00',
    })
    const writes: Request[] = []
    let gets = 0
    transport.mockImplementation(async (request) => {
      if (request.method === 'GET') return json(200, { data: gets++ === 0 ? base : latest })
      writes.push(request.clone())
      if (writes.length === 1) return problem('VERSION_CONFLICT', 412)
      return json(200, { data: receipt({ ...latest, name: '我的名称', version: '5' }) })
    })
    const editor = useTripEditor()
    await editor.open(base)
    editor.draft.name = '我的名称'
    await editor.save()
    expect(editor.conflict.value).toBe(true)
    expect(editor.latest.value?.destination).toBe('新的目的地')
    expect(editor.draft.destination).toBe('杭州')
    expect(editor.draft.name).toBe('我的名称')
    expect(editor.baseline.value?.version).toBe('3')
    expect(await editor.save()).toBeNull()
    expect(writes).toHaveLength(1)
    await editor.save(true)
    expect(await writes[1]!.json()).toEqual({ name: '我的名称' })
    expect(writes[1]!.headers.get('If-Match')).toBe('"4"')
    expect(writes[1]!.headers.get('Idempotency-Key')).not.toBe(
      writes[0]!.headers.get('Idempotency-Key'),
    )
  })

  it('重放资源为 null 仍保留成功回执与日期/时区警告', async () => {
    const warnings = ['ITINERARY_OUTSIDE_TRIP_DATES', 'TIMEZONE_INTERPRETATION_CHANGED']
    transport.mockImplementation(async (request) =>
      request.method === 'GET'
        ? json(200, { data: trip() })
        : json(200, { data: receipt(null, warnings, true) }),
    )
    const editor = useTripEditor()
    await editor.open(trip())
    editor.draft.end_date = '2026-10-05'
    const result = await editor.save()
    expect(result?.resource).toBeNull()
    expect(result?.result.warnings).toEqual(warnings)
    expect(result?.result.replayed).toBe(true)
    expect(editor.opened.value).toBe(false)
  })

  it('更改币种遇到已有金额时保留输入并提供下一步', async () => {
    transport.mockImplementation(async (request) =>
      request.method === 'GET' ? json(200, { data: trip() }) : problem('CURRENCY_AMOUNTS_EXIST'),
    )
    const editor = useTripEditor()
    await editor.open(trip())
    editor.draft.currency_code = 'KWD'
    await editor.save()
    expect(editor.error.value).toContain('清空总预算并保存')
    expect(editor.error.value).toContain('预计费用')
    expect(editor.draft.currency_code).toBe('KWD')
    expect(editor.opened.value).toBe(true)
  })

  it('服务端字段错误保留对应字段与文案', async () => {
    transport.mockImplementation(async (request) =>
      request.method === 'GET'
        ? json(200, { data: trip() })
        : problem('VALIDATION_FAILED', 422, {
            errors: [
              { field: 'timezone', code: 'INVALID_TIMEZONE', message: '服务器不支持此时区' },
            ],
          }),
    )
    const editor = useTripEditor()
    await editor.open(trip())
    editor.draft.timezone = 'Asia/Tokyo'
    await editor.save()
    expect(editor.errors.value.timezone).toBe('服务器不支持此时区')
    expect(editor.error.value).toBe('服务器不支持此时区')
  })
})
