import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { ElMessageBox } from 'element-plus'
import TripMembersDialog from './TripMembersDialog.vue'
import { json, problem, receipt, tripId } from '@/shared/travel/__tests__/fixtures'
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
const guards = [{ kind: 'members', scope_id: tripId, revision: `sha256:${'a'.repeat(64)}` }]
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
const Shell = defineComponent({
  props: ['modelValue', 'title', 'beforeClose'],
  template:
    '<section v-if="modelValue" role="dialog"><button @click="beforeClose()">关闭</button><slot/><footer><slot name="footer"/></footer></section>',
})
const Draggable = defineComponent({
  props: ['modelValue', 'disabled'],
  template: '<ol><slot/></ol>',
})
let view: VueWrapper | undefined
beforeEach(() => {
  setActivePinia(createPinia())
  transport.mockReset()
  vi.spyOn(ElMessageBox, 'confirm').mockResolvedValue('confirm' as never)
})
afterEach(() => {
  view?.unmount()
  view = undefined
  vi.restoreAllMocks()
})
async function setup() {
  const result = mount(TripMembersDialog, {
    props: { tripId },
    global: { stubs: { ResponsiveEditorShell: Shell, VueDraggable: Draggable } },
  })
  view = result
  await result.vm.open()
  await flushPromises()
  return result
}
const button = (v: VueWrapper, text: string) => v.findAll('button').find((b) => b.text() === text)!
const name = (v: VueWrapper) => v.get('input[aria-label="成员名称 1"]')
describe('成员管理冲突交互', () => {
  it('缺基线时不显示可编辑成员且禁保存', async () => {
    transport.mockResolvedValue(json(200, { data: [self] }))
    const v = await setup()
    expect(v.text()).toContain('基线')
    expect(button(v, '保存').attributes('disabled')).toBeDefined()
    expect(v.findAll('input')).toHaveLength(0)
  })
  it('412后输入保留，读取候选与取消确认不覆盖，明确确认才采用', async () => {
    let gets = 0
    transport.mockImplementation(async (req) =>
      req.method === 'PUT'
        ? problem('COLLECTION_CONFLICT', 412)
        : json(200, {
            data: [{ ...self, name: ++gets === 1 ? '我' : '远端成员' }],
            scope_revisions: guards,
          }),
    )
    const v = await setup()
    await name(v).setValue('当前输入')
    await button(v, '保存').trigger('click')
    await flushPromises()
    expect((name(v).element as HTMLInputElement).value).toBe('当前输入')
    expect(button(v, '保存').attributes('disabled')).toBeDefined()
    await button(v, '查看最新成员').trigger('click')
    await flushPromises()
    expect(v.get('section[aria-label="最新成员"]').text()).toContain('远端成员')
    expect((name(v).element as HTMLInputElement).value).toBe('当前输入')
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel')
    await button(v, '采用最新成员').trigger('click')
    await flushPromises()
    expect((name(v).element as HTMLInputElement).value).toBe('当前输入')
    await button(v, '采用最新成员').trigger('click')
    await flushPromises()
    expect((name(v).element as HTMLInputElement).value).toBe('远端成员')
    expect(ElMessageBox.confirm).toHaveBeenCalledWith(
      expect.stringContaining('未保存'),
      '采用最新成员',
      expect.any(Object),
    )
  })
  it('未知结果锁住输入，关闭再开保留原样重试', async () => {
    const puts: Request[] = []
    transport.mockImplementation(async (req) => {
      if (req.method === 'GET') return json(200, { data: [self], scope_revisions: guards })
      puts.push(req.clone())
      if (puts.length === 1) throw new Error('offline')
      return json(200, { data: receipt(null, [], true) })
    })
    const v = await setup()
    await name(v).setValue('原提交')
    await button(v, '保存').trigger('click')
    await flushPromises()
    expect(name(v).attributes('disabled')).toBeDefined()
    expect(button(v, '添加成员').attributes('disabled')).toBeDefined()
    await button(v, '关闭').trigger('click')
    await flushPromises()
    expect(v.find('[role="dialog"]').exists()).toBe(false)
    await v.vm.open()
    await flushPromises()
    expect((name(v).element as HTMLInputElement).value).toBe('原提交')
    expect(transport).toHaveBeenCalledTimes(2)
    await button(v, '重试确认').trigger('click')
    await flushPromises()
    expect(await puts[1]!.json()).toEqual(await puts[0]!.json())
    expect(puts[1]!.headers.get('Idempotency-Key')).toBe(puts[0]!.headers.get('Idempotency-Key'))
    expect(v.emitted('saved')).toHaveLength(1)
  })
})
