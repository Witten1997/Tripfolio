import { File as NodeFile } from 'node:buffer'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ElPagination } from 'element-plus'
import { defineComponent } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const { fetcher } = vi.hoisted(() => ({ fetcher: vi.fn() }))
vi.mock('@/shared/api/client', async () => ({
  api: (await import('openapi-fetch')).default({
    baseUrl: 'https://tripfolio.test/api/v1',
    fetch: fetcher,
  }),
  registerRefreshHandler: vi.fn(),
}))
vi.mock('@/shared/travel/tripContext', () => ({
  useTripContext: () => ({ tripId: '11111111-1111-4111-8111-111111111111' }),
}))
vi.mock('@/platform/ledgerTemplate', () => ({ saveLedgerTemplate: vi.fn() }))
import LedgerImportDialog from './LedgerImportDialog.vue'
import type { LedgerImportPreview } from '@/shared/api/ledgerImport'

const trip = '11111111-1111-4111-8111-111111111111'
const guard = { kind: 'members' as const, scope_id: trip, revision: `sha256:${'b'.repeat(64)}` }
const Dialog = defineComponent({
  props: ['modelValue'],
  template:
    '<section v-if="modelValue" role="dialog"><slot/><footer><slot name="footer"/></footer></section>',
})
const file = (n = 1, name = '账单.xlsx') => new File([new Uint8Array([80, 75, 255, n])], name)
function preview(count = 1): LedgerImportPreview {
  return {
    scope_revisions: [guard],
    digest: 'a'.repeat(64),
    count,
    valid_count: count,
    total_amount: '12.50',
    currency_code: 'CNY',
    errors: [],
    warnings: [],
    rows: Array.from({ length: count }, (_, index) => ({
      row: index + 5,
      amount: '12.50',
      category: '餐饮',
      split_mode: 'personal',
      participants: ['我'],
      occurred_on: '2026-10-06',
      notes: '',
      payer: '我',
      splits: [{ member: '我', amount: '12.50' }],
    })),
  }
}
let current: LedgerImportPreview
let status: number | 'network' | null
let previewFailure: boolean
let requests: Request[]
let views: VueWrapper[]
let previewGate: ((request: Request) => Promise<void>) | null
const receipt = { scope_revisions: [guard], affected: [], replayed: true, warnings: [], data: null }
beforeEach(() => {
  vi.stubGlobal('File', NodeFile)
  current = preview()
  status = null
  previewFailure = false
  requests = []
  views = []
  previewGate = null
  fetcher.mockReset().mockImplementation(async (request: Request) => {
    requests.push(request.clone())
    if (request.url.endsWith('/ledger-import-preview')) {
      const captured = structuredClone(current)
      if (previewGate) await previewGate(request)
      if (previewFailure) throw new TypeError('preview offline')
      return Response.json({ data: captured })
    }
    if (status === 'network') throw new TypeError('offline')
    if (status)
      return Response.json(
        {
          status,
          code:
            status === 412
              ? 'COLLECTION_CONFLICT'
              : status === 428
                ? 'COLLECTION_BASE_REQUIRED'
                : 'UNAVAILABLE',
        },
        { status },
      )
    return Response.json({ data: receipt })
  })
})
afterEach(() => {
  views.forEach((view) => view.unmount())
  vi.unstubAllGlobals()
})
async function setup() {
  const view = mount(LedgerImportDialog, {
    attachTo: document.body,
    global: { stubs: { ElDialog: Dialog, ElDrawer: Dialog } },
  })
  views.push(view)
  view.vm.open()
  await flushPromises()
  return view
}
async function choose(view: VueWrapper, selected = file()) {
  const input = view.find('input[type=file]')
  Object.defineProperty(input.element, 'files', { configurable: true, value: [selected] })
  await input.trigger('change')
  await flushPromises()
}
const button = (view: VueWrapper, text: string) =>
  view.findAll('button').find((b) => b.text() === text)!
const submit = (view: VueWrapper) =>
  view.findAll('button').find((b) => /^(确认导入|重试确认)/.test(b.text()))!
const imports = () => requests.filter((r) => new URL(r.url).pathname.endsWith('/ledger-import'))
async function signature(request: Request) {
  return {
    bytes: [...new Uint8Array(await request.clone().arrayBuffer())],
    digest: new URL(request.url).searchParams.get('preview_digest'),
    guard: request.headers.get('X-Collection-Guards'),
    key: request.headers.get('Idempotency-Key'),
  }
}

