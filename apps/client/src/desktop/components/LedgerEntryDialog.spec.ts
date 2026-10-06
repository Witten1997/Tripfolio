import { webcrypto } from 'node:crypto'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ElFormItem, ElInput, ElSelect, ElMessageBox } from 'element-plus'
import { defineComponent, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import LedgerEntryDialog from './LedgerEntryDialog.vue'
import type { LedgerEntry } from '@/shared/api/ledger'
import type { TripMember } from '@/shared/api/members'
const { fetcher } = vi.hoisted(() => ({
  fetcher: vi.fn<(request: Request) => Promise<Response>>(),
}))
vi.mock('@/shared/api/client', async () => ({
  api: (await import('openapi-fetch')).default({
    baseUrl: 'https://tripfolio.test/api/v1',
    fetch: fetcher,
  }),
  registerRefreshHandler: vi.fn(),
}))
const tripId = '11111111-1111-4111-8111-111111111111'
const memberId = '22222222-2222-4222-8222-222222222222'
const otherId = '33333333-3333-4333-8333-333333333333'
const entryId = '44444444-4444-4444-8444-444444444444'
const context = {
  tripId,
  today: ref('2026-10-06'),
  minorUnits: ref(2),
  trip: ref({ currency_code: 'CNY', timezone: 'Asia/Shanghai' }),
}
vi.mock('@/shared/travel/tripContext', () => ({ useTripContext: () => context }))
const member: TripMember = {
  id: memberId,
  trip_id: tripId,
  name: '我',
  share_percent: '60',
  sort_order: 0,
  is_self: true,
  version: '1',
  created_at: '',
  updated_at: '',
  deleted_at: null,
}
const original: LedgerEntry = {
  id: entryId,
  trip_id: tripId,
  kind: 'expense',
  amount: '10.00',
  split_count: 1,
  personal_amount: '10.00',
  payer_member_id: memberId,
  split_mode: 'personal',
  splits: [{ member_id: memberId, amount: '10.00' }],
  currency_code: 'CNY',
  category_id: 'cat',
  occurred_on: '2026-10-06',
  notes: '原备注',
  refunded_entry_id: null,
  attachment_asset_ids: ['ticket'],
  version: '9007199254740993',
  created_at: '',
  updated_at: '',
  deleted_at: null,
}
const guard = (revision = 'a') => ({
  kind: 'members',
  scope_id: tripId,
  revision: `sha256:${revision.repeat(64)}`,
})
const Shell = defineComponent({
  props: ['modelValue', 'beforeClose'],
  template:
    '<section v-if="modelValue" role="dialog"><button aria-label="关闭" @click="beforeClose()">关闭</button><slot/><footer><slot name="footer"/></footer></section>',
})
const Asset = defineComponent({
  props: ['modelValue', 'disabled'],
  emits: ['update:modelValue', 'busy'],
  template: '<div data-testid="asset" :data-disabled="disabled">{{modelValue}}</div>',
})
const Keypad = defineComponent({
  props: ['modelValue', 'disabled', 'submitDisabled'],
  emits: ['update:modelValue', 'submit'],
  template: '<div data-testid="keypad" :data-disabled="disabled">{{modelValue}}</div>',
})
const DatePicker = defineComponent({
  props: ['modelValue', 'disabled'],
  emits: ['update:modelValue'],
  template: '<input :value="modelValue" :disabled="disabled" />',
})
let entity: LedgerEntry
let members: TripMember[]
let revision: string
let memberFailure: boolean
let missingGuard: boolean
let entityFailure: number | null
let memberHold: Promise<void> | null
let entityHold: Promise<void> | null
let requests: Request[]
let views: VueWrapper[]
let statuses: { status: number; code: string }[]
beforeEach(() => {
  vi.stubGlobal('crypto', webcrypto)
  context.trip.value.currency_code = 'CNY'
  context.minorUnits.value = 2
  entity = structuredClone(original)
  members = [
    { ...member },
    { ...member, id: otherId, name: '同行者', is_self: false, sort_order: 1, share_percent: '40' },
  ]
  revision = 'a'
  memberFailure = false
  missingGuard = false
  entityFailure = null
  memberHold = entityHold = null
  requests = []
  views = []
  statuses = []
  vi.spyOn(ElMessageBox, 'confirm').mockResolvedValue('confirm' as never)
  fetcher.mockReset().mockImplementation(async (request) => {
    requests.push(request.clone())
    const url = new URL(request.url)
    if (request.method === 'GET' && url.pathname.endsWith('/members')) {
      const response = {
        data: structuredClone(members),
        scope_revisions: missingGuard ? [] : [guard(revision)],
      }
      if (memberHold) await memberHold
      return memberFailure
        ? Response.json({ code: 'UNAVAILABLE', status: 503 }, { status: 503 })
        : Response.json(response)
    }
    if (request.method === 'GET' && url.searchParams.get('kind') === 'refund')
      return Response.json({
        items: [{ ...original, id: 'refund', kind: 'refund', amount: '3.00' }],
        next_cursor: null,
      })
    if (request.method === 'GET') {
      const response = structuredClone(entity)
      if (entityHold) await entityHold
      return entityFailure
        ? Response.json({ code: 'RESOURCE_GONE', status: entityFailure }, { status: entityFailure })
        : Response.json({ data: response })
    }
    const failure = statuses.shift()
    if (failure) return Response.json(failure, { status: failure.status })
    return Response.json({
      data: { data: null, warnings: [], replayed: true, scope_revisions: [guard('a')] },
    })
  })
})
afterEach(() => {
  views.forEach((v) => v.unmount())
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})
async function setup(mode: 'edit' | 'create' | 'closed' = 'edit', mobile = false) {
  vi.stubGlobal('matchMedia', () => ({
    matches: mobile,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }))
  const view = mount(LedgerEntryDialog, {
    attachTo: document.body,
    props: { categories: [{ id: 'cat', name: '餐饮', sort_order: 0, version: '1' }] as never },
    global: {
      stubs: {
        ResponsiveEditorShell: Shell,
        ElDrawer: true,
        ElDatePicker: DatePicker,
        AssetPicker: Asset,
        LedgerAmountKeypad: Keypad,
        CategoryCreateDrawer: true,
      },
    },
  })
  views.push(view)
  if (mode !== 'closed') await view.vm.open(mode === 'edit' ? entity : undefined)
  await flushPromises()
  return view
}
const button = (v: VueWrapper, label: string) =>
  v.findAll('button').find((b) => b.text() === label)!
const field = (v: VueWrapper, label: string) =>
  v.findAllComponents(ElFormItem).find((f) => f.props('label') === label)!
const writes = () => requests.filter((r) => r.method !== 'GET')
const memberReads = () => requests.filter((r) => r.url.endsWith('/members'))
async function amount(v: VueWrapper, value: string) {
  field(v, '金额').getComponent(ElInput).vm.$emit('update:modelValue', value)
  await flushPromises()
}
async function notes(v: VueWrapper, value: string) {
  field(v, '备注').getComponent(ElInput).vm.$emit('update:modelValue', value)
  await flushPromises()
}
async function click(v: VueWrapper, label: string) {
  await button(v, label).trigger('click')
  await flushPromises()
}
async function fillCreate(v: VueWrapper) {
  await amount(v, '12.50')
  field(v, '账单分类').getComponent(ElSelect).vm.$emit('update:modelValue', 'cat')
  await flushPromises()
}
async function review(v: VueWrapper) {
  await click(v, '读取最新资料')
}
const mine = '保留我的改动并采用已核对基线'

describe('账目完整基线与恢复', () => {
  it('每次打开完整读取成员，创建的上下文与原票据一同提交', async () => {
    const v = await setup('create')
    await fillCreate(v)
    v.getComponent(Asset).vm.$emit('update:modelValue', ['new-ticket'])
    await click(v, '保存')
    expect(writes()).toHaveLength(1)
    expect(writes()[0]!.headers.get('X-Collection-Guards')).toBe(JSON.stringify([guard()]))
    expect(await writes()[0]!.json()).toMatchObject({
      amount: '12.50',
      payer_member_id: memberId,
      participant_member_ids: [memberId],
      attachment_asset_ids: ['new-ticket'],
    })
    await v.vm.open()
    expect(memberReads()).toHaveLength(2)
    expect(v.emitted('saved')?.[0]?.[0]).toMatchObject({
      resource: null,
      result: { scope_revisions: [guard()] },
    })
  })
  it('成员读取失败仍可保存已有个人账目的备注，不产生财务差异', async () => {
    memberFailure = true
    const v = await setup()
    await notes(v, '只改备注')
    await click(v, '保存修改')
    expect(await writes()[0]!.json()).toEqual({ notes: '只改备注' })
    expect(writes()[0]!.headers.has('X-Collection-Guards')).toBe(false)
  })
  it.each([true, false])('缺少完整基线时%s新建或财务编辑不写入', async (creating) => {
    missingGuard = true
    const v = await setup(creating ? 'create' : 'edit')
    if (creating) await fillCreate(v)
    else await amount(v, '11')
    await click(v, creating ? '保存' : '保存修改')
    expect(writes()).toHaveLength(0)
  })
  it.each(['edit', 'create'] as const)(
    '%s unknown后401仍锁输入/关闭/重开/核对，直到原请求成功',
    async (mode) => {
      const v = await setup(mode)
      if (mode === 'create') await fillCreate(v)
      else await amount(v, '12.50')
      statuses = [
        { status: 503, code: 'UNAVAILABLE' },
        { status: 401, code: 'UNAUTHORIZED' },
      ]
      await click(v, mode === 'create' ? '保存' : '保存修改')
      const first = writes()[0]!
      const originalBody = await first.clone().json()
      await notes(v, '禁止的新备注')
      v.getComponent(Asset).vm.$emit('update:modelValue', ['forbidden-ticket'])
      await v.vm.refreshMembers()
      await click(v, '关闭')
      await v.vm.open({ ...entity, id: 'other' })
      await v.vm.openRefund(entity)
      expect(v.find('[role="dialog"]').exists()).toBe(true)
      expect(memberReads()).toHaveLength(1)
      expect(v.text()).not.toContain('禁止的新备注')
      expect(button(v, '重试确认').attributes('disabled')).toBeUndefined()
      await click(v, '重试确认')
      expect(v.text()).toContain('保存结果尚未确认')
      context.trip.value.currency_code = 'USD'
      await click(v, '重试确认')
      expect(writes()).toHaveLength(3)
      for (const req of writes()) {
        expect(await req.clone().json()).toEqual(originalBody)
        expect(req.headers.get('Idempotency-Key')).toBe(first.headers.get('Idempotency-Key'))
        expect(req.headers.get('If-Match')).toBe(first.headers.get('If-Match'))
        expect(req.headers.get('X-Collection-Guards')).toBe(
          first.headers.get('X-Collection-Guards'),
        )
      }
      expect(v.emitted('saved')).toHaveLength(1)
      expect(v.find('[role="dialog"]').exists()).toBe(false)
    },
  )
  it('移动unknown锁原生输入与键盘，独立重试按钮可用', async () => {
    const v = await setup('edit', true)
    v.getComponent(Keypad).vm.$emit('update:modelValue', '11')
    statuses = [{ status: 503, code: 'UNAVAILABLE' }]
    v.getComponent(Keypad).vm.$emit('submit')
    await flushPromises()
    expect(v.get('input[aria-label="备注"]').attributes('disabled')).toBeDefined()
    expect(v.getComponent(Keypad).props('disabled')).toBe(true)
    v.getComponent(Keypad).vm.$emit('update:modelValue', '99')
    await v.get('input[aria-label="备注"]').setValue('锁定')
    await click(v, '重试确认')
    expect(await writes()[1]!.json()).toEqual(await writes()[0]!.json())
  })
  it.each([412, 428])('%s候选只展示，确认吸收远端未编辑字段并保留本地主动差异', async (status) => {
    const v = await setup()
    await amount(v, '12.50')
    statuses = [
      { status, code: status === 412 ? 'COLLECTION_CONFLICT' : 'COLLECTION_BASE_REQUIRED' },
    ]
    await click(v, '保存修改')
    const first = writes()[0]!
    entity = {
      ...entity,
      version: '9007199254740994',
      notes: '远端备注',
      attachment_asset_ids: ['remote-ticket'],
    }
    revision = 'b'
    await review(v)
    expect(field(v, '备注').getComponent(ElInput).props('modelValue')).toBe('原备注')
    expect(v.getComponent(Asset).props('modelValue')).toEqual(['ticket'])
    await click(v, '保存修改')
    expect(writes()).toHaveLength(1)
    await click(v, mine)
    expect(field(v, '备注').getComponent(ElInput).props('modelValue')).toBe('远端备注')
    expect(v.getComponent(Asset).props('modelValue')).toEqual(['remote-ticket'])
    expect(writes()).toHaveLength(1)
    await click(v, '保存修改')
    expect(await writes()[1]!.json()).toEqual({ amount: '12.50', currency_code: 'CNY' })
    expect(writes()[1]!.headers.get('If-Match')).toBe('"9007199254740994"')
    expect(writes()[1]!.headers.get('X-Collection-Guards')).toBe(JSON.stringify([guard('b')]))
    expect(writes()[1]!.headers.get('Idempotency-Key')).not.toBe(
      first.headers.get('Idempotency-Key'),
    )
  })
  it('实体冲突自动latest仅供展示，集合核对之前所有保存入口均拒绝', async () => {
    const v = await setup()
    await notes(v, '主动备注')
    statuses = [
      { status: 412, code: 'VERSION_CONFLICT' },
      { status: 412, code: 'COLLECTION_CONFLICT' },
    ]
    entity = { ...entity, version: '2', amount: '25.00' }
    revision = 'b'
    await click(v, '保存修改')
    expect(memberReads()).toHaveLength(1)
    await click(v, '保存修改')
    expect(writes()).toHaveLength(1)
    await review(v)
    await click(v, mine)
    expect(field(v, '金额').getComponent(ElInput).props('modelValue')).toBe('25.00')
    await click(v, '保存修改')
    expect(await writes()[1]!.json()).toEqual({ notes: '主动备注' })
    expect(button(v, '保存修改').attributes('disabled')).toBeDefined()
    await review(v)
    await click(v, mine)
    await click(v, '保存修改')
    expect(v.emitted('saved')).toHaveLength(1)
  })
  it('外部成员刷新不GET不替换；取消和重读失败不可采用旧候选', async () => {
    const v = await setup()
    await notes(v, '本地')
    await v.vm.refreshMembers()
    await flushPromises()
    expect(memberReads()).toHaveLength(1)
    entity = { ...entity, notes: '远端', version: '2' }
    revision = 'b'
    await review(v)
    await click(v, '取消核对')
    expect(button(v, mine)).toBeUndefined()
    await review(v)
    memberFailure = true
    await review(v)
    expect(button(v, mine)).toBeUndefined()
    expect(field(v, '备注').getComponent(ElInput).props('modelValue')).toBe('本地')
    expect(writes()).toHaveLength(0)
  })
  it('明确放弃输入同时采用实体与成员，未自动提交', async () => {
    const v = await setup()
    await amount(v, '12')
    await v.vm.refreshMembers()
    await flushPromises()
    entity = { ...entity, notes: '远端', version: '2' }
    revision = 'b'
    await review(v)
    await click(v, '放弃输入采用最新')
    expect(field(v, '金额').getComponent(ElInput).props('modelValue')).toBe('10.00')
    expect(writes()).toHaveLength(0)
    await amount(v, '13')
    await click(v, '保存修改')
    expect(writes()[0]!.headers.get('X-Collection-Guards')).toBe(JSON.stringify([guard('b')]))
  })
  it.each([404, 410])('核对%s保留草稿和票据，候选不能采用', async (status) => {
    const v = await setup()
    await notes(v, '保留')
    await v.vm.refreshMembers()
    await flushPromises()
    entityFailure = status
    await review(v)
    expect(button(v, mine)).toBeUndefined()
    expect(field(v, '备注').getComponent(ElInput).props('modelValue')).toBe('保留')
    expect(v.getComponent(Asset).props('modelValue')).toEqual(['ticket'])
  })
  it('采用新成员不会过滤旧付款人/参与人；财务保存提示重新选择', async () => {
    entity = {
      ...entity,
      split_mode: 'even',
      splits: [
        { member_id: memberId, amount: '6.00' },
        { member_id: otherId, amount: '4.00' },
      ],
    }
    const v = await setup()
    await amount(v, '20')
    await v.vm.refreshMembers()
    await flushPromises()
    members = [members[1]!]
    revision = 'b'
    await review(v)
    await click(v, mine)
    await click(v, '保存修改')
    expect(writes()).toHaveLength(0)
    expect(v.text()).toContain('付款人已移除')
    await vi.waitFor(() => expect(v.text()).toContain('参与人已移除'))
  })
  it('重开使迟到成员和实体读取失效', async () => {
    const v = await setup('closed')
    let release!: () => void
    memberHold = entityHold = new Promise<void>((resolve) => {
      release = resolve
    })
    const old = v.vm.open(entity)
    await flushPromises()
    entity = { ...entity, id: 'new', notes: '新记录', version: '2' }
    revision = 'b'
    memberHold = entityHold = null
    await v.vm.open(entity)
    release()
    await old
    await flushPromises()
    expect(field(v, '备注').getComponent(ElInput).props('modelValue')).toBe('新记录')
    await amount(v, '13')
    await click(v, '保存修改')
    expect(writes()[0]!.url).toContain('/new')
    expect(writes()[0]!.headers.get('X-Collection-Guards')).toBe(JSON.stringify([guard('b')]))
  })
  it('退款按最新余额预填，成员候选确认不改金额和原支出关联', async () => {
    const v = await setup('closed')
    await v.vm.openRefund(entity)
    await flushPromises()
    expect(field(v, '金额').getComponent(ElInput).props('modelValue')).toBe('7.00')
    await amount(v, '5')
    statuses = [{ status: 412, code: 'COLLECTION_CONFLICT' }]
    await click(v, '保存')
    revision = 'b'
    await review(v)
    await click(v, mine)
    await click(v, '保存')
    expect(await writes()[1]!.json()).toMatchObject({
      amount: '5.00',
      kind: 'refund',
      refunded_entry_id: entryId,
      category_id: 'cat',
    })
    expect(writes()[1]!.headers.get('X-Collection-Guards')).toBe(JSON.stringify([guard('b')]))
  })
  it('核对关闭后迟到候选不安装；卸载后的打开读取也不继续', async () => {
    const v = await setup()
    await notes(v, '保留')
    await v.vm.refreshMembers()
    await flushPromises()
    let release!: () => void
    memberHold = new Promise<void>((resolve) => {
      release = resolve
    })
    await button(v, '读取最新资料').trigger('click')
    await click(v, '关闭')
    release()
    await flushPromises()
    expect(v.find('[role="dialog"]').exists()).toBe(false)
    expect(button(v, mine)).toBeUndefined()
    memberHold = new Promise<void>((resolve) => {
      release = resolve
    })
    const opening = v.vm.open(entity)
    await flushPromises()
    v.unmount()
    views = views.filter((item) => item !== v)
    release()
    await opening
    expect(writes()).toHaveLength(0)
  })
  it('保留主动附件与参与人顺序，不把相同数组当成主动修改', async () => {
    entity = {
      ...entity,
      split_mode: 'even',
      splits: [
        { member_id: memberId, amount: '6.00' },
        { member_id: otherId, amount: '4.00' },
      ],
    }
    const v = await setup()
    v.getComponent(Asset).vm.$emit('update:modelValue', ['mine-ticket', 'ticket'])
    await v.vm.refreshMembers()
    await flushPromises()
    entity = {
      ...entity,
      version: '2',
      notes: 'remote',
      splits: [
        { member_id: otherId, amount: '4.00' },
        { member_id: memberId, amount: '6.00' },
      ],
    }
    revision = 'b'
    await review(v)
    await click(v, mine)
    await click(v, '保存修改')
    expect(await writes()[0]!.json()).toEqual({ attachment_asset_ids: ['mine-ticket', 'ticket'] })
  })
  it('集合冲突后实体冲突仍需重新组合核对', async () => {
    const v = await setup()
    await amount(v, '12')
    statuses = [
      { status: 428, code: 'COLLECTION_BASE_REQUIRED' },
      { status: 412, code: 'VERSION_CONFLICT' },
    ]
    await click(v, '保存修改')
    revision = 'b'
    await review(v)
    await click(v, mine)
    entity = { ...entity, version: '3', notes: '并发备注' }
    await click(v, '保存修改')
    expect(memberReads()).toHaveLength(2)
    expect(button(v, '保存修改').attributes('disabled')).toBeDefined()
    revision = 'c'
    await review(v)
    await click(v, mine)
    await click(v, '保存修改')
    expect(writes()[2]!.headers.get('If-Match')).toBe('"3"')
    expect(writes()[2]!.headers.get('X-Collection-Guards')).toBe(JSON.stringify([guard('c')]))
  })
  it('移动冲突键盘submit不能绕过核对', async () => {
    const v = await setup('edit', true)
    v.getComponent(Keypad).vm.$emit('update:modelValue', '11')
    statuses = [{ status: 412, code: 'VERSION_CONFLICT' }]
    v.getComponent(Keypad).vm.$emit('submit')
    await flushPromises()
    v.getComponent(Keypad).vm.$emit('submit')
    await flushPromises()
    expect(writes()).toHaveLength(1)
    expect(v.getComponent(Keypad).props('submitDisabled')).toBe(true)
  })
  it('取消关闭不清本地草稿，不能用另一次打开覆盖', async () => {
    const v = await setup()
    await notes(v, '未保存')
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel')
    await v.vm.open({ ...entity, id: 'different' })
    expect(memberReads()).toHaveLength(1)
    expect(field(v, '备注').getComponent(ElInput).props('modelValue')).toBe('未保存')
  })
  it('首次成员读取失败后无输入重试可建立首份完整基线', async () => {
    memberFailure = true
    const v = await setup('create')
    memberFailure = false
    await click(v, '重试')
    await fillCreate(v)
    await click(v, '保存')
    expect(writes()).toHaveLength(1)
    expect(writes()[0]!.headers.get('X-Collection-Guards')).toBe(JSON.stringify([guard()]))
  })
  it('成功后的父刷新失败不恢复未知，也不重复写入', async () => {
    const v = await setup()
    const reloadFailure = vi.fn()
    await v.setProps({
      onSaved: () => {
        Promise.reject(new Error('读取失败')).catch(reloadFailure)
      },
    })
    await notes(v, '已保存')
    await click(v, '保存修改')
    expect(reloadFailure).toHaveBeenCalledOnce()
    expect(v.find('[role="dialog"]').exists()).toBe(false)
    expect(writes()).toHaveLength(1)
    await v.vm.open(entity)
    await flushPromises()
    expect(v.text()).not.toContain('保存结果尚未确认')
  })
})
