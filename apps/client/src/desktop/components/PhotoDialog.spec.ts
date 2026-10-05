import { createHash, webcrypto } from 'node:crypto'
import { TextEncoder } from 'node:util'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ElFormItem, ElInputNumber, ElMessageBox, ElRadioGroup } from 'element-plus'
import { defineComponent, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const { fetcher } = vi.hoisted(() => ({ fetcher: vi.fn() }))
vi.mock('@/shared/api/client', async () => ({
  api: (await import('openapi-fetch')).default({
    baseUrl: 'https://tripfolio.test/api/v1',
    fetch: fetcher,
  }),
  registerRefreshHandler: vi.fn(),
}))
const trip = '11111111-1111-4111-8111-111111111111'
const day = '2026-10-06',
  next = '2026-10-07'
const context = {
  tripId: trip,
  today: ref(day),
  trip: ref({ timezone: 'Asia/Shanghai', destination: '' }),
}
vi.mock('@/shared/travel/tripContext', () => ({ useTripContext: () => context }))
import PhotoDialog from './PhotoDialog.vue'
import type { Photo } from '@/shared/api/photos'

const id = '00000000-0000-4000-8000-000000000001'
const assetId = '00000000-0000-4000-8000-000000000099'
const epoch = '22222222-2222-4222-8222-222222222222'
const original: Photo = {
  id,
  trip_id: trip,
  asset_id: assetId,
  version: '9007199254740993',
  recorded_on: day,
  taken_at_local: `${day}T10:00:00`,
  sort_order: 0,
  caption: '原说明',
  place_name: '',
  address: '',
  latitude: null,
  longitude: null,
  created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-01T00:00:00Z',
  deleted_at: null,
}
const Dialog = defineComponent({
  props: ['modelValue'],
  template:
    '<section v-if="modelValue" role="dialog"><slot/><footer><slot name="footer"/></footer></section>',
})
const Asset = defineComponent({
  name: 'AssetPickerStub',
  props: ['modelValue', 'disabled'],
  emits: ['update:modelValue', 'loaded', 'busy'],
  template: '<div data-testid="asset" :data-disabled="disabled">{{ modelValue }}</div>',
})
const DatePicker = defineComponent({
  name: 'DatePickerStub',
  props: ['modelValue', 'disabled', 'type'],
  emits: ['update:modelValue'],
  template: '<input :value="modelValue" :disabled="disabled" />',
})
let current: Photo
let status: number | null
let requests: Request[]
let views: VueWrapper[]
let holdPage: Promise<void> | null
beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-10-06T00:00:00Z'))
  vi.stubGlobal('crypto', webcrypto)
  vi.stubGlobal('TextEncoder', TextEncoder)
  current = { ...original }
  status = null
  requests = []
  views = []
  holdPage = null
  vi.spyOn(ElMessageBox, 'confirm').mockResolvedValue('confirm' as never)
  fetcher.mockReset().mockImplementation(async (request: Request) => {
    requests.push(request.clone())
    const url = new URL(request.url)
    if (url.pathname.endsWith('/collection-baselines')) {
      if (holdPage) await holdPage
      const scope = url.searchParams.get('scope_id')!
      const items = scope.endsWith(current.recorded_on) ? [current] : []
      return Response.json({
        kind: 'photo_day',
        scope_id: scope,
        sync_epoch: epoch,
        revision: `sha256:${createHash('sha256')
          .update(
            `${epoch}\nphoto_day\n${scope}\n${items.map((i) => `${i.id}:${i.version}\n`).join('')}`,
          )
          .digest('hex')}`,
        expires_at: '2026-10-06T01:00:00Z',
        items,
        next_cursor: null,
      })
    }
    if (request.method === 'GET') return Response.json({ data: current })
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
    return Response.json({ data: { data: current, warnings: [], scope_revisions: [] } })
  })
})
afterEach(() => {
  views.forEach((view) => view.unmount())
  vi.restoreAllMocks()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})
async function setup(item: Photo | undefined = current) {
  const view = mount(PhotoDialog, {
    attachTo: document.body,
    global: {
      stubs: {
        ElDialog: Dialog,
        ElDrawer: Dialog,
        ElDatePicker: DatePicker,
        AssetPicker: Asset,
        PlacePicker: true,
      },
    },
  })
  views.push(view)
  await view.vm.open(item)
  await flushPromises()
  return view
}
const button = (view: VueWrapper, text: string) =>
  view.findAll('button').find((b) => b.text() === text)!
const field = (view: VueWrapper, label: string) =>
  view.findAllComponents(ElFormItem).find((f) => f.props('label') === label)!
const writes = () => requests.filter((r) => r.method !== 'GET')
const ready = async (view: VueWrapper) => {
  await button(view, '准备日期与排序编辑').trigger('click')
  await vi.waitFor(() => expect(view.findComponent(ElInputNumber).props('disabled')).toBe(false))
}

