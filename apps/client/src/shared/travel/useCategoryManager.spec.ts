import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createHash, webcrypto } from 'node:crypto'
import { TextEncoder } from 'node:util'

import type { CategoryCreate } from '@/shared/api/categories'
import { useMetadataStore } from '@/shared/stores/metadata'
import { useCategoryManager } from '@/shared/travel/useCategoryManager'
import { useCategoryCards } from '@/shared/travel/useCategoryManager'
import { useSessionStore, type Account } from '@/shared/stores/session'
import type { ExpenseCategory } from '@/shared/api/categories'
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
const owner = '11111111-1111-4111-8111-111111111111'
const epoch = '22222222-2222-4222-8222-222222222222'
let expiresAt = ''
const cid = (n: number) => `20000000-0000-4000-8000-${String(n).padStart(12, '0')}`
function page(items: ExpenseCategory[], all = items, cursor: string | null = null) {
  const sorted = [...all].sort((a, b) => a.id.localeCompare(b.id))
  const revision = `sha256:${createHash('sha256')
    .update(
      `${epoch}\ncategories\n${owner}\n${sorted.map((item) => `${item.id}:${item.version}\n`).join('')}`,
    )
    .digest('hex')}`
  return {
    kind: 'categories',
    scope_id: owner,
    sync_epoch: epoch,
    revision,
    expires_at: expiresAt,
    items: [...items].sort((a, b) => a.id.localeCompare(b.id)),
    next_cursor: cursor,
  }
}
const categoryPage = (items: ExpenseCategory[]) => json(200, page(items))
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
  expiresAt = new Date(Date.now() + 600000).toISOString().replace(/\.\d{3}Z$/, 'Z')
  useSessionStore().account = { id: owner } as Account
  vi.stubGlobal('crypto', webcrypto)
  vi.stubGlobal('TextEncoder', TextEncoder)
})
afterEach(() => vi.unstubAllGlobals())

function sortingServer() {
  let rows = [1, 2, 3].map((n) =>
    category({ id: cid(n), name: `分类${n}`, sort_order: n - 1, version: '9007199254740993' }),
  )
  const writes: Request[] = []
  const readGuards: string[] = []
  const receipts = new Map<string, ReturnType<typeof receipt>>()
  let reads = 0
  const server = {
    get rows() {
      return rows
    },
    set rows(value) {
      rows = value
    },
    writes,
    readGuards,
    get reads() {
      return reads
    },
    beforeRead: null as null | (() => Promise<Response | undefined>),
    beforeWrite: null as null | ((request: Request) => Promise<Response | undefined>),
    async apply(request: Request) {
      const key = request.headers.get('Idempotency-Key')!
      if (receipts.has(key)) return json(200, { data: { ...receipts.get(key)!, replayed: true } })
      const id = new URL(request.url).pathname.split('/').pop()!
      const patch = await request.clone().json()
      const previous = rows.find((item) => item.id === id)!
      rows = rows.map((item) =>
        item.id === id
          ? { ...item, ...patch, version: String(BigInt(previous.version) + 1n) }
          : item,
      )
      const result = {
        ...receipt(null),
        scope_revisions: [
          { kind: 'categories' as const, scope_id: owner, revision: page(rows).revision },
        ],
      }
      receipts.set(key, result)
      return json(200, { data: result })
    },
  }
  transport.mockImplementation(async (request) => {
    if (request.method === 'GET') {
      reads++
      const fault = await server.beforeRead?.()
      if (fault) return fault
      readGuards.push(page(rows).revision)
      return categoryPage(rows)
    }
    writes.push(request.clone())
    const fault = await server.beforeWrite?.(request)
    return fault ?? server.apply(request)
  })
  return server
}
const target = [cid(3), cid(1), cid(2)]

