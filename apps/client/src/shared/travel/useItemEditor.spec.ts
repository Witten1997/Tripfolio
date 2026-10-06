import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive, ref } from 'vue'

import { ApiError } from '@/shared/api/auth'
import { writeOutcome, type WriteOutcome } from '@/shared/api/writes'
import * as writes from '@/shared/api/writes'
import { deferred, receipt } from '@/shared/travel/__tests__/fixtures'
import { DraftError } from '@/shared/travel/tripDraft'
import { useItemEditor, type ItemEditorSpec } from '@/shared/travel/useItemEditor'

interface Note {
  id: string
  version: string
  title: string
  notes: string
}
interface NoteDraft {
  title: string
  notes: string
}

function note(overrides: Partial<Note> = {}): Note {
  return { id: 'n1', version: '1', title: '旧标题', notes: '', ...overrides }
}

function problem(code: string, status: number, extra: object = {}) {
  return new ApiError({ type: 'about:blank', title: code, status, code, request_id: 'r', ...extra })
}

type NoteValues = NoteDraft
type NotePatch = Partial<NoteDraft>

function makeSpec<S = undefined>(
  overrides: Partial<ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch, S>> = {},
) {
  const calls = { create: [] as unknown[], update: [] as unknown[], get: 0 }
  const spec: ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch, S> = {
    emptyDraft: () => ({ title: '', notes: '' }),
    draftFrom: (item) => ({ title: item.title, notes: item.notes }),
    validate: (draft) => {
      if (!draft.title.trim()) throw new DraftError({ title: '标题必填' })
      return { title: draft.title.trim(), notes: draft.notes }
    },
    diff: (values, baseline) => {
      const patch: NotePatch = {}
      for (const key of ['title', 'notes'] as const) {
        if (values[key] !== baseline[key]) patch[key] = values[key]
      }
      return patch
    },
    get: async (id) => {
      calls.get++
      return note({ id })
    },
    create: async (body, operationId) => {
      calls.create.push({ body, operationId })
      return writeOutcome<Note>(receipt(note({ ...(body as object) })), () => true)
    },
    update: async (id, version, patch, operationId) => {
      calls.update.push({ id, version, patch, operationId })
      return writeOutcome<Note>(receipt(note({ version: '2', ...(patch as object) })), () => true)
    },
    ...overrides,
  }
  return { spec, calls }
}

beforeEach(() => setActivePinia(createPinia()))
afterEach(() => vi.restoreAllMocks())