describe('照片弹窗', () => {
  it('完整读取前禁用日期和顺序，说明可以独立保存', async () => {
    const view = await setup()
    expect(view.findComponent(ElInputNumber).props('disabled')).toBe(true)
    expect(view.findAllComponents(DatePicker).every((p) => p.props('disabled'))).toBe(true)
    await view.find('textarea').setValue('仅修改说明')
    await button(view, '保存').trigger('click')
    await flushPromises()
    expect(await writes()[0]!.json()).toEqual({ caption: '仅修改说明' })
    expect(writes()[0]!.headers.has('X-Collection-Guards')).toBe(false)
    expect(view.emitted('saved')).toHaveLength(1)
  })
  it('准备期间保留说明、禁止保存；目标读完前不改变日期', async () => {
    const view = await setup()
    let release!: () => void
    holdPage = new Promise<void>((resolve) => {
      release = resolve
    })
    await button(view, '准备日期与排序编辑').trigger('click')
    await view.find('textarea').setValue('读取中的输入')
    expect(button(view, '保存').attributes('disabled')).toBeDefined()
    release()
    await readyAfterLoad(view)
    holdPage = new Promise<void>((resolve) => {
      release = resolve
    })
    field(view, '分组日期').findComponent(DatePicker).vm.$emit('update:modelValue', next)
    await flushPromises()
    expect(field(view, '分组日期').findComponent(DatePicker).props('modelValue')).toBe(day)
    release()
    await vi.waitFor(() =>
      expect(field(view, '分组日期').findComponent(DatePicker).props('modelValue')).toBe(next),
    )
    expect((view.find('textarea').element as HTMLTextAreaElement).value).toBe('读取中的输入')
  })
  it.each([412, 428])('%s保留照片及草稿，核对/取消不替换，确认才采用', async (failure) => {
    const view = await setup()
    await ready(view)
    await view.find('textarea').setValue('本地草稿')
    view.findComponent(ElInputNumber).vm.$emit('update:modelValue', 3)
    await flushPromises()
    status = failure
    await button(view, '保存').trigger('click')
    await flushPromises()
    expect(view.text()).toContain('输入及原日期资料已保留')
    expect((view.find('textarea').element as HTMLTextAreaElement).value).toBe('本地草稿')
    expect(view.find('[data-testid="asset"]').text()).toContain(assetId)
    current = { ...current, version: '9007199254740994', caption: '远端说明' }
    await button(view, '核对最新照片与日期').trigger('click')
    await vi.waitFor(() => expect(view.text()).toContain('最新照片说明：远端说明'))
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel')
    await button(view, '放弃输入并采用最新').trigger('click')
    await flushPromises()
    expect((view.find('textarea').element as HTMLTextAreaElement).value).toBe('本地草稿')
    await button(view, '放弃输入并采用最新').trigger('click')
    await flushPromises()
    expect((view.find('textarea').element as HTMLTextAreaElement).value).toBe('远端说明')
    expect(writes()).toHaveLength(1)
  })
  it('未知更新禁用字段、关闭和核对，原样重试按钮仍可用', async () => {
    const view = await setup()
    await ready(view)
    view.findComponent(ElInputNumber).vm.$emit('update:modelValue', 3)
    await flushPromises()
    status = 503
    await button(view, '保存').trigger('click')
    await flushPromises()
    expect(button(view, '取消').attributes('disabled')).toBeDefined()
    expect(button(view, '核对最新照片与日期').attributes('disabled')).toBeDefined()
    expect(view.find('textarea').attributes('disabled')).toBeDefined()
    expect(button(view, '原样重试').attributes('disabled')).toBeUndefined()
    const first = writes()[0]!
    status = null
    await button(view, '原样重试').trigger('click')
    await flushPromises()
    expect(await writes()[1]!.json()).toEqual(await first.json())
    expect(writes()[1]!.headers.get('Idempotency-Key')).toBe(first.headers.get('Idempotency-Key'))
    expect(writes()[1]!.headers.get('X-Collection-Guards')).toBe(
      first.headers.get('X-Collection-Guards'),
    )
    expect(view.emitted('saved')).toHaveLength(1)
  })
  it.each(['append', 'explicit'] as const)('创建默认追加与手工零值：%s', async (mode) => {
    const view = await setup()
    await button(view, '取消').trigger('click')
    await view.vm.open()
    await flushPromises()
    view.findComponent(Asset).vm.$emit('update:modelValue', [assetId])
    view.findComponent(ElRadioGroup).vm.$emit('update:modelValue', mode)
    await flushPromises()
    await button(view, '保存').trigger('click')
    await flushPromises()
    expect((await writes()[0]!.json()).sort_order).toBe(mode === 'append' ? undefined : 0)
    expect(
      requests.filter((r) => new URL(r.url).pathname.endsWith('/collection-baselines')),
    ).toHaveLength(0)
  })
})
async function readyAfterLoad(view: VueWrapper) {
  await vi.waitFor(() => expect(view.findComponent(ElInputNumber).props('disabled')).toBe(false))
}
