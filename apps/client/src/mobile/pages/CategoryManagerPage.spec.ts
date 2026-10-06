import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { createRouter, createMemoryHistory, RouterView } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import { createHash, webcrypto } from 'node:crypto'
import { TextEncoder } from 'node:util'
import { ElMessageBox } from 'element-plus'
import CategoryManagerPage from './CategoryManagerPage.vue'

import { useSessionStore, type Account } from '@/shared/stores/session'
import { useMetadataStore } from '@/shared/stores/metadata'
import { category, metadata, json, problem, receipt } from '@/shared/travel/__tests__/fixtures'

const { transport } = vi.hoisted(() => ({
  transport: vi.fn<(request: Request) => Promise<Response>>(),
}))
vi.mock('@/shared/api/client', async (original) => {
  const source = await original<typeof import('@/shared/api/client')>()
  return {
    ...source,
    api: source.createApiClient({
      baseUrl: 'http://api.test/api/v1',
      fetch: transport,
      refresh: async () => false,
    }),
  }
})
const owner = '11111111-1111-4111-8111-111111111111',
  epoch = '22222222-2222-4222-8222-222222222222'
const Shell = defineComponent({
  props: ['modelValue', 'beforeClose'],
  template: '<section v-if="modelValue"><slot/><footer><slot name="footer"/></footer></section>',
})
const Draggable = defineComponent({
  props: ['modelValue', 'disabled'],
  emits: ['update:modelValue', 'start', 'end'],
  template: '<ul><slot/></ul>',
})
let view: VueWrapper | undefined
let rows: ReturnType<typeof category>[]
let writes: Request[]
let mode: 'normal' | 'conflict' | 'unknown' | 'readfail'
let readFailed: boolean
let cache: Map<string, ReturnType<typeof receipt>>
function page() {
  const sorted = [...rows].sort((a, b) => a.id.localeCompare(b.id))
  return {
    kind: 'categories',
    scope_id: owner,
    sync_epoch: epoch,
    revision: `sha256:${createHash('sha256')
      .update(
        `${epoch}\ncategories\n${owner}\n${sorted.map((r) => `${r.id}:${r.version}\n`).join('')}`,
      )
      .digest('hex')}`,
    expires_at: new Date(Date.now() + 600000).toISOString().replace(/\.\d{3}Z$/, 'Z'),
    items: sorted,
    next_cursor: null,
  }
}
async function apply(request: Request) {
  const key = request.headers.get('Idempotency-Key')!
  if (cache.has(key)) return json(200, { data: { ...cache.get(key)!, replayed: true } })
  const body = await request.clone().json()
  if (request.method === 'POST')
    rows = [...rows, category({ ...body, version: '1', sort_order: rows.length })]
  else {
    const id = new URL(request.url).pathname.split('/').pop()
    rows = rows.map((r) =>
      r.id === id ? { ...r, ...body, version: String(BigInt(r.version) + 1n) } : r,
    )
  }
  const result = {
    ...receipt(null),
    scope_revisions: [{ kind: 'categories' as const, scope_id: owner, revision: page().revision }],
  }
  cache.set(key, result)
  return json(request.method === 'POST' ? 201 : 200, { data: result })
}
beforeEach(() => {
  setActivePinia(createPinia())
  useSessionStore().account = { id: owner } as Account
  useMetadataStore().metadata = metadata
  useMetadataStore().status = 'ready'
  vi.stubGlobal('crypto', webcrypto)
  vi.stubGlobal('TextEncoder', TextEncoder)
  vi.spyOn(ElMessageBox, 'confirm').mockResolvedValue('confirm' as never)
  rows = [1, 2, 3].map((n) =>
    category({
      id: `20000000-0000-4000-8000-${String(n).padStart(12, '0')}`,
      name: `分类${n}`,
      sort_order: n - 1,
      version: '1',
    }),
  )
  writes = []
  mode = 'normal'
  readFailed = false
  cache = new Map()
  transport.mockReset()
  transport.mockImplementation(async (request) => {
    if (request.method === 'GET') {
      if (mode === 'readfail' && writes.length === 1 && !readFailed) {
        readFailed = true
        throw new TypeError('read failed')
      }
      return json(200, page())
    }
    writes.push(request.clone())
    if (mode === 'conflict') return problem('COLLECTION_CONFLICT', 412)
    if (mode === 'unknown' && writes.length === 1) {
      await apply(request)
      throw new TypeError('response lost')
    }
    if (mode === 'unknown' && writes.length === 2) return problem('COLLECTION_CONFLICT', 412)
    return apply(request)
  })
})
afterEach(() => {
  view?.unmount()
  view = undefined
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})
const button = (text: string) => view!.findAll('button').find((b) => b.text() === text)!
let router: ReturnType<typeof createRouter>
async function setup() {
  router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/categories', component: CategoryManagerPage },
      { path: '/account', name: 'account', component: { template: '<p>账户页面</p>' } },
    ],
  })
  await router.push('/categories')
  await router.isReady()
  const wrapper = mount(defineComponent({ setup: () => () => h(RouterView) }), {
    global: {
      plugins: [router],
      stubs: { ResponsiveEditorShell: Shell, VueDraggable: Draggable, CategoryIconPicker: true },
    },
  })
  view = wrapper
  await vi.waitFor(() => expect(wrapper.findAll('.category-card button')).toHaveLength(3))
  return wrapper
}
const move = async () => {
  await view!
    .findAll('.category-card button')[2]!
    .trigger('keydown', { key: 'ArrowLeft', shiftKey: true })
  await flushPromises()
}

