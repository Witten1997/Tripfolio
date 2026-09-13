import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'

import { ApiError } from '@/shared/api/auth'
import { writeOutcome } from '@/shared/api/writes'
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

function makeSpec(overrides: Partial<ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch>> = {}) {
  const calls = { create: [] as unknown[], update: [] as unknown[], get: 0 }
  const spec: ItemEditorSpec<Note, NoteDraft, NoteValues, NotePatch> = {
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