describe('useItemEditor', () => {
  it('创建时携带客户端 id 并在成功后关闭，操作号对相同草稿稳定', async () => {
    const { spec, calls } = makeSpec()
    const editor = useItemEditor(spec)
    await editor.open()
    editor.draft.title = '  新标题 '
    const outcome = await editor.save()
    expect(outcome?.resource?.title).toBe('新标题')
    expect(editor.opened.value).toBe(false)
    const call = calls.create[0] as { body: { id: string; title: string }; operationId: string }
    expect(call.body.id).toMatch(/^[0-9a-f-]{36}$/)
    expect(call.body.title).toBe('新标题')
    expect(call.operationId).toMatch(/^[0-9a-f-]{36}$/)
  })

  it('编辑时只提交变化字段；无修改时不请求', async () => {
    const { spec, calls } = makeSpec()
    const editor = useItemEditor(spec)
    await editor.open(note())
    expect(calls.get).toBe(1)
    expect(editor.dirty.value).toBe(false)
    expect(await editor.save()).toBeNull()
    expect(editor.error.value).toContain('没有需要保存')
    editor.draft.notes = '补充'
    await editor.save()
    expect(calls.update[0]).toMatchObject({ id: 'n1', version: '1', patch: { notes: '补充' } })
  })

  it('校验失败在请求前拦截并给出字段错误', async () => {
    const { spec, calls } = makeSpec()
    const editor = useItemEditor(spec)
    await editor.open()
    expect(await editor.save()).toBeNull()
    expect(editor.errors.value.title).toBe('标题必填')
    expect(calls.create).toHaveLength(0)
  })

  it('412 进入冲突态并载入最新版本；对最新版本重提仍只含主动改动', async () => {
    let attempts = 0
    const { spec, calls } = makeSpec({
      get: async (id) => note({ id, version: '3', title: '别人改的标题', notes: '' }),
      update: async (id, version, patch, operationId) => {
        calls.update.push({ id, version, patch, operationId })
        attempts++
        if (attempts === 1) throw problem('VERSION_CONFLICT', 412)
        return writeOutcome<Note>(receipt(note({ version: '4' })), () => true)
      },
    })
    const editor = useItemEditor(spec)
    await editor.open(note())
    editor.draft.notes = '我的备注'
    expect(await editor.save()).toBeNull()
    expect(editor.conflict.value).toBe(true)
    expect(editor.latest.value?.version).toBe('3')
    expect(await editor.save(true)).not.toBeNull()
    expect(calls.update[1]).toMatchObject({ version: '3', patch: { notes: '我的备注' } })
  })

  it('创建响应丢失后锁定为不确定状态，重试原样重放同一操作号', async () => {
    let first = true
    const { spec, calls } = makeSpec({
      create: async (body, operationId) => {
        calls.create.push({ body, operationId })
        if (first) {
          first = false
          throw new TypeError('network')
        }
        return writeOutcome<Note>(receipt(note(), [], true), () => true)
      },
    })
    const editor = useItemEditor(spec)
    await editor.open()
    editor.draft.title = '标题'
    expect(await editor.save()).toBeNull()
    expect(editor.uncertainCreate.value).toBe(true)
    editor.draft.title = '被改动'
    expect(await editor.save()).not.toBeNull()
    const [a, b] = calls.create as Array<{ body: { title: string }; operationId: string }>
    expect(b!.operationId).toBe(a!.operationId)
    expect(b!.body.title).toBe('标题')
  })

  it('打开时切换目标使旧的加载结果作废', async () => {
    const slow = deferred<Note>()
    const { spec } = makeSpec({ get: async (id) => (id === 'slow' ? slow.promise : note({ id })) })
    const editor = useItemEditor(spec)
    const opening = editor.open(note({ id: 'slow' }))
    await editor.open(note({ id: 'fast' }))
    slow.resolve(note({ id: 'slow', title: '慢' }))
    await opening
    expect(editor.baseline.value?.id).toBe('fast')
  })

  it('draft 是响应式对象，可直接绑定表单', async () => {
    const { spec } = makeSpec()
    const editor = useItemEditor(spec)
    await editor.open()
    const form = reactive(editor.draft)
    form.title = 'x'
    expect(editor.dirty.value).toBe(true)
  })
})

interface GuardContext {
  guards: Array<{ kind: string; scope_id: string; revision: string }>
  orderMode: 'append' | 'explicit'
}

function guardContext(): GuardContext {
  return {
    guards: [{ kind: 'members', scope_id: 'trip1', revision: 'old' }],
    orderMode: 'append',
  }
}

function observeIntent() {
  const original = writes.createWriteIntent
  const inputs: unknown[] = []
  vi.spyOn(writes, 'createWriteIntent').mockImplementation(() => {
    const intent = original()
    return {
      ...intent,
      key: (input) => {
        inputs.push(input)
        return intent.key(input)
      },
    }
  })
  return inputs
}

