import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { json, problem, receipt } from '@/shared/travel/__tests__/fixtures'
import {
  distributeEvenly,
  formatPercent,
  percentGap,
  percentUnits,
  useTripMembers,
} from '@/shared/travel/useTripMembers'

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

const tripId = '10000000-0000-4000-8000-000000000001'
const guards = [{ kind: 'members', scope_id: tripId, revision: `sha256:${'a'.repeat(64)}` }]
const nextGuards = [{ ...guards[0]!, revision: `sha256:${'b'.repeat(64)}` }]
const self = {
  id: '40000000-0000-4000-8000-000000000001',
  trip_id: tripId,
  name: '我',
  share_percent: '100',
  sort_order: 0,
  is_self: true,
  version: '1',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
  deleted_at: null,
}

describe('百分比工具', () => {
  it('解析与差值', () => {
    expect(percentUnits('33.33')).toBe(3333)
    expect(percentUnits('100')).toBe(10000)
    expect(percentUnits('100.01')).toBeNull()
    expect(percentUnits('1.234')).toBeNull()
    expect(percentUnits('-1')).toBeNull()
    expect(percentGap([{ share_percent: '60' }, { share_percent: '40' }])).toBe(0)
    expect(percentGap([{ share_percent: '60' }, { share_percent: '30.5' }])).toBe(950)
    expect(percentGap([{ share_percent: 'x' }])).toBeNull()
  })

  it('格式化与平均分配', () => {
    expect(formatPercent(950)).toBe('9.5')
    expect(formatPercent(3333)).toBe('33.33')
    expect(formatPercent(10000)).toBe('100')
    expect(distributeEvenly(3)).toEqual(['33.34', '33.33', '33.33'])
    expect(percentGap(distributeEvenly(7).map((share_percent) => ({ share_percent })))).toBe(0)
  })
})

describe('useTripMembers', () => {
  it('加载后只有「我」；添加成员后总和不为 100 不能保存，平均分配后可保存并整体 PUT', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      if (request.method === 'GET') return json(200, { data: [self], scope_revisions: guards })
      return json(200, { data: receipt(null) })
    })
    const m = useTripMembers(tripId)
    await m.open()
    expect(m.rows.length).toBe(1)
    expect(m.rows[0]!.is_self).toBe(true)
    expect(m.canSave.value).toBe(false)

    m.add()
    m.rows[1]!.name = '小王'
    expect(m.gap.value).toBe(0)
    expect(m.canSave.value).toBe(true)
    m.rows[1]!.share_percent = '10'
    expect(m.gap.value).toBe(-1000)
    expect(m.canSave.value).toBe(false)
    m.equalize()
    expect(m.rows.map((r) => r.share_percent)).toEqual(['50', '50'])
    expect(m.canSave.value).toBe(true)

    expect(await m.save()).toBe(true)
    const put = requests.find((r) => r.method === 'PUT')!
    expect(put.url).toContain(`/trips/${tripId}/members`)
    expect(put.headers.get('Idempotency-Key')).toBeTruthy()
    const body = (await put.json()) as { members: Array<{ name: string; share_percent: string }> }
    expect(body.members.map((x) => [x.name, x.share_percent])).toEqual([
      ['我', '50'],
      ['小王', '50'],
    ])
    expect(m.feedback.value).toContain('成员已保存')
  })

  it('「我」不能删除，可以上下移动；名称重复与非法百分比阻止保存', async () => {
    transport.mockResolvedValue(json(200, { data: [self], scope_revisions: guards }))
    const m = useTripMembers(tripId)
    await m.open()
    m.remove(0)
    expect(m.rows.length).toBe(1)
    m.add()
    m.rows[1]!.name = ' 我 '
    m.rows[1]!.share_percent = '0'
    expect(m.invalidRows.value['1.name']).toContain('重复')
    m.rows[1]!.name = '小李'
    m.rows[1]!.share_percent = '1.234'
    expect(m.invalidRows.value['1.share_percent']).toBeTruthy()
    m.move(1, -1)
    expect(m.rows[0]!.name).toBe('小李')
    expect(m.rows[1]!.is_self).toBe(true)
  })

  it('被账目引用的成员删除失败时展示 MEMBER_IN_USE 提示且保留输入', async () => {
    const friend = {
      ...self,
      id: '40000000-0000-4000-8000-000000000002',
      name: '小王',
      share_percent: '0',
      is_self: false,
    }
    transport.mockImplementation(async (request) =>
      request.method === 'GET'
        ? json(200, { data: [self, friend], scope_revisions: guards })
        : problem('MEMBER_IN_USE', 409, { detail: '成员「小王」仍被 2 条账目引用，不能删除' }),
    )
    const m = useTripMembers(tripId)
    await m.open()
    m.remove(1)
    expect(m.canSave.value).toBe(true)
    expect(await m.save()).toBe(false)
    expect(m.error.value).toContain('小王')
    expect(m.rows.length).toBe(1)
  })
})

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

