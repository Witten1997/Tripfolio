import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { createHash, webcrypto } from 'node:crypto'
import { TextEncoder } from 'node:util'
import { ElMessageBox } from 'element-plus'
import CategoryManagerDialog from './CategoryManagerDialog.vue'
import CategoryCreateDrawer from './CategoryCreateDrawer.vue'
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
async function setup() {
  const wrapper = mount(CategoryManagerDialog, {
    global: { stubs: { ElDialog: Shell, VueDraggable: Draggable, CategoryIconPicker: true } },
  })
  view = wrapper
  await wrapper.vm.open()
  await flushPromises()
  return wrapper
}
const move = async () => {
  await view!
    .findAll('.category-card button')[2]!
    .trigger('keydown', { key: 'ArrowLeft', shiftKey: true })
  await flushPromises()
}

describe('桌面分类排序恢复', () => {
  it('键盘排序逐条保存，名称草稿保持', async () => {
    const v = await setup()
    await v.findAll('.category-card button')[0]!.trigger('click')
    await flushPromises()
    await v.get('input').setValue('未保存名称')
    await move()
    await vi.waitFor(() => expect(v.text()).toContain('分类顺序已保存'))
    expect(writes).toHaveLength(2)
    expect((v.get('input').element as HTMLInputElement).value).toBe('未保存名称')
    expect(writes[0]!.headers.get('X-Collection-Guards')).not.toBe(
      writes[1]!.headers.get('X-Collection-Guards'),
    )
  })
  it('冲突保留镜像，取消不采纳，确认采用后仍需手动保存', async () => {
    mode = 'conflict'
    const v = await setup()
    await move()
    expect(v.findAll('.category-card__name').map((x) => x.text())).toEqual([
      '分类1',
      '分类3',
      '分类2',
    ])
    expect(button('新增分类').attributes('disabled')).toBeDefined()
    await button('核对最新分类').trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(button('保留目标顺序并采用')).toBeDefined())
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel')
    await button('保留目标顺序并采用').trigger('click')
    await flushPromises()
    expect(writes).toHaveLength(1)
    await button('保留目标顺序并采用').trigger('click')
    await flushPromises()
    expect(writes).toHaveLength(1)
    mode = 'normal'
    await button('保存待排顺序').trigger('click')
    await vi.waitFor(() => expect(v.text()).toContain('分类顺序已保存'))
  })
  it('未知后412持续锁编辑关闭，但原样重试仍可用', async () => {
    mode = 'unknown'
    const v = await setup()
    await move()
    expect(button('关闭').attributes('disabled')).toBeDefined()
    expect(v.findComponent(Draggable).props('disabled')).toBe(true)
    const done = vi.fn()
    await v.findComponent(Shell).props('beforeClose')(done)
    expect(done).not.toHaveBeenCalled()
    await button('原样重试排序').trigger('click')
    await flushPromises()
    expect(v.text()).toContain('当前请求结果未确认')
    expect(button('原样重试排序').attributes('disabled')).toBeUndefined()
    await button('原样重试排序').trigger('click')
    await vi.waitFor(() => expect(v.text()).toContain('分类顺序已保存'))
    expect(new Set(writes.slice(0, 3).map((r) => r.headers.get('Idempotency-Key'))).size).toBe(1)
  })
  it('已确认写入后读失败，仅重读再继续剩余项', async () => {
    mode = 'readfail'
    const v = await setup()
    await move()
    expect(v.text()).toContain('已保存 1 条')
    expect(writes).toHaveLength(1)
    await button('重试读取已保存结果').trigger('click')
    await vi.waitFor(() => expect(v.text()).toContain('分类顺序已保存'))
    expect(writes).toHaveLength(2)
  })
  it('快捷新增沿原流程回调，连续未知后原样重试不重复创建', async () => {
    mode = 'unknown'
    const wrapper = mount(CategoryCreateDrawer, {
      global: { stubs: { ResponsiveEditorShell: Shell, CategoryIconPicker: true } },
    })
    view = wrapper
    await wrapper.vm.open()
    await flushPromises()
    await wrapper.get('input').setValue('咖啡')
    await button('保存分类').trigger('click')
    await flushPromises()
    await button('重试创建').trigger('click')
    await flushPromises()
    expect(button('重试创建')).toBeDefined()
    await button('重试创建').trigger('click')
    await vi.waitFor(() => expect(wrapper.emitted('saved')).toHaveLength(1))
    expect(rows.filter((r) => r.name === '咖啡')).toHaveLength(1)
    expect(new Set(writes.map((r) => r.headers.get('Idempotency-Key'))).size).toBe(1)
  })
})
