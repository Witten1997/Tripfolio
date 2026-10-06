import { computed, getCurrentScope, onScopeDispose, ref, shallowRef } from 'vue'
import {
  createPhoto,
  getPhoto,
  updatePhoto,
  type Photo,
  type PhotoCreate,
  type PhotoPatch,
} from '@/shared/api/photos'
import { ApiError } from '@/shared/api/problem'
import type { CompleteCollection } from '@/shared/api/collectionBaselines'
import { loadCollectionBaselineHttp } from '@/shared/api/collectionBaselinesHttp'
import { CollectionBaseline, type CollectionGuard } from '@/shared/api/collectionGuards'
import { actionError } from '@/shared/api/writes'
import {
  changedContent,
  emptyPhotoDraft,
  photoDraftFrom,
  validatePhoto,
  type PhotoDraft,
} from './contentDrafts'
import { validDate } from './itineraryDraft'
import { useItemEditor } from './useItemEditor'

type Day = CompleteCollection<'photo_day'>
type Values = Omit<PhotoCreate, 'id'>
type Context = { guards: readonly CollectionGuard[]; orderMode: 'append' | 'explicit' }
type Candidate = { photo: Photo; days: readonly Day[] }
const sensitive = (patch: PhotoPatch) =>
  patch.recorded_on !== undefined ||
  patch.taken_at_local !== undefined ||
  patch.sort_order !== undefined