describe('成员基线和恢复', () => {
  it('没有基线时不允许编辑或无头保存', async () => {
    transport.mockResolvedValue(json(200, { data: [self] }))
    const m = useTripMembers(tripId)
    await m.open()
    expect(m.loadError.value).toContain('基线')
    m.add()
    expect(m.rows).toHaveLength(0)
    expect(m.canSave.value).toBe(false)
    expect(await m.save()).toBe(false)
    expect(transport).toHaveBeenCalledTimes(1)
  })

  it.each(['COLLECTION_CONFLICT', 'COLLECTION_BASE_REQUIRED'])(
    '%s保留输入和原基线，显式确认才采用候选',
    async (code) => {
      const puts: Request[] = []
      let latest = false
      transport.mockImplementation(async (request) => {
        if (request.method === 'GET')
          return json(200, {
            data: [{ ...self, name: latest ? '远端' : '我' }],
            scope_revisions: latest ? nextGuards : guards,
          })
        puts.push(request.clone())
        if (puts.length === 1) return problem(code, code === 'COLLECTION_CONFLICT' ? 412 : 428)
        return json(200, { data: receipt(null) })
      })
      const m = useTripMembers(tripId)
      await m.open()
      m.rows[0]!.name = '我的输入'
      expect(await m.save()).toBe(false)
      expect(m.rows[0]!.name).toBe('我的输入')
      expect(m.needsReview.value).toBe(true)
      expect(m.baseline.value!.guards).toEqual(guards)
      expect(transport).toHaveBeenCalledTimes(2)
      expect(await m.save()).toBe(false)
      latest = true
      await m.loadLatest()
      expect(m.latest.value!.members[0]!.name).toBe('远端')
      expect(m.rows[0]!.name).toBe('我的输入')
      expect(m.baseline.value!.guards).toEqual(guards)
      m.adoptLatest()
      expect(m.rows[0]!.name).toBe('远端')
      expect(m.dirty.value).toBe(false)
      m.rows[0]!.name = '核对后修改'
      expect(await m.save()).toBe(true)
      expect(puts[0]!.headers.get('X-Collection-Guards')).toBe(JSON.stringify(guards))
      expect(puts[1]!.headers.get('X-Collection-Guards')).toBe(JSON.stringify(nextGuards))
      expect(puts[1]!.headers.get('Idempotency-Key')).not.toBe(
        puts[0]!.headers.get('Idempotency-Key'),
      )
    },
  )

  it('未知结果关闭重开仍只重试原body、guard、key；禁用修改和重新读取', async () => {
    const puts: Request[] = []
    transport.mockImplementation(async (request) => {
      if (request.method === 'GET') return json(200, { data: [self], scope_revisions: guards })
      puts.push(request.clone())
      if (puts.length === 1) throw new Error('offline')
      return json(200, { data: receipt(null, [], true) })
    })
    const m = useTripMembers(tripId)
    await m.open()
    m.rows[0]!.name = '提交内容'
    expect(await m.save()).toBe(false)
    expect(m.uncertain.value).toBe(true)
    expect(m.editingLocked.value).toBe(true)
    m.add()
    m.remove(0)
    m.move(0, 1)
    m.equalize()
    await m.load()
    await m.loadLatest()
    m.close()
    await m.open()
    expect(transport).toHaveBeenCalledTimes(2)
    // Even an external mutation cannot alter the prepared request.
    m.rows[0]!.name = '不能进入重试'
    expect(await m.save()).toBe(true)
    expect(await puts[1]!.json()).toEqual(await puts[0]!.json())
    expect(puts[1]!.headers.get('Idempotency-Key')).toBe(puts[0]!.headers.get('Idempotency-Key'))
    expect(puts[1]!.headers.get('X-Collection-Guards')).toBe(
      puts[0]!.headers.get('X-Collection-Guards'),
    )
    expect(m.uncertain.value).toBe(false)
  })

  it('已保存但GET失败不会再写，也不会变为未知状态', async () => {
    let gets = 0
    const put = vi.fn()
    transport.mockImplementation(async (request) => {
      if (request.method === 'PUT') {
        put()
        return json(200, { data: receipt(null) })
      }
      if (++gets > 1) throw new Error('refresh offline')
      return json(200, { data: [self], scope_revisions: guards })
    })
    const m = useTripMembers(tripId)
    await m.open()
    m.rows[0]!.name = '已提交'
    expect(await m.save()).toBe(true)
    expect(m.feedback.value).toContain('已保存')
    expect(m.loadError.value).toBeTruthy()
    expect(m.uncertain.value).toBe(false)
    expect(m.baseline.value).toBeNull()
    expect(m.rows[0]!.name).toBe('已提交')
    expect(m.dirty.value).toBe(false)
    expect(await m.save()).toBe(false)
    expect(put).toHaveBeenCalledTimes(1)
  })

  it('核对加载失败保留姓名、比例、排序及原guard', async () => {
    transport
      .mockResolvedValueOnce(json(200, { data: [self], scope_revisions: guards }))
      .mockRejectedValue(new Error('offline'))
    const m = useTripMembers(tripId)
    await m.open()
    m.add()
    m.rows[1]!.name = '朋友'
    m.equalize()
    m.move(1, -1)
    const before = JSON.stringify(m.rows)
    await m.load()
    expect(m.latestError.value).toBeTruthy()
    expect(JSON.stringify(m.rows)).toBe(before)
    expect(m.baseline.value!.guards).toEqual(guards)
  })

  it('关闭重开的迟到GET不能覆盖新一代内容', async () => {
    const late = deferred<Response>()
    transport
      .mockReturnValueOnce(late.promise)
      .mockResolvedValue(
        json(200, { data: [{ ...self, name: '新一代' }], scope_revisions: nextGuards }),
      )
    const m = useTripMembers(tripId)
    const old = m.open()
    m.close()
    await m.open()
    late.resolve(json(200, { data: [self], scope_revisions: guards }))
    await old
    expect(m.rows[0]!.name).toBe('新一代')
    expect(m.baseline.value!.guards).toEqual(nextGuards)
  })

  it('保存进行中不能关闭、重开、再次提交或让load覆盖输入', async () => {
    const write = deferred<Response>()
    transport.mockImplementation(async (request) =>
      request.method === 'PUT'
        ? write.promise
        : json(200, { data: [self], scope_revisions: guards }),
    )
    const m = useTripMembers(tripId)
    await m.open()
    m.rows[0]!.name = '提交'
    const saving = m.save()
    m.close()
    await m.open()
    await m.load()
    await m.loadLatest()
    expect(m.opened.value).toBe(true)
    expect(await m.save()).toBe(false)
    expect(transport).toHaveBeenCalledTimes(2)
    write.resolve(json(200, { data: receipt(null) }))
    expect(await saving).toBe(true)
  })
})