describe('完整分类基线与逐条排序', () => {
  it('未取得全部分页时禁排，完整后才安装；空集合可就绪', async () => {
    const rows = Array.from({ length: 51 }, (_, index) => category({ id: cid(index + 1) }))
    const last = deferred<Response>()
    transport.mockImplementation(async (request) =>
      new URL(request.url).searchParams.has('cursor')
        ? last.promise
        : json(200, page(rows.slice(0, 50), rows, 'next')),
    )
    const manager = useCategoryManager()
    const opening = manager.open()
    await vi.waitFor(() => expect(transport).toHaveBeenCalledTimes(2))
    expect(manager.items.value).toEqual([])
    expect(await manager.reorder([cid(2), cid(1)])).toBe(false)
    last.resolve(json(200, page(rows.slice(50), rows)))
    await opening
    expect(manager.canReorder.value).toBe(true)
    manager.close()
    transport.mockImplementation(async () => categoryPage([]))
    await manager.open()
    expect(await manager.reorder([])).toBe(true)
    expect(transport.mock.calls.every(([request]) => request.method === 'GET')).toBe(true)
  })
  it('逐条使用上一收据核对后的完整基线，保留大版本和编辑稿原基线', async () => {
    const server = sortingServer()
    const manager = useCategoryManager()
    await manager.open()
    manager.start(server.rows[0])
    manager.draft.name = '尚未保存名称'
    const editing = manager.baseline.value
    expect(await manager.reorder(target)).toBe(true)
    expect(await Promise.all(server.writes.map((request) => request.clone().json()))).toEqual([
      { sort_order: 0 },
      { sort_order: 1 },
      { sort_order: 2 },
    ])
    server.writes.forEach((request, index) => {
      expect(JSON.parse(request.headers.get('X-Collection-Guards')!)[0].revision).toBe(
        server.readGuards[index],
      )
      expect(request.headers.get('If-Match')).toBe('"9007199254740993"')
    })
    expect(
      new Set(server.writes.map((request) => request.headers.get('Idempotency-Key'))).size,
    ).toBe(3)
    expect(manager.savedOrderCount.value).toBe(3)
    expect(manager.items.value.map((item) => item.id)).toEqual(target)
    expect(manager.draft.name).toBe('尚未保存名称')
    expect(manager.baseline.value).toBe(editing)
  })
  it.each([412, 428])('部分成功后%s保留目标、进度和输入且不自动读写', async (status) => {
    const server = sortingServer()
    server.beforeWrite = async () =>
      server.writes.length === 2 ? problem('COLLECTION_CONFLICT', status) : undefined
    const manager = useCategoryManager()
    await manager.open()
    manager.start(server.rows[0])
    manager.draft.icon = 'food'
    expect(await manager.reorder(target)).toBe(false)
    expect(manager.savedOrderCount.value).toBe(1)
    expect(manager.pendingOrderCount.value).toBe(2)
    expect(manager.items.value.map((item) => item.id)).toEqual(target)
    expect(manager.draft.icon).toBe('food')
    expect(server.reads).toBe(2)
    await manager.load()
    manager.start()
    expect(await manager.save()).toBe(false)
    expect(await manager.remove(server.rows[0]!, true)).toBe(false)
    const cards = useCategoryCards(manager)
    await cards.move(0, 1)
    expect(server.writes).toHaveLength(2)
    expect(server.reads).toBe(2)
  })
  it.each([401, 412, 428])('未知后%s仍只原样重试，禁止关闭和核对', async (status) => {
    const server = sortingServer()
    server.beforeWrite = async (request) => {
      if (server.writes.length === 1) {
        await server.apply(request)
        throw new TypeError('lost')
      }
      if (server.writes.length === 2) return problem('REJECTED', status)
    }
    const manager = useCategoryManager()
    await manager.open()
    await manager.reorder(target)
    expect(manager.orderUnknown.value).toBe(true)
    manager.close(true)
    await manager.reviewOrder()
    await manager.load()
    expect(manager.opened.value).toBe(true)
    await manager.continueOrder()
    expect(manager.orderUnknown.value).toBe(true)
    if (status === 401) {
      expect(await manager.continueOrder()).toBe(false)
      useSessionStore().account = { id: owner } as Account
    }
    expect(await manager.continueOrder()).toBe(true)
    const attempts = server.writes.slice(0, 3)
    expect(new Set(attempts.map((r) => r.headers.get('Idempotency-Key'))).size).toBe(1)
    expect(new Set(attempts.map((r) => r.headers.get('X-Collection-Guards'))).size).toBe(1)
    expect(await Promise.all(attempts.map((r) => r.clone().json()))).toEqual([
      { sort_order: 0 },
      { sort_order: 0 },
      { sort_order: 0 },
    ])
    expect(server.rows.find((r) => r.id === cid(3))?.version).toBe('9007199254740994')
  })
  it('成功后重读失败只重试读取，不再发送已确认的一条', async () => {
    const server = sortingServer()
    let fail = true
    server.beforeRead = async () => {
      if (server.reads === 2 && fail) {
        fail = false
        throw new TypeError('offline')
      }
      return undefined
    }
    const manager = useCategoryManager()
    await manager.open()
    await manager.reorder(target)
    expect(manager.orderState.value).toBe('read_failed')
    expect(manager.savedOrderCount.value).toBe(1)
    expect(server.writes).toHaveLength(1)
    expect(await manager.continueOrder()).toBe(true)
    expect(server.writes.map((r) => new URL(r.url).pathname.split('/').pop())).toEqual(target)
  })
  it('重放原成功收据后，当前完整列表已变化则停止，不把最新data当作原事实', async () => {
    const server = sortingServer()
    server.beforeWrite = async (request) => {
      if (server.writes.length === 1) {
        await server.apply(request)
        server.rows = server.rows.map((item) =>
          item.id === cid(1)
            ? { ...item, name: '远端改名', version: String(BigInt(item.version) + 1n) }
            : item,
        )
        throw new TypeError('lost')
      }
      const response = await server.apply(request)
      const body = await response.json()
      body.data.data = server.rows[0]
      return json(200, body)
    }
    const manager = useCategoryManager()
    await manager.open()
    await manager.reorder(target)
    expect(await manager.continueOrder()).toBe(false)
    expect(manager.orderState.value).toBe('conflict')
    expect(manager.savedOrderCount.value).toBe(1)
    expect(server.writes).toHaveLength(2)
    expect(manager.items.value.map((item) => item.id)).toEqual(target)
  })
  it.each(['missing', 'insert', 'delete'])(
    '原facts缺失或并发%s后停止，不采纳任意新列表',
    async (mode) => {
      const server = sortingServer()
      server.beforeWrite = async (request) => {
        const response = await server.apply(request)
        if (mode === 'missing') return json(200, { data: receipt(category({ version: '999' })) })
        if (mode === 'insert') server.rows = [...server.rows, category({ id: cid(4) })]
        else server.rows = server.rows.filter((r) => r.id !== cid(1))
        return response
      }
      const manager = useCategoryManager()
      await manager.open()
      await manager.reorder(target)
      expect(manager.orderState.value).toBe('conflict')
      expect(server.writes).toHaveLength(1)
      expect(manager.items.value.map((item) => item.id)).toEqual(target)
      await manager.reviewOrder()
      expect(manager.latestOrder.value).not.toBeNull()
      if (mode !== 'missing') expect(manager.adoptOrder(true)).toBe(false)
      expect(manager.adoptOrder(false)).toBe(true)
      expect(server.writes).toHaveLength(1)
    },
  )
  it('候选必须显式采用且不自动保存，新读取失败或取消不能沿用旧候选', async () => {
    const server = sortingServer()
    server.beforeWrite = async () => problem('COLLECTION_CONFLICT', 412)
    const manager = useCategoryManager()
    await manager.open()
    await manager.reorder(target)
    const original = manager.collection.value
    await manager.reviewOrder()
    expect(manager.collection.value).toBe(original)
    server.beforeRead = async () => {
      throw new TypeError('failed')
    }
    await manager.reviewOrder()
    expect(manager.latestOrder.value).toBeNull()
    expect(manager.adoptOrder(true)).toBe(false)
    server.beforeRead = null
    await manager.reviewOrder()
    manager.cancelOrderReview()
    expect(manager.adoptOrder(true)).toBe(false)
    await manager.reviewOrder()
    expect(manager.adoptOrder(true)).toBe(true)
    expect(manager.orderState.value).toBe('ready')
    expect(server.writes).toHaveLength(1)
    server.beforeWrite = null
    expect(await manager.continueOrder()).toBe(true)
    expect(server.writes[0]!.headers.get('Idempotency-Key')).not.toBe(
      server.writes[1]!.headers.get('Idempotency-Key'),
    )
  })
  it('关闭、账号变化和迟到的候选不得安装或继续下一条', async () => {
    const server = sortingServer()
    server.beforeWrite = async () => problem('COLLECTION_CONFLICT', 412)
    const manager = useCategoryManager()
    await manager.open()
    await manager.reorder(target)
    const pending = deferred<Response>()
    server.beforeRead = async () => pending.promise
    const loading = manager.reviewOrder()
    manager.close(true)
    pending.resolve(categoryPage(server.rows))
    await loading
    expect(manager.latestOrder.value).toBeNull()
    expect(manager.opened.value).toBe(false)
    server.beforeRead = null
    server.beforeWrite = null
    await manager.open()
    const write = deferred<Response>()
    server.beforeWrite = async () => write.promise
    const saving = manager.reorder(target)
    useSessionStore().account = { id: '33333333-3333-4333-8333-333333333333' } as Account
    write.resolve(json(200, { data: receipt(null) }))
    await saving
    expect(manager.items.value).toEqual([])
    expect(manager.hasPendingOrder.value).toBe(false)
    expect(server.writes).toHaveLength(2)
  })
  it('快捷新增未知后明确失败仍保留原创建请求', async () => {
    const writes: Request[] = []
    transport.mockImplementation(async (request) => {
      if (request.method === 'GET') return categoryPage([])
      writes.push(request.clone())
      if (writes.length === 1) throw new TypeError('lost')
      return writes.length === 2
        ? problem('AUTH_REQUIRED', 401)
        : json(201, { data: receipt(category()) })
    })
    const manager = useCategoryManager()
    await manager.open()
    manager.start()
    manager.draft.name = '咖啡'
    await manager.save()
    manager.draft.name = '茶'
    await manager.save()
    expect(manager.uncertainCreate.value).toBe(true)
    expect(await manager.save()).toBe(false)
    useSessionStore().account = { id: owner } as Account
    await manager.save()
    expect(await writes[0]!.json()).toEqual(await writes[2]!.json())
    expect(writes[0]!.headers.get('Idempotency-Key')).toBe(
      writes[2]!.headers.get('Idempotency-Key'),
    )
  })
})

