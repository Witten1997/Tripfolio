import {
  computed,
  reactive,
  ref,
  shallowRef,
  type ComputedRef,
  type Ref,
  type ShallowRef,
} from 'vue'

import { ApiError } from '@/shared/api/auth'
import { actionError, createWriteIntent, fieldErrors, type WriteOutcome } from '@/shared/api/writes'
import { randomId } from '@/shared/randomId'
import { DraftError } from '@/shared/travel/tripDraft'

/** 一种旅行内容资源的编辑规格：草稿与校验、差异、读写；由各模块提供，编辑流程共用。 */
export interface ItemEditorSpec<T extends { id: string; version: string }, D extends object, C, P> {
  emptyDraft: () => D
  draftFrom: (item: T) => D
  /** 校验并规范化草稿；失败抛 DraftError。返回值用于创建正文或与基线做差异。 */
  validate: (draft: D) => C
  /** 只含相对基线的主动修改；空对象表示没有变化。 */
  diff: (values: C, baseline: T) => P
  get: (id: string) => Promise<T>
  create: (body: C & { id: string }, operationId: string) => Promise<WriteOutcome<T>>
  update: (id: string, version: string, patch: P, operationId: string) => Promise<WriteOutcome<T>>
}

export interface ItemEditor<T extends { id: string; version: string }, D extends object> {
  opened: Ref<boolean>
  loading: Ref<boolean>
  saving: Ref<boolean>
  uncertainCreate: Ref<boolean>
  loadingLatest: Ref<boolean>
  baseline: ShallowRef<T | null>
  latest: ShallowRef<T | null>
  conflict: Ref<boolean>
  error: Ref<string | null>
  latestError: Ref<string | null>
  errors: Ref<Record<string, string>>
  draft: D
  dirty: ComputedRef<boolean>
  isEditing: ComputedRef<boolean>
  open: (item?: T, presets?: Partial<D>) => Promise<void>
  load: () => Promise<void>
  close: (discardUncertain?: boolean) => void
  loadLatest: () => Promise<void>
  adoptLatest: () => void
  save: (againstLatest?: boolean) => Promise<WriteOutcome<T> | null>
}

/**
 * 与 useTripEditor 相同的编辑流程：基线/最新版本、412 冲突对比与对最新版本重提、
 * 幂等意图、创建结果不确定时原样重放。行程、行李、待办共用。
 */
export function useItemEditor<
  T extends { id: string; version: string },
  D extends object,
  C,
  P extends object,
