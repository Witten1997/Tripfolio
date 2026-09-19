import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { CategoryCreate } from '@/shared/api/categories'
import { useMetadataStore } from '@/shared/stores/metadata'
import { useCategoryManager } from '@/shared/travel/useCategoryManager'
import {
  category,
  deferred,
  json,
  metadata,
  problem,
  receipt,
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

beforeEach(() => {
  setActivePinia(createPinia())
  transport.mockReset()
  useMetadataStore().metadata = metadata
  useMetadataStore().status = 'ready'
})

describe('账号共用分类管理', () => {
  it('列表读取 data 数组，以排序值和固定 id 处理并列顺序', async () => {
    transport.mockResolvedValue(
      json(200, {
        data: [
          category({ id: 'b', sort_order: 5 }),
          category({ id: 'a', sort_order: 5 }),
          category({ id: 'c', sort_order: 1 }),
        ],
      }),
    )
    const manager = useCategoryManager()
    await manager.open()
    expect(manager.items.value.map((item) => item.id)).toEqual(['c', 'a', 'b'])
  })

  it('预设分类可以改图标，只 PATCH 发生变化的字段，图标清空为 null', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      return request.method === 'GET'
        ? json(200, { data: [category()] })
        : json(200, { data: receipt(category({ icon: null, sort_order: 0, version: '3' })) })
    })
    const manager = useCategoryManager()
    await manager.open()
    manager.start(category())
    manager.draft.icon = ''
    expect(await manager.save()).toBe(true)
    const patch = requests.find((request) => request.method === 'PATCH')!
    expect(await patch.json()).toEqual({ icon: null })
    expect(patch.headers.get('If-Match')).toBe('"2"')
    expect(patch.headers.get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    expect(manager.active.value).toBe(false)
  })

  it('删除必须确认，CATEGORY_IN_USE 不移除项目并提供可执行的说明', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      return request.method === 'DELETE'
        ? problem('CATEGORY_IN_USE')
        : json(200, { data: [category()] })
    })
    const manager = useCategoryManager()
    await manager.open()
    expect(await manager.remove(category(), false)).toBe(false)
    expect(requests.some((request) => request.method === 'DELETE')).toBe(false)
    expect(await manager.remove(category(), true)).toBe(false)
    const deletion = requests.find((request) => request.method === 'DELETE')!
    expect(deletion.headers.get('If-Match')).toBe('"2"')
    expect(deletion.headers.get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    expect(manager.items.value).toHaveLength(1)
    expect(manager.error.value).toContain('账目使用')
    expect(manager.error.value).toContain('可以改名')
  })

  it('创建已提交但响应丢失后锁定管理入口，原样重放不会重复创建', async () => {
    const requests: Request[] = []
    let committed: { body: CategoryCreate; key: string | null } | null = null
    let creations = 0
    transport.mockImplementation(async (request) => {
      if (request.method === 'GET')
        return json(200, { data: [category(), ...(committed ? [category(committed.body)] : [])] })
      requests.push(request.clone())
      const body = (await request.json()) as CategoryCreate
      const key = request.headers.get('Idempotency-Key')
      if (!committed) {
        creations++
        committed = { body, key }
        throw new TypeError('response lost after commit')
      }
      if (key !== committed.key || JSON.stringify(body) !== JSON.stringify(committed.body)) {
        return problem('ID_ALREADY_USED', 409)
      }
      return json(201, { data: receipt(category(committed.body), [], true) })
    })
    const manager = useCategoryManager()
    await manager.open()
    manager.start()
    manager.draft.name = '咖啡'
    manager.draft.icon = 'food'
    expect(await manager.save()).toBe(false)
    expect(manager.uncertainCreate.value).toBe(true)
    expect(manager.draft.name).toBe('咖啡')
    manager.draft.name = '后来修改的草稿'
    manager.draft.icon = 'unsupported'
    useMetadataStore().status = 'error'
    manager.start()
    manager.start(category())
    await manager.open()
    await manager.load()
    expect(await manager.remove(category(), true)).toBe(false)
    manager.close()
    expect(manager.opened.value).toBe(true)
    expect(manager.active.value).toBe(true)
    expect(manager.baseline.value).toBeNull()
    expect(manager.draft.name).toBe('后来修改的草稿')
    expect(await manager.save()).toBe(true)
    const [first, second] = await Promise.all(requests.map((request) => request.json()))
    expect(first).toEqual(second)
    expect(first).toMatchObject({ name: '咖啡', icon: 'food' })
    expect(first).not.toHaveProperty('sort_order')
    expect(first.id).toMatch(/^[0-9a-f-]{36}$/)
    expect(requests[0]!.headers.get('Idempotency-Key')).toBe(
      requests[1]!.headers.get('Idempotency-Key'),
    )
    expect(creations).toBe(1)
    expect(manager.uncertainCreate.value).toBe(false)
    expect(manager.active.value).toBe(false)
    expect(manager.items.value.some((item) => item.name === '咖啡')).toBe(true)
  })

  it('创建收到 5xx 时仍锁定原请求快照', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      if (request.method === 'GET') return json(200, { data: [] })
      requests.push(request.clone())
      return requests.length === 1
        ? problem('INTERNAL_ERROR', 503)
        : json(201, { data: receipt(category()) })
    })
    const manager = useCategoryManager()
    await manager.open()
    manager.start()
    manager.draft.name = '咖啡'
    await manager.save()
    expect(manager.uncertainCreate.value).toBe(true)
    manager.draft.name = '后来改变的草稿'
    await manager.save()
    expect(await requests[0]!.json()).toEqual(await requests[1]!.json())
    expect(requests[0]!.headers.get('Idempotency-Key')).toBe(
      requests[1]!.headers.get('Idempotency-Key'),
    )
  })

  it.each([422, 409])('明确的 %i 创建错误允许修改后重试', async (status) => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      if (request.method === 'GET') return json(200, { data: [] })
      requests.push(request.clone())
      return requests.length === 1
        ? problem('VALIDATION_FAILED', status)
        : json(201, { data: receipt(category()) })
    })
    const manager = useCategoryManager()
    await manager.open()
    manager.start()
    manager.draft.name = '咖啡'
    await manager.save()
    expect(manager.uncertainCreate.value).toBe(false)
    manager.draft.name = '茶饮'
    await manager.save()
    const first = await requests[0]!.json()
    const second = await requests[1]!.json()
    expect(second.id).toBe(first.id)
    expect(second.name).toBe('茶饮')
    expect(requests[0]!.headers.get('Idempotency-Key')).not.toBe(
      requests[1]!.headers.get('Idempotency-Key'),
    )
  })

  it('保存中不能刷新或切换分类，成功后仍会刷新列表', async () => {
    const pending = deferred<Response>()
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      return request.method === 'GET' ? json(200, { data: [category()] }) : pending.promise
    })
    const manager = useCategoryManager()
    await manager.open()
    manager.start()
    manager.draft.name = '咖啡'
    const saving = manager.save()
    await manager.open()
    manager.start(category())
    await manager.load()
    manager.close()
    expect(await manager.remove(category(), true)).toBe(false)
    expect(manager.active.value).toBe(true)
    expect(manager.baseline.value).toBeNull()
    expect(manager.draft.name).toBe('咖啡')
    expect(requests.filter((request) => request.method === 'GET')).toHaveLength(1)
    pending.resolve(json(201, { data: receipt(category({ name: '咖啡' })) }))
    expect(await saving).toBe(true)
    expect(requests.filter((request) => request.method === 'GET')).toHaveLength(2)
    expect(requests.filter((request) => request.method === 'POST')).toHaveLength(1)
  })

  it('普通 PATCH 失败后仍能改稿重试', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      if (request.method === 'GET') return json(200, { data: [category()] })
      requests.push(request.clone())
      if (requests.length === 1) throw new TypeError('network')
      return json(200, { data: receipt(category({ name: '修正名称' })) })
    })
    const manager = useCategoryManager()
    await manager.open()
    manager.start(category())
    manager.draft.name = '第一次修改'
    await manager.save()
    expect(manager.uncertainCreate.value).toBe(false)
    manager.draft.name = '修正名称'
    await manager.save()
    expect(await requests[1]!.json()).toEqual({ name: '修正名称' })
    expect(requests[0]!.headers.get('Idempotency-Key')).not.toBe(
      requests[1]!.headers.get('Idempotency-Key'),
    )
  })

  it('分类冲突保留输入，明确重提只保存自己的改动', async () => {
    const latest = category({ version: '3', name: '地面交通', sort_order: 12 })
    const writes: Request[] = []
    transport.mockImplementation(async (request) => {
      const path = new URL(request.url).pathname
      if (request.method === 'GET')
        return path.endsWith('/expense-categories')
          ? json(200, { data: [category()] })
          : json(200, { data: latest })
      writes.push(request.clone())
      return writes.length === 1
        ? problem('VERSION_CONFLICT', 412)
        : json(200, { data: receipt({ ...latest, icon: null }) })
    })
    const manager = useCategoryManager()
    await manager.open()
    manager.start(category())
    manager.draft.icon = ''
    await manager.save()
    expect(manager.conflict.value).toBe(true)
    expect(manager.draft.icon).toBe('')
    expect(manager.latest.value?.name).toBe('地面交通')
    await manager.save()
    expect(writes).toHaveLength(1)
    await manager.save(true)
    expect(await writes[1]!.json()).toEqual({ icon: null })
    expect(writes[1]!.headers.get('If-Match')).toBe('"3"')
  })

  it('不支持的图标不发起写入', async () => {
    transport.mockResolvedValue(json(200, { data: [] }))
    const manager = useCategoryManager()
    await manager.open()
    manager.start()
    manager.draft.name = '测试'
    manager.draft.icon = 'unsupported'
    expect(await manager.save()).toBe(false)
    expect(manager.errors.value.icon).toContain('支持的图标')
    expect(transport.mock.calls.every(([request]) => request.method === 'GET')).toBe(true)
  })

  it('拖动排序按新位置写入排序值，只更新位置变化的分类，失败后刷新列表', async () => {
    const writes: Request[] = []
    let failNext = false
    transport.mockImplementation(async (request) => {
      if (request.method === 'GET')
        return json(200, {
          data: [
            category({ id: 'a', name: '交通', sort_order: 0, version: '1' }),
            category({ id: 'b', name: '住宿', sort_order: 1, version: '1' }),
            category({ id: 'c', name: '美食', sort_order: 2, version: '1' }),
          ],
        })
      writes.push(request.clone())
      if (failNext) {
        failNext = false
        throw new TypeError('network')
      }
      return json(200, { data: receipt(category()) })
    })
    const manager = useCategoryManager()
    await manager.open()
    expect(await manager.reorder(['a', 'b'])).toBe(false)
    expect(await manager.reorder(['a', 'b', 'c'])).toBe(true)
    expect(writes).toHaveLength(0)
    expect(await manager.reorder(['c', 'a', 'b'])).toBe(true)
    expect(writes.map((request) => new URL(request.url).pathname.split('/').pop())).toEqual([
      'c',
      'a',
      'b',
    ])
    expect(await Promise.all(writes.map((request) => request.json()))).toEqual([
      { sort_order: 0 },
      { sort_order: 1 },
      { sort_order: 2 },
    ])
    expect(writes[0]!.headers.get('If-Match')).toBe('"1"')
    expect(manager.feedback.value).toContain('顺序已保存')
    failNext = true
    writes.length = 0
    expect(await manager.reorder(['b', 'a', 'c'])).toBe(false)
    expect(writes).toHaveLength(1)
    expect(manager.error.value).toContain('尚未确认')
    expect(manager.reordering.value).toBe(false)
    expect(manager.items.value.map((item) => item.id)).toEqual(['a', 'b', 'c'])
  })

  it('切换编辑项后，迟到的最新版本不会替换当前分类', async () => {
    const pending = deferred<Response>()
    transport.mockImplementation(async (request) =>
      new URL(request.url).pathname.endsWith('/expense-categories')
        ? json(200, { data: [category()] })
        : pending.promise,
    )
    const manager = useCategoryManager()
    await manager.open()
    manager.start(category())
    const load = manager.loadLatest()
    const second = category({ id: 'another-id', name: '住宿' })
    manager.start(second)
    pending.resolve(json(200, { data: category({ name: '过期的第一项', version: '8' }) }))
    await load
    expect(manager.baseline.value?.id).toBe(second.id)
    expect(manager.draft.name).toBe('住宿')
    expect(manager.latest.value).toBeNull()
    expect(manager.loadingLatest.value).toBe(false)
  })
})