describe('移动分类排序恢复', () => {
  it('拖动镜像通过原guard逐条提交', async () => {
    const v = await setup()
    const drag = v.findComponent(Draggable)
    drag.vm.$emit('start')
    drag.vm.$emit('update:modelValue', [rows[2], rows[0], rows[1]])
    drag.vm.$emit('end')
    await vi.waitFor(() => expect(v.text()).toContain('分类顺序已保存'))
    expect(v.findAll('.category-card__name').map((x) => x.text())).toEqual([
      '分类3',
      '分类1',
      '分类2',
    ])
    expect(writes).toHaveLength(3)
    expect(new Set(writes.map((r) => r.headers.get('X-Collection-Guards'))).size).toBe(3)
  })
  it('未知后412仍禁止路由离开与再次拖动，保留可用原样重试', async () => {
    mode = 'unknown'
    const v = await setup()
    await move()
    expect(v.findComponent(Draggable).props('disabled')).toBe(true)
    await router.push('/account')
    expect(router.currentRoute.value.path).toBe('/categories')
    await move()
    expect(writes).toHaveLength(1)
    await button('原样重试排序').trigger('click')
    await flushPromises()
    expect(v.text()).toContain('当前请求结果未确认')
    await router.push('/account')
    expect(router.currentRoute.value.path).toBe('/categories')
    expect(button('原样重试排序').attributes('disabled')).toBeUndefined()
    await button('原样重试排序').trigger('click')
    await vi.waitFor(() => expect(v.text()).toContain('分类顺序已保存'))
    expect(new Set(writes.slice(0, 3).map((r) => r.headers.get('Idempotency-Key'))).size).toBe(1)
  })
  it('新增分类导致ID集合变化时，显式放弃待排顺序才采用候选', async () => {
    mode = 'conflict'
    const v = await setup()
    await move()
    rows = [
      ...rows,
      category({ id: '20000000-0000-4000-8000-000000000004', name: '新增分类', sort_order: 3 }),
    ]
    await button('核对最新分类').trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(button('保留目标顺序并采用')).toBeDefined())
    expect(v.findAll('.category-card')).toHaveLength(3)
    expect(button('保留目标顺序并采用').attributes('disabled')).toBeDefined()
    await button('放弃待排顺序并采用').trigger('click')
    await flushPromises()
    expect(v.findAll('.category-card')).toHaveLength(4)
    expect(writes).toHaveLength(1)
    expect(v.findComponent(Draggable).props('disabled')).toBe(false)
  })
  it('已确认一条后读取失败保留进度，只重读不重写', async () => {
    mode = 'readfail'
    const v = await setup()
    await move()
    expect(v.text()).toContain('已保存 1 条')
    expect(writes).toHaveLength(1)
    await button('重试读取已保存结果').trigger('click')
    await vi.waitFor(() => expect(v.text()).toContain('分类顺序已保存'))
    expect(writes).toHaveLength(2)
  })
  it('明确拒绝后的路由离开需确认，取消保留排序镜像', async () => {
    mode = 'conflict'
    const v = await setup()
    await move()
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel')
    await router.push('/account')
    expect(router.currentRoute.value.path).toBe('/categories')
    expect(v.findAll('.category-card__name').map((x) => x.text())).toEqual([
      '分类1',
      '分类3',
      '分类2',
    ])
    await router.push('/account')
    expect(router.currentRoute.value.path).toBe('/account')
    expect(writes).toHaveLength(1)
  })
})
