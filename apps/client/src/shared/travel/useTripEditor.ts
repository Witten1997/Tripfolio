import { computed, reactive, ref, shallowRef } from 'vue'

import { ApiError } from '@/shared/api/auth'
import {
  createTrip,
  getTrip,
  updateTrip,
  type Trip,
  type TripCreate,
  type TripPatch,
} from '@/shared/api/trips'
import { actionError, createWriteIntent, fieldErrors } from '@/shared/api/writes'
import { useMetadataStore } from '@/shared/stores/metadata'
import { useSessionStore } from '@/shared/stores/session'
import {
  changedTripFields,
  DraftError,
  draftFromTrip,
  emptyTripDraft,
  validateTripDraft,
} from '@/shared/travel/tripDraft'

export function useTripEditor() {
  const metadata = useMetadataStore()
  const session = useSessionStore()
  const opened = ref(false)
  const loading = ref(false)
  const saving = ref(false)
  const uncertainCreate = ref(false)
  const loadingLatest = ref(false)
  const baseline = shallowRef<Trip | null>(null)
  const latest = shallowRef<Trip | null>(null)
  const conflict = ref(false)
  const error = ref<string | null>(null)
  const latestError = ref<string | null>(null)
  const errors = ref<Record<string, string>>({})
  const draft = reactive(emptyTripDraft())
  const editId = ref<string | null>(null)
  const initial = ref(JSON.stringify(draft))
  let createId = ''
  // 创建可能已提交：确认结果前保留原正文与操作号，不从可变草稿重新生成请求。
  let pendingCreate: { body: TripCreate; operationId: string } | null = null
  let generation = 0
  const intent = createWriteIntent()

  const dirty = computed(() => JSON.stringify(draft) !== initial.value)
  const isEditing = computed(() => editId.value !== null)

  function fill(trip: Trip) {
    baseline.value = trip
    Object.assign(draft, draftFromTrip(trip))
    initial.value = JSON.stringify(draft)
  }

  async function load() {
    if (!editId.value || saving.value || uncertainCreate.value) return
    const request = ++generation
    loading.value = true
    error.value = null
    try {
      const trip = await getTrip(editId.value)
      if (request === generation && opened.value) fill(trip)
    } catch (cause) {
      if (request === generation) error.value = actionError(cause, '无法加载旅行，请重试')
    } finally {
      if (request === generation) loading.value = false
    }
  }

  async function open(trip?: Trip) {
    if (saving.value || uncertainCreate.value) return
    generation++
    opened.value = true
    loading.value = false
    loadingLatest.value = false
    editId.value = trip?.id ?? null
    baseline.value = latest.value = null
    conflict.value = false
    error.value = latestError.value = null
    errors.value = {}
    intent.reset()
    pendingCreate = null
    createId = crypto.randomUUID()
    Object.assign(
      draft,
      emptyTripDraft(
        session.account?.default_timezone ?? 'Asia/Shanghai',
        metadata.metadata?.default_currency_code ?? 'CNY',
      ),
    )
    initial.value = JSON.stringify(draft)
    if (trip) await load()
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
      const trip = await getTrip(baseline.value.id)
      if (request === generation && opened.value) latest.value = trip
    } catch (cause) {
      if (request === generation) latestError.value = actionError(cause, '无法加载最新版本，请重试')
    } finally {
      if (request === generation) loadingLatest.value = false
    }
  }

  /** 只有用户明确放弃本地输入后调用。 */
  function adoptLatest() {
    if (!latest.value || saving.value) return
    fill(latest.value)
    latest.value = null
    conflict.value = false
    error.value = latestError.value = null
    errors.value = {}
    intent.reset()
  }

  async function save(againstLatest = false) {
    if (!opened.value || saving.value || loading.value || (conflict.value && !againstLatest))
      return null
    if (isEditing.value && !baseline.value) return null
    if (againstLatest && !latest.value) return null
    error.value = null
    errors.value = {}
    let patch: TripPatch = {}
    let createRequest = pendingCreate
    if (!createRequest) {
      try {
        const values = validateTripDraft(
          draft,
          metadata.status === 'ready' ? metadata.metadata : null,
        )
        if (baseline.value) {
          patch = changedTripFields(values, baseline.value)
          if (!Object.keys(patch).length) {
            error.value = '没有需要保存的修改'
            return null
          }
        } else {
          createRequest = {
            body: { ...values, id: createId },
            operationId: intent.key({ id: createId, patch: values }),
          }
        }
      } catch (cause) {
        if (cause instanceof DraftError) errors.value = cause.fields
        error.value = cause instanceof Error ? cause.message : '请检查填写内容'
        return null
      }
    }
    saving.value = true
    try {
      // 冲突重提仍仅包含相对原基线的主动修改，避免覆盖最新版本中的无关字段。
      const base = againstLatest ? latest.value : baseline.value
      const outcome = base
        ? await updateTrip(
            base.id,
            base.version,
            patch,
            intent.key({ id: base.id, version: base.version, patch }),
          )
        : await createTrip(createRequest!.body, createRequest!.operationId)
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
          error.value = '创建结果尚未确认，旅行可能已经保存。请原样重试，确认后再编辑内容。'
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