export function usePhotoEditor(tripId: string, today: () => string) {
  const orderMode = ref<'append' | 'explicit'>('append')
  const confirmed = shallowRef<readonly Day[]>([])
  const candidate = shallowRef<Candidate | null>(null)
  const preparing = ref(false)
  const checking = ref(false)
  const dateError = ref<string | null>(null)
  const rejected = ref(false)
  const uncertainUpdate = ref(false)
  let generation = 0
  let controller: AbortController | null = null

  function stopReading() {
    generation++
    controller?.abort()
    controller = null
    preparing.value = checking.value = false
    candidate.value = null
  }
  function beginReading() {
    stopReading()
    controller = new AbortController()
    return { turn: generation, signal: controller.signal }
  }
  function markFailure(cause: unknown) {
    uncertainUpdate.value ||= !(cause instanceof ApiError) || (cause.problem?.status ?? 500) >= 500
    if (
      cause instanceof ApiError &&
      ['COLLECTION_BASE_REQUIRED', 'COLLECTION_CONFLICT', 'VERSION_CONFLICT'].includes(
        cause.code ?? '',
      )
    ) {
      rejected.value = true
    }
  }
  const core = useItemEditor<Photo, PhotoDraft, Values, PhotoPatch, Context>({
    emptyDraft: () => emptyPhotoDraft(today()),
    draftFrom: photoDraftFrom,
    validate(draft) {
      const values: Values = validatePhoto(draft)
      if (!core.isEditing.value && orderMode.value === 'append') delete values.sort_order
      return values
    },
    diff(values, baseline) {
      const patch = changedContent(values, baseline)
      // 分组日期是用户确认的目标；即使与实体同值，也不能在改时间时被差异过滤掉。
      if (patch.taken_at_local !== undefined) patch.recorded_on = values.recorded_on
      return patch
    },
    get: (id) => getPhoto(tripId, id),
    captureIntentContext(submission) {
      const guards: CollectionGuard[] = []
      if (submission.kind === 'update' && sensitive(submission.patch)) {
        const photo = core.baseline.value!
        const day = finalDay(submission.patch, photo)
        const selected = requireDays(confirmed.value, photo, day)
        for (const value of selected)
          guards.push(...value.baseline.guards.map((guard) => ({ ...guard })))
      }
      return { guards, orderMode: orderMode.value }
    },
    create: (body, key) => createPhoto(tripId, body, key),
    async update(id, version, patch, key, context) {
      try {
        const baseline = context?.guards.length
          ? new CollectionBaseline({ complete: true, guards: context.guards })
          : undefined
        const outcome = await updatePhoto(tripId, id, version, patch, key, baseline)
        uncertainUpdate.value = false
        return outcome
      } catch (cause) {
        markFailure(cause)
        throw cause
      }
    },
  })
  const uncertain = computed(() => core.uncertainCreate.value || uncertainUpdate.value)
  const locked = computed(() => core.saving.value || uncertain.value)

  function finalDay(patch: PhotoPatch, photo: Photo) {
    return patch.recorded_on ?? patch.taken_at_local?.slice(0, 10) ?? photo.recorded_on
  }
  function requireDays(days: readonly Day[], photo: Photo, target: string): Day[] {
    const dates = [...new Set([photo.recorded_on, target])]
    const selected = dates.map((date) =>
      days.find((value) => value.scope_id === `${tripId}/${date}`),
    )
    if (selected.some((value) => !value || Date.parse(value.expires_at) <= Date.now()))
      throw new Error('日期资料尚未完整准备或已过期，请保留输入并核对最新照片。')
    const complete = selected as Day[]
    const old = complete[0]!.items.find((item) => item.id === photo.id)
    if (!old || old.version !== photo.version)
      throw new Error('照片已发生变化，请保留输入并核对最新照片。')
    assertConsistent(complete)
    return complete
  }
  function assertConsistent(days: readonly Day[]) {
    const ids = new Set<string>()
    for (const value of days) {
      if (value.sync_epoch !== days[0]!.sync_epoch)
        throw new Error('不同日期的资料不属于同一同步版本，请核对最新照片。')
      for (const item of value.items) {
        if (ids.has(item.id)) throw new Error('照片日期资料已变化，请核对最新照片。')
        ids.add(item.id)
      }
    }
  }
  const sensitiveReady = computed(() => {
    if (!core.isEditing.value) return true
    if (!core.baseline.value || rejected.value) return false
    try {
      requireDays(confirmed.value, core.baseline.value, core.draft.recorded_on)
      return true
    } catch {
      return false
    }
  })

  async function prepareDay(target: string): Promise<boolean> {
    if (
      locked.value ||
      checking.value ||
      rejected.value ||
      !core.opened.value ||
      !validDate(target)
    )
      return false
    if (!core.isEditing.value) return true
    const photo = core.baseline.value
    if (!photo) return false
    const { turn, signal } = beginReading()
    preparing.value = true
    dateError.value = null
    try {
      const days = [...confirmed.value]
      for (const day of new Set([photo.recorded_on, target])) {
        if (!days.some((value) => value.scope_id === `${tripId}/${day}`)) {
          days.push(
            await loadCollectionBaselineHttp(
              { kind: 'photo_day', scope_id: `${tripId}/${day}` },
              { limit: 100, signal },
            ),
          )
        }
      }
      requireDays(days, photo, target)
      if (turn !== generation || !core.opened.value || locked.value) return false
      confirmed.value = days
      return true
    } catch (cause) {
      if (turn === generation)
        dateError.value = actionError(
          cause,
          cause instanceof Error ? cause.message : '无法准备日期资料',
        )
      return false
    } finally {
      if (turn === generation) preparing.value = false
    }
  }

  async function chooseDay(value: unknown) {
    if (typeof value !== 'string' || !(await prepareDay(value))) return
    core.draft.recorded_on = value
  }
  async function setTaken(value: unknown) {
    const time = typeof value === 'string' && value ? value : null
    const day = time?.slice(0, 10) ?? core.draft.recorded_on
    if (!(await prepareDay(day))) return
    core.draft.taken_at_local = time
    core.draft.recorded_on = day
  }
  async function checkLatest() {
    if (locked.value || !core.baseline.value || !core.opened.value) return
    const id = core.baseline.value.id
    const { turn, signal } = beginReading()
    checking.value = true
    dateError.value = null
    candidate.value = null
    try {
      const photo = await getPhoto(tripId, id)
      if (turn !== generation || !core.opened.value) return
      const days: Day[] = []
      for (const day of new Set([
        photo.recorded_on,
        core.baseline.value!.recorded_on,
        core.draft.recorded_on,
      ])) {
        days.push(
          await loadCollectionBaselineHttp(
            { kind: 'photo_day', scope_id: `${tripId}/${day}` },
            { limit: 100, signal },
          ),
        )
      }
      requireDays(days, photo, core.draft.recorded_on)
      assertConsistent(days)
      if (turn === generation && core.opened.value && !locked.value)
        candidate.value = { photo, days }
    } catch (cause) {
      if (turn === generation)
        dateError.value = actionError(
          cause,
          cause instanceof Error ? cause.message : '无法核对最新照片',
        )
    } finally {
      if (turn === generation) checking.value = false
    }
  }
  function adoptCandidate() {
    const latest = candidate.value
    if (!latest || !core.opened.value || locked.value || checking.value || preparing.value) return
    try {
      requireDays(latest.days, latest.photo, latest.photo.recorded_on)
    } catch (cause) {
      dateError.value = (cause as Error).message
      return
    }
    core.latest.value = latest.photo
    core.adoptLatest()
    confirmed.value = latest.days
    candidate.value = null
    rejected.value = false
    dateError.value = null
  }
  async function open(photo?: Photo) {
    if (locked.value || (core.opened.value && core.dirty.value)) return
    stopReading()
    confirmed.value = []
    candidate.value = null
    rejected.value = false
    dateError.value = null
    orderMode.value = photo ? 'explicit' : 'append'
    await core.open(photo)
  }
  function close() {
    if (locked.value) return
    stopReading()
    core.close()
  }
  async function save() {
    if (preparing.value || checking.value || (rejected.value && !uncertain.value)) return null
    // 不开放共享编辑器的 save(true)，核对后仅显式采用完整候选。
    const outcome = await core.save()
    if (outcome) stopReading()
    return outcome
  }
  async function load() {
    if (!locked.value && !core.baseline.value) await core.load()
  }
  if (getCurrentScope()) onScopeDispose(stopReading)
  return {
    ...core,
    open,
    close,
    load,
    save,
    // 专属入口不暴露可绕过完整集合核对的共享方法。
    loadLatest: checkLatest,
    adoptLatest: adoptCandidate,
    orderMode,
    confirmed,
    candidate,
    preparing,
    checking,
    dateError,
    rejected,
    uncertain,
    uncertainUpdate,
    locked,
    sensitiveReady,
    prepareSensitive: () => prepareDay(core.draft.recorded_on),
    chooseDay,
    setTaken,
    checkLatest,
    adoptCandidate,
    cancelCandidate: () => {
      if (!locked.value) stopReading()
    },
  }
}