describe('useItemEditor 提交上下文', () => {
  it('无 hook 时保留原指纹输入和回调参数个数', async () => {
    const inputs = observeIntent()
    const { spec } = makeSpec()
    const create = vi.fn(spec.create)
    const update = vi.fn(spec.update)
    // 显式四泛型及原两/四参数回调仍可编译。
    const editor = useItemEditor<Note, NoteDraft, NoteValues, NotePatch>({
      ...spec,
      create,
      update,
    })
    await editor.open()
    editor.draft.title = '新标题'
    await editor.save()
    const first = create.mock.calls[0]!
    expect(first).toHaveLength(2)
    expect(inputs[0]).toEqual({ id: first[0].id, body: { title: '新标题', notes: '' } })
    await editor.open(note())
    editor.draft.notes = '备注'
    await editor.save()
    expect(update.mock.calls[0]).toHaveLength(4)
    expect(inputs[1]).toEqual({ id: 'n1', version: '1', patch: { notes: '备注' } })
  })

  it('同一深冻结快照进入指纹和发送，源数据变化不修改在途请求', async () => {
    const inputs = observeIntent()
    const source = guardContext()
    const response = deferred<WriteOutcome<Note>>()
    const capture = vi.fn(() => source)
    const create = vi.fn<
      ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch, GuardContext>['create']
    >(() => response.promise)
    const { spec } = makeSpec({ captureIntentContext: capture, create })
    const editor = useItemEditor(spec)
    await editor.open()
    editor.draft.title = '标题'
    const saving = editor.save()
    const [body, , context] = create.mock.calls[0]!
    expect(inputs[0]).toMatchObject({ request: { body: { title: '标题' } }, context: source })
    expect((inputs[0] as { context: GuardContext }).context).toBe(context)
    expect(context).not.toBe(source)
    expect(Object.isFrozen(context)).toBe(true)
    expect(Object.isFrozen(context!.guards)).toBe(true)
    expect(Object.isFrozen(context!.guards[0])).toBe(true)
    expect(Object.isFrozen(body)).toBe(true)
    expect(capture).toHaveBeenCalledWith({ kind: 'create', body })
    source.guards[0]!.revision = 'new'
    source.orderMode = 'explicit'
    editor.draft.title = '发送期间编辑'
    expect(context!.guards[0]!.revision).toBe('old')
    expect(context!.orderMode).toBe('append')
    expect(body.title).toBe('标题')
    response.resolve(writeOutcome<Note>(receipt(note()), () => true))
    await saving
  })

  it.each(['create', 'update'] as const)('%s 相同请求换上下文得到新操作号', async (kind) => {
    const source = guardContext()
    const create = vi.fn(async () => {
      throw problem('COLLECTION_CONFLICT', 412)
    })
    const update = vi.fn(async () => {
      throw problem('COLLECTION_CONFLICT', 412)
    })
    const { spec, calls } = makeSpec<GuardContext>({
      captureIntentContext: () => source,
      create,
      update,
    })
    const editor = useItemEditor(spec)
    await editor.open(kind === 'update' ? note() : undefined)
    editor.draft.title = '我的修改'
    const getCount = calls.get
    await editor.save()
    await editor.save()
    source.guards[0]!.revision = 'new'
    await editor.save()
    source.orderMode = 'explicit'
    await editor.save()
    const submissions = (kind === 'create' ? create : update).mock.calls as unknown as unknown[][]
    const idIndex = kind === 'create' ? 1 : 3
    expect(submissions[1]![idIndex]).toBe(submissions[0]![idIndex])
    expect(submissions[2]![idIndex]).not.toBe(submissions[1]![idIndex])
    expect(submissions[3]![idIndex]).not.toBe(submissions[2]![idIndex])
    expect(calls.get).toBe(getCount)
    expect(editor.draft.title).toBe('我的修改')
    expect(editor.opened.value).toBe(true)
  })

  it.each<[string, () => Error]>([
    ['网络失败', () => new TypeError('network')],
    ['服务端错误', () => problem('INTERNAL_ERROR', 500)],
  ])('创建%s后按原正文/编号/context重试，不重新捕获', async (_, failure) => {
    const source = guardContext()
    const capture = vi.fn(() => source)
    const create = vi
      .fn<ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch, GuardContext>['create']>()
      .mockRejectedValueOnce(failure())
      .mockResolvedValue(writeOutcome<Note>(receipt(note(), [], true), () => true))
    const { spec } = makeSpec({ captureIntentContext: capture, create })
    const editor = useItemEditor(spec)
    await editor.open()
    editor.draft.title = '原始标题'
    await editor.save()
    source.guards[0]!.revision = 'changed'
    editor.draft.title = '改过的标题'
    await editor.save()
    expect(capture).toHaveBeenCalledTimes(1)
    expect(create.mock.calls[1]).toEqual(create.mock.calls[0])
    expect(create.mock.calls[1]![0]).toBe(create.mock.calls[0]![0])
    expect(create.mock.calls[1]![2]).toBe(create.mock.calls[0]![2])
    expect(create.mock.calls[1]![0].title).toBe('原始标题')
    expect(editor.uncertainCreate.value).toBe(false)
  })

  it.each<[string, () => Error]>([
    ['网络失败', () => new TypeError('network')],
    ['服务端错误', () => problem('INTERNAL_ERROR', 503)],
  ])('更新%s后不因重新加载/换目标/草稿变化丢失原重试请求', async (_, failure) => {
    const source = guardContext()
    const capture = vi.fn(() => source)
    const update = vi
      .fn<ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch, GuardContext>['update']>()
      .mockRejectedValueOnce(failure())
      .mockResolvedValue(writeOutcome<Note>(receipt(note(), [], true), () => true))
    const { spec, calls } = makeSpec({ captureIntentContext: capture, update })
    const editor = useItemEditor(spec)
    await editor.open(note())
    editor.draft.notes = '原始备注'
    await editor.save()
    expect(capture).toHaveBeenCalledWith({
      kind: 'update',
      id: 'n1',
      version: '1',
      patch: { notes: '原始备注' },
    })
    source.guards[0]!.revision = 'changed'
    editor.draft.notes = '之后的修改'
    await editor.load()
    await editor.loadLatest()
    await editor.open(note({ id: 'other' }))
    editor.latest.value = note({ version: '8' })
    editor.adoptLatest()
    editor.close()
    expect(editor.opened.value).toBe(true)
    expect(calls.get).toBe(1)
    expect(editor.baseline.value?.id).toBe('n1')
    expect(editor.baseline.value?.version).toBe('1')
    await editor.save(true)
    expect(capture).toHaveBeenCalledTimes(1)
    expect(update.mock.calls[1]).toEqual(update.mock.calls[0])
    expect(update.mock.calls[1]![2]).toBe(update.mock.calls[0]![2])
    expect(update.mock.calls[1]![4]).toBe(update.mock.calls[0]![4])
    expect(update.mock.calls[1]![2]).toEqual({ notes: '原始备注' })
  })

  it.each([
    ['COLLECTION_BASE_REQUIRED', 428],
    ['COLLECTION_CONFLICT', 412],
  ] as const)('%s 不自动读取新基线或重发，保留草稿及原实体版本', async (code, status) => {
    const source = guardContext()
    const capture = vi.fn(() => source)
    const update = vi.fn(async () => {
      throw problem(code, status)
    })
    const { spec, calls } = makeSpec({ captureIntentContext: capture, update })
    const editor = useItemEditor(spec)
    await editor.open(note())
    editor.draft.notes = '草稿'
    await editor.save()
    expect(update).toHaveBeenCalledTimes(1)
    expect(capture).toHaveBeenCalledTimes(1)
    expect(calls.get).toBe(1)
    expect(editor.latest.value).toBeNull()
    expect(editor.conflict.value).toBe(false)
    expect(editor.baseline.value?.version).toBe('1')
    expect(editor.draft.notes).toBe('草稿')
    expect(editor.error.value).toContain('集合')
  })

  it('对最新版本重提后响应丢失，普通重试仍重放该版本和上下文', async () => {
    const capture = vi.fn(() => guardContext())
    const update = vi
      .fn<ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch, GuardContext>['update']>()
      .mockRejectedValueOnce(problem('VERSION_CONFLICT', 412))
      .mockRejectedValueOnce(new TypeError('network'))
      .mockResolvedValue(writeOutcome<Note>(receipt(note(), [], true), () => true))
    let reads = 0
    const { spec } = makeSpec({
      captureIntentContext: capture,
      update,
      get: async () => note({ version: ++reads === 1 ? '1' : '4' }),
    })
    const editor = useItemEditor(spec)
    await editor.open(note())
    editor.draft.notes = '我的备注'
    await editor.save()
    expect(editor.conflict.value).toBe(true)
    await editor.save(true)
    expect(update.mock.calls[1]![1]).toBe('4')
    expect(await editor.save()).not.toBeNull()
    expect(update.mock.calls[2]).toEqual(update.mock.calls[1])
    expect(capture).toHaveBeenCalledTimes(2)
  })

  it.each([
    ['UNAUTHORIZED', 401],
    ['FORBIDDEN', 403],
    ['COLLECTION_CONFLICT', 412],
    ['COLLECTION_BASE_REQUIRED', 428],
    ['VERSION_CONFLICT', 412],
  ] as const)('未知更新后%s仍固定原请求，不能换基线或关闭', async (code, status) => {
    const source = guardContext()
    const capture = vi.fn(() => source)
    const update = vi
      .fn<ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch, GuardContext>['update']>()
      .mockRejectedValueOnce(new TypeError('network'))
      .mockRejectedValueOnce(problem(code, status))
      .mockResolvedValue(writeOutcome<Note>(receipt(note()), () => true))
    const { spec, calls } = makeSpec({ captureIntentContext: capture, update })
    const editor = useItemEditor(spec)
    await editor.open(note())
    editor.draft.notes = '我的备注'
    await editor.save()
    await editor.save()
    expect(capture).toHaveBeenCalledTimes(1)
    source.guards[0]!.revision = 'confirmed-new'
    editor.draft.notes = '不应重发的新草稿'
    await editor.load()
    await editor.loadLatest()
    await editor.open(note({ id: 'other' }))
    editor.latest.value = note({ version: '8' })
    editor.adoptLatest()
    editor.close()
    expect(editor.opened.value).toBe(true)
    expect(editor.baseline.value?.version).toBe('1')
    expect(calls.get).toBe(1)
    expect(editor.conflict.value).toBe(false)
    expect(editor.error.value).toContain('尚未确认')
    expect(await editor.save(true)).not.toBeNull()
    expect(capture).toHaveBeenCalledTimes(1)
    for (const call of update.mock.calls.slice(1)) {
      expect(call).toEqual(update.mock.calls[0])
      expect(call[2]).toBe(update.mock.calls[0]![2])
      expect(call[4]).toBe(update.mock.calls[0]![4])
    }
    expect(editor.opened.value).toBe(false)
    await editor.open(note({ id: 'next' }))
    expect(editor.baseline.value?.id).toBe('next')
  })

  it.each([
    ['UNAUTHORIZED', 401],
    ['FORBIDDEN', 403],
    ['COLLECTION_CONFLICT', 412],
    ['COLLECTION_BASE_REQUIRED', 428],
    ['VERSION_CONFLICT', 412],
  ] as const)('未知创建后%s仍固定id/body/context直到成功', async (code, status) => {
    for (const contextual of [false, true]) {
      const source = guardContext()
      const capture = vi.fn(() => source)
      const create = vi
        .fn<ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch, GuardContext>['create']>()
        .mockRejectedValueOnce(new TypeError('network'))
        .mockRejectedValueOnce(problem(code, status))
        .mockResolvedValue(writeOutcome<Note>(receipt(note(), [], true), () => true))
      const { spec, calls } = makeSpec<GuardContext>({
        create,
        ...(contextual ? { captureIntentContext: capture } : {}),
      })
      const editor = useItemEditor(spec)
      await editor.open()
      editor.draft.title = '原草稿'
      await editor.save()
      await editor.save()
      expect(editor.uncertainCreate.value).toBe(true)
      expect(editor.error.value).toContain('尚未确认')
      source.guards[0]!.revision = 'new'
      editor.draft.title = '不应重发'
      editor.close()
      await editor.open(note())
      await editor.loadLatest()
      expect(editor.opened.value).toBe(true)
      expect(editor.isEditing.value).toBe(false)
      expect(calls.get).toBe(0)
      expect(await editor.save()).not.toBeNull()
      for (const call of create.mock.calls.slice(1)) {
        expect(call).toEqual(create.mock.calls[0])
        expect(call[0]).toBe(create.mock.calls[0]![0])
        expect(call[2]).toBe(create.mock.calls[0]![2])
      }
      expect(capture).toHaveBeenCalledTimes(contextual ? 1 : 0)
      expect(editor.uncertainCreate.value).toBe(false)
      expect(editor.opened.value).toBe(false)
    }
  })

  it('初次创建明确拒绝仍允许修改，重新捕获新意图', async () => {
    const capture = vi.fn(() => guardContext())
    const create = vi
      .fn<ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch, GuardContext>['create']>()
      .mockRejectedValueOnce(problem('FORBIDDEN', 403))
      .mockResolvedValue(writeOutcome<Note>(receipt(note()), () => true))
    const { spec } = makeSpec({ captureIntentContext: capture, create })
    const editor = useItemEditor(spec)
    await editor.open()
    editor.draft.title = '旧输入'
    await editor.save()
    expect(editor.uncertainCreate.value).toBe(false)
    editor.draft.title = '修正输入'
    expect(await editor.save()).not.toBeNull()
    expect(capture).toHaveBeenCalledTimes(2)
    expect(create.mock.calls[1]![0].title).toBe('修正输入')
    expect(create.mock.calls[1]![1]).not.toBe(create.mock.calls[0]![1])
  })

  it.each(['create', 'update'])('保留%s显式close(true)丢弃的兼容语义', async (kind) => {
    const { spec } = makeSpec({
      captureIntentContext: () => guardContext(),
      create: async () => {
        throw new TypeError('offline')
      },
      update: async () => {
        throw new TypeError('offline')
      },
    })
    const editor = useItemEditor(spec)
    await editor.open(kind === 'update' ? note() : undefined)
    editor.draft.title = '提交内容'
    await editor.save()
    editor.close()
    expect(editor.opened.value).toBe(true)
    editor.close(true)
    expect(editor.opened.value).toBe(false)
    expect(editor.uncertainCreate.value).toBe(false)
    await editor.open(note({ id: 'next' }))
    expect(editor.baseline.value?.id).toBe('next')
  })

  it('捕获异常发生在发送和分配意图之前，保留字段错误与草稿', async () => {
    const inputs = observeIntent()
    const { spec, calls } = makeSpec<GuardContext>({
      captureIntentContext: () => {
        throw new DraftError({ notes: '请核对成员基线' })
      },
    })
    const editor = useItemEditor(spec)
    await editor.open()
    editor.draft.title = '草稿'
    expect(await editor.save()).toBeNull()
    expect(editor.errors.value.notes).toBe('请核对成员基线')
    expect(editor.draft.title).toBe('草稿')
    expect(calls.create).toHaveLength(0)
    expect(inputs).toHaveLength(0)
    expect(editor.saving.value).toBe(false)
    expect(editor.uncertainCreate.value).toBe(false)
  })

  it.each<[string, () => unknown]>([
    ['undefined', () => undefined],
    ['嵌套undefined', () => ({ guards: [undefined] })],
    ['NaN', () => ({ revision: NaN })],
    ['Infinity', () => Infinity],
    ['bigint', () => ({ value: 1n })],
    ['function', () => ({ value: () => 'revision' })],
    ['symbol值', () => ({ value: Symbol('revision') })],
    ['symbol键', () => ({ [Symbol('revision')]: 'old' })],
    ['隐藏字段', () => Object.defineProperty({}, 'revision', { value: 'old' })],
    ['Date', () => ({ when: new Date() })],
    ['Map', () => new Map([['revision', 'old']])],
    ['Promise', () => Promise.resolve({ revision: 'old' })],
    [
      'class',
      () =>
        new (class Context {
          revision = 'old'
        })(),
    ],
    ['Ref', () => ({ revision: ref('old') })],
    ['响应式对象', () => reactive({ revision: 'old' })],
    ['稀疏数组', () => new Array(1)],
    ['超长稀疏数组', () => new Array(0xffffffff)],
    ['数组附加字段', () => Object.assign(['old'], { revision: 'old' })],
    [
      '循环',
      () => {
        const value: { self?: unknown } = {}
        value.self = value
        return value
      },
    ],
  ])('拒绝非JSON上下文：%s', async (_, value) => {
    const { spec, calls } = makeSpec<unknown>({ captureIntentContext: value })
    const editor = useItemEditor(spec)
    await editor.open()
    editor.draft.title = '保留'
    expect(await editor.save()).toBeNull()
    expect(calls.create).toHaveLength(0)
    expect(editor.error.value).toContain('提交上下文')
    expect(editor.draft.title).toBe('保留')
  })

  it.each(['revision', '__v_isRef', 'toJSON'])('拒绝访问器且不执行%s getter', async (key) => {
    const getter = vi.fn(() => 'old')
    const source = Object.defineProperty({}, key, { get: getter, enumerable: true })
    const { spec, calls } = makeSpec<unknown>({ captureIntentContext: () => source })
    const editor = useItemEditor(spec)
    await editor.open()
    editor.draft.title = '保留'
    await editor.save()
    expect(getter).not.toHaveBeenCalled()
    expect(calls.create).toHaveLength(0)
  })

  it('接收JSON基本值及重复引用，保留特殊自有键而不改变原型', async () => {
    const shared = { revision: 'old' }
    const source = JSON.parse('{"__proto__":{"revision":"special"}}') as Record<string, unknown>
    source.values = [null, true, false, 0, 'text', shared, shared]
    const create = vi.fn<ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch, unknown>['create']>(
      async () => writeOutcome<Note>(receipt(note()), () => true),
    )
    const { spec } = makeSpec<unknown>({ captureIntentContext: () => source, create })
    const editor = useItemEditor(spec)
    await editor.open()
    editor.draft.title = '标题'
    expect(await editor.save()).not.toBeNull()
    const context = create.mock.calls[0]![2]
    expect(context).toEqual(source)
    expect(Object.getPrototypeOf(context)).toBe(Object.prototype)
    expect(Object.hasOwn(context as object, '__proto__')).toBe(true)
    expect(JSON.stringify(context)).toBe(JSON.stringify(source))
  })
})