describe('账号共用分类管理', () => {
  it('列表读取 data 数组，以排序值和固定 id 处理并列顺序', async () => {
    transport.mockResolvedValue(
      categoryPage([
        category({ id: cid(2), sort_order: 5 }),
        category({ id: cid(1), sort_order: 5 }),
        category({ id: cid(3), sort_order: 1 }),
      ]),
    )
    const manager = useCategoryManager()
    await manager.open()
    expect(manager.items.value.map((item) => item.id)).toEqual([cid(3), cid(1), cid(2)])
  })

  it('预设分类可以改图标，只 PATCH 发生变化的字段，图标清空为 null', async () => {
    const requests: Request[] = []
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      return request.method === 'GET'
        ? categoryPage([category()])
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
      return request.method === 'DELETE' ? problem('CATEGORY_IN_USE') : categoryPage([category()])
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
        return categoryPage([category(), ...(committed ? [category(committed.body)] : [])])
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
      if (request.method === 'GET') return categoryPage([])
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
      if (request.method === 'GET') return categoryPage([])
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
      return request.method === 'GET' ? categoryPage([category()]) : pending.promise
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
      if (request.method === 'GET') return categoryPage([category()])
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
        return path.endsWith('/collection-baselines')
          ? categoryPage([category()])
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
    transport.mockResolvedValue(categoryPage([]))
    const manager = useCategoryManager()
    await manager.open()
    manager.start()
    manager.draft.name = '测试'
    manager.draft.icon = 'unsupported'
    expect(await manager.save()).toBe(false)
    expect(manager.errors.value.icon).toContain('支持的图标')
    expect(transport.mock.calls.every(([request]) => request.method === 'GET')).toBe(true)
  })

  it('切换编辑项后，迟到的最新版本不会替换当前分类', async () => {
    const pending = deferred<Response>()
    transport.mockImplementation(async (request) =>
      new URL(request.url).pathname.endsWith('/collection-baselines')
        ? categoryPage([category()])
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