describe('账单导入确认', () => {
  it('确认发送原文件/摘要/成员头，成功仅通知一次并清空旧批次', async () => {
    const view = await setup()
    await choose(view)
    expect(imports()).toHaveLength(0)
    await submit(view).trigger('click')
    await flushPromises()
    expect(await signature(imports()[0]!)).toMatchObject({
      bytes: [80, 75, 255, 1],
      digest: 'a'.repeat(64),
      guard: JSON.stringify([guard]),
    })
    expect(view.emitted('saved')).toEqual([[receipt, 1]])
    view.vm.open()
    await flushPromises()
    expect(submit(view).attributes('disabled')).toBeDefined()
    expect(view.text()).not.toContain('账单.xlsx')
  })
  it.each(['rows', 'missing-guard'])('有%s错误的预览不得导入', async (kind) => {
    if (kind === 'rows') {
      current.errors = [{ row: 5, column: '金额', message: '错误' }]
      current.valid_count = 0
    } else delete current.scope_revisions
    const view = await setup()
    await choose(view)
    expect(submit(view).attributes('disabled')).toBeDefined()
    expect(imports()).toHaveLength(0)
  })
  it.each([412, 428])('%s保留原批次；新预览须采用后再次确认且换操作ID', async (failure) => {
    const view = await setup()
    await choose(view)
    status = failure
    await submit(view).trigger('click')
    await flushPromises()
    const original = await signature(imports()[0]!)
    expect(view.text()).toContain('原预览已保留')
    expect(submit(view).attributes('disabled')).toBeDefined()
    current = {
      ...preview(2),
      digest: 'c'.repeat(64),
      scope_revisions: [{ ...guard, revision: `sha256:${'d'.repeat(64)}` }],
    }
    await button(view, '重新预览').trigger('click')
    await flushPromises()
    expect(view.text()).toContain('尚未替换原批次')
    expect(submit(view).attributes('disabled')).toBeDefined()
    await button(view, '保留原预览').trigger('click')
    await flushPromises()
    expect(view.text()).toContain('共 1 笔')
    await button(view, '重新预览').trigger('click')
    await flushPromises()
    await button(view, '采用新预览').trigger('click')
    await flushPromises()
    expect(imports()).toHaveLength(1)
    status = null
    await submit(view).trigger('click')
    await flushPromises()
    const next = await signature(imports()[1]!)
    expect(next.bytes).toEqual(original.bytes)
    expect(next.digest).toBe('c'.repeat(64))
    expect(next.key).not.toBe(original.key)
    expect(next.guard).not.toBe(original.guard)
    expect(view.emitted('saved')).toEqual([[receipt, 2]])
  })
  it('失败重预览和错误新文件保留原文件、行及分页', async () => {
    current = preview(26)
    const view = await setup()
    await choose(view)
    view.findComponent(ElPagination).vm.$emit('update:currentPage', 2)
    await flushPromises()
    expect(view.findComponent({ name: 'ElTable' }).props('data')[0].row).toBe(30)
    previewFailure = true
    await button(view, '重新预览').trigger('click')
    await flushPromises()
    expect(view.findComponent({ name: 'ElTable' }).props('data')[0].row).toBe(30)
    await choose(view, file(2, 'bad.txt'))
    expect(view.text()).toContain('账单.xlsx')
    expect(view.text()).not.toContain('bad.txt')
    expect(view.findComponent({ name: 'ElTable' }).props('data')[0].row).toBe(30)
    status = null
    await submit(view).trigger('click')
    await flushPromises()
    expect((await signature(imports()[0]!)).bytes).toEqual([80, 75, 255, 1])
  })
  it('新文件候选取消不替换原bytes，采用后才切换', async () => {
    const view = await setup()
    await choose(view)
    await choose(view, file(2))
    await button(view, '保留原预览').trigger('click')
    await flushPromises()
    status = 401
    await submit(view).trigger('click')
    await flushPromises()
    expect((await signature(imports()[0]!)).bytes).toEqual([80, 75, 255, 1])
    await choose(view, file(2))
    await button(view, '采用新预览').trigger('click')
    await flushPromises()
    status = null
    await submit(view).trigger('click')
    await flushPromises()
    expect((await signature(imports()[1]!)).bytes).toEqual([80, 75, 255, 2])
  })
  it.each([401, 403, 412, 428])('未知后%s仍禁关闭/换文件/预览且继续原请求', async (later) => {
    const view = await setup()
    await choose(view)
    status = 'network'
    await submit(view).trigger('click')
    await flushPromises()
    const original = await signature(imports()[0]!)
    status = later
    await submit(view).trigger('click')
    await flushPromises()
    expect(submit(view).text()).toBe('重试确认')
    expect(submit(view).attributes('disabled')).toBeUndefined()
    expect(button(view, '关闭').attributes('disabled')).toBeDefined()
    const count = requests.length
    await choose(view, file(2))
    view.vm.open()
    await flushPromises()
    expect(requests).toHaveLength(count)
    expect(view.find('input[type=file]').attributes('disabled')).toBeDefined()
    status = null
    await submit(view).trigger('click')
    await flushPromises()
    expect(await signature(imports()[1]!)).toEqual(original)
    expect(await signature(imports()[2]!)).toEqual(original)
  })
  it('普通关闭保留原批次；取消在途预览后迟到响应不能覆盖新会话', async () => {
    const view = await setup()
    await choose(view)
    await button(view, '关闭').trigger('click')
    view.vm.open()
    await flushPromises()
    expect(view.text()).toContain('账单.xlsx')
    let release!: () => void
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    previewGate = () => held
    current.digest = 'e'.repeat(64)
    await choose(view, file(2))
    await button(view, '关闭').trigger('click')
    view.vm.open()
    await flushPromises()
    release()
    await flushPromises()
    expect(view.find('[aria-label="待核对的新预览"]').exists()).toBe(false)
    await submit(view).trigger('click')
    await flushPromises()
    expect(await signature(imports()[0]!)).toMatchObject({
      bytes: [80, 75, 255, 1],
      digest: 'a'.repeat(64),
    })
  })
  it('保存中防双击，父页刷新报错不恢复待提交状态', async () => {
    const view = await setup()
    await choose(view)
    let release!: () => void
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    fetcher.mockImplementation(async (request: Request) => {
      requests.push(request.clone())
      await held
      return Response.json({ data: receipt })
    })
    view.vm.$.appContext.config.errorHandler = vi.fn()
    await view.setProps({
      onSaved: () => {
        throw new Error('parent refresh failed')
      },
    } as never)
    await submit(view).trigger('click')
    await submit(view).trigger('click')
    await flushPromises()
    expect(imports()).toHaveLength(1)
    expect(button(view, '关闭').attributes('disabled')).toBeDefined()
    release()
    await flushPromises()
    view.vm.open()
    await flushPromises()
    expect(submit(view).attributes('disabled')).toBeDefined()
    expect(imports()).toHaveLength(1)
    expect(view.emitted('saved')).toHaveLength(1)
  })
  it('保留20项错误分页与有效金额提示', async () => {
    current = preview(26)
    current.valid_count = 5
    current.errors = Array.from({ length: 21 }, (_, i) => ({
      row: i + 5,
      column: '金额',
      message: `错误${i}`,
    }))
    const view = await setup()
    await choose(view)
    const pager = view.findAllComponents(ElPagination).find((p) => p.props('pageSize') === 20)!
    expect(view.text()).toContain('有效金额')
    pager.vm.$emit('update:currentPage', 2)
    await flushPromises()
    expect(view.find('[aria-label="导入错误"]').text()).toContain('错误20')
    expect(view.find('[aria-label="导入错误"]').text()).not.toContain('错误0')
    expect(submit(view).attributes('disabled')).toBeDefined()
  })
  it('卸载取消未结束预览，迟到结果不能给新弹窗建立基线', async () => {
    const view = await setup()
    let release!: () => void
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    previewGate = () => held
    await choose(view)
    const request = requests[0]!
    view.unmount()
    views = []
    const next = await setup()
    release()
    await flushPromises()
    expect(request.signal.aborted).toBe(true)
    expect(submit(next).attributes('disabled')).toBeDefined()
    expect(next.text()).not.toContain('账单.xlsx')
    expect(imports()).toHaveLength(0)
    expect(view.emitted('saved')).toBeUndefined()
  })
})