>(spec: ItemEditorSpec<T, D, C, P>): ItemEditor<T, D> {
  const opened = ref(false)
  const loading = ref(false)
  const saving = ref(false)
  const uncertainCreate = ref(false)
  const loadingLatest = ref(false)
  // shallowRef 的重载在泛型 T 上推断不稳定，显式标注
  const baseline = shallowRef(null) as ShallowRef<T | null>
  const latest = shallowRef(null) as ShallowRef<T | null>
  const conflict = ref(false)
  const error = ref<string | null>(null)
  const latestError = ref<string | null>(null)
  const errors = ref<Record<string, string>>({})
  const draft = reactive(spec.emptyDraft()) as D
  const editId = ref<string | null>(null)
  const initial = ref(JSON.stringify(draft))
  let createId = ''
  let pendingCreate: { body: C & { id: string }; operationId: string } | null = null
  let generation = 0
  const intent = createWriteIntent()

  const dirty = computed(() => JSON.stringify(draft) !== initial.value)
  const isEditing = computed(() => editId.value !== null)

  function fill(item: T) {
    baseline.value = item
    Object.assign(draft, spec.draftFrom(item))
    initial.value = JSON.stringify(draft)
  }

  async function load() {
    if (!editId.value || saving.value || uncertainCreate.value) return
    const request = ++generation
    loading.value = true
    error.value = null
    try {
      const item = await spec.get(editId.value)
      if (request === generation && opened.value) fill(item)
    } catch (cause) {
      if (request === generation) error.value = actionError(cause, '无法加载记录，请重试')
    } finally {
      if (request === generation) loading.value = false
    }
  }

  async function open(item?: T, presets: Partial<D> = {}) {
    if (saving.value || uncertainCreate.value) return
    generation++
    opened.value = true
    loading.value = false
    loadingLatest.value = false
    editId.value = item?.id ?? null
    baseline.value = latest.value = null
    conflict.value = false
    error.value = latestError.value = null
    errors.value = {}
    intent.reset()
    pendingCreate = null
    createId = randomId()
    Object.assign(draft, spec.emptyDraft(), presets)
    initial.value = JSON.stringify(draft)
    if (item) await load()
  }

  function close(discardUncertain = false) {
    if (saving.value || (uncertainCreate.value && !discardUncertain)) return
    generation++
    opened.value = false
    pendingCreate = null
    uncertainCreate.value = false
  }

  async function loadLatest() {
    if (!baseline.value || loadingLatest.value) return
    const request = generation
    loadingLatest.value = true
    latestError.value = null
    try {
      const item = await spec.get(baseline.value.id)
      if (request === generation && opened.value) latest.value = item
    } catch (cause) {
      if (request === generation) latestError.value = actionError(cause, '无法加载最新版本，请重试')
    } finally {
      if (request === generation) loadingLatest.value = false
    }
  }

  function adoptLatest() {
    if (!latest.value || saving.value) return
    fill(latest.value)
    latest.value = null
    conflict.value = false
    error.value = latestError.value = null
    errors.value = {}
    intent.reset()
  }

  async function save(againstLatest = false): Promise<WriteOutcome<T> | null> {
    if (!opened.value || saving.value || loading.value || (conflict.value && !againstLatest))
      return null
    if (isEditing.value && !baseline.value) return null
    if (againstLatest && !latest.value) return null
    error.value = null
    errors.value = {}
    let patch: P | null = null
    let createRequest = pendingCreate
    if (!createRequest) {
      try {
        const values = spec.validate(draft)
        if (baseline.value) {
          patch = spec.diff(values, baseline.value)
          if (!Object.keys(patch).length) {
            error.value = '没有需要保存的修改'
            return null
          }
        } else {
          const body = { ...values, id: createId }
          createRequest = { body, operationId: intent.key({ id: createId, body: values }) }
        }
      } catch (cause) {
        if (cause instanceof DraftError) errors.value = cause.fields
        error.value = cause instanceof Error ? cause.message : '请检查填写内容'
        return null
      }
    }
    saving.value = true
    try {
      const base = againstLatest ? latest.value : baseline.value
      const outcome = base
        ? await spec.update(
            base.id,
            base.version,
            patch!,
            intent.key({ id: base.id, version: base.version, patch }),
          )
        : await spec.create(createRequest!.body, createRequest!.operationId)
      intent.reset()
      pendingCreate = null
      uncertainCreate.value = false
      opened.value = false
      return outcome
    } catch (cause) {
      error.value = actionError(
        cause,
        '网络连接中断，结果尚未确认。保留当前内容重试可避免重复提交。',
      )
      errors.value = fieldErrors(cause)
      if (createRequest) {
        uncertainCreate.value =
          !(cause instanceof ApiError) || (cause.problem?.status ?? 500) >= 500
        pendingCreate = uncertainCreate.value ? createRequest : null
        if (uncertainCreate.value) {
          error.value = '创建结果尚未确认，记录可能已经保存。请原样重试，确认后再编辑内容。'
        }
      }
      if (cause instanceof ApiError && cause.code === 'VERSION_CONFLICT') {
        conflict.value = true
        latest.value = null
        await loadLatest()
      }
      return null
    } finally {
      saving.value = false
    }
  }

  return {
    opened,
    loading,
    saving,
    uncertainCreate,
    loadingLatest,
    baseline,
    latest,
    conflict,
    error,
    latestError,
    errors,
    draft,
    dirty,
    isEditing,
    open,
    load,
    close,
    loadLatest,
    adoptLatest,
    save,
  }
}
