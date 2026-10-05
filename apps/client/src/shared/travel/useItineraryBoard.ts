import { computed, getCurrentScope, onScopeDispose, ref, shallowRef, watch } from 'vue'

import { ApiError } from '@/shared/api/auth'
import { loadCollectionBaselineHttp } from '@/shared/api/collectionBaselinesHttp'
import type { CompleteCollection } from '@/shared/api/collectionBaselines'
import { CollectionBaseline } from '@/shared/api/collectionGuards'
import {
  listAllItineraryItems,
  reorderItineraryItems,
  type ItineraryItem,
  type ItineraryReorder,
} from '@/shared/api/itinerary'
import {
  actionError,
  createWriteIntent,
  writeWarnings,
  type WriteResult,
} from '@/shared/api/writes'
import { groupByDay, type TripDay } from '@/shared/travel/tripDays'

export interface TripSpan {
  start: string
  end: string
}
interface BoardSnapshot {
  readonly span: string
  readonly collections: Readonly<Record<string, CompleteCollection<'itinerary_day'>>>
  readonly items: readonly ItineraryItem[]
}
interface PendingOrder {
  readonly body: ItineraryReorder
  readonly baseline: CollectionBaseline
  readonly operationId: string
}

/** 展示日期发现与可编辑的完整集合分离；重排失败保留原请求和排序草稿。 */
export function useItineraryBoard(tripId: string, span: () => TripSpan) {
  const confirmed = shallowRef<BoardSnapshot | null>(null)
  const draft = shallowRef<readonly ItineraryItem[] | null>(null)
  const latest = shallowRef<BoardSnapshot | null>(null)
  const pending = shallowRef<PendingOrder | null>(null)
  const loading = ref(false)
  const loadingLatest = ref(false)
  const error = ref<string | null>(null)
  const latestError = ref<string | null>(null)
  const reordering = ref(false)
  const uncertain = ref(false)
  const savedNeedsReload = ref(false)
  const actionFailure = ref<string | null>(null)
  const feedback = ref<string[]>([])
  const intent = createWriteIntent()
  let generation = 0
  let controller: AbortController | undefined
  let disposed = false
  const spanKey = () => `${span().start}/${span().end}`
  const items = computed(() => draft.value ?? confirmed.value?.items ?? [])
  const days = computed<TripDay[]>(() => groupByDay(span().start, span().end, [...items.value]))
  const hasPending = computed(() => pending.value !== null)
  const canReorder = computed(
    () =>
      !disposed &&
      !!confirmed.value &&
      confirmed.value.span === spanKey() &&
      !loading.value &&
      !reordering.value &&
      !pending.value &&
      !savedNeedsReload.value &&
      !error.value,
  )
  const comparison = computed(
    () =>
      pending.value?.body.days.map((day) => ({
        date: day.date,
        original: ordered(confirmed.value?.items ?? [], day.date),
        draft: ordered(draft.value ?? [], day.date),
        latest: latest.value ? ordered(latest.value.items, day.date) : null,
      })) ?? [],
  )

  function ordered(source: readonly ItineraryItem[], date: string): ItineraryItem[] {
    return source
      .filter((item) => item.scheduled_on === date)
      .sort((a, b) => a.sort_order - b.sort_order || a.id.localeCompare(b.id))
  }

  async function readSnapshot(
    signal: AbortSignal,
    captured: TripSpan,
    extraDates: string[] = [],
  ): Promise<BoardSnapshot> {
    if (!captured.start || !captured.end) throw new Error('旅行日期尚未加载。')
    // 普通列表只用于发现日期；每一天的资源和基线均由同一个完整读取提供。
    const discovered = await listAllItineraryItems(tripId, signal)
    const dates = [
      ...new Set([
        ...groupByDay(captured.start, captured.end, discovered).map((day) => day.date),
        ...extraDates,
      ]),
    ].sort()
    const collections: Record<string, CompleteCollection<'itinerary_day'>> = {}
    let next = 0
    await Promise.all(
      Array.from({ length: Math.min(4, dates.length) }, async () => {
        while (next < dates.length) {
          signal.throwIfAborted()
          const date = dates[next++]!
          collections[date] = await loadCollectionBaselineHttp(
            { kind: 'itinerary_day', scope_id: `${tripId}/${date}` },
            { limit: 100, signal },
          )
        }
      }),
    )
    signal.throwIfAborted()
    const all: ItineraryItem[] = []
    const ids = new Set<string>()
    const epoch = collections[dates[0]!]?.sync_epoch
    for (const date of dates) {
      const collection = collections[date]!
      if (!collection.complete || collection.sync_epoch !== epoch)
        throw new Error('日期集合已变化，请重新读取并核对。')
      for (const item of collection.items) {
        if (ids.has(item.id)) throw new Error('日期集合包含重复行程，请重新读取并核对。')
        ids.add(item.id)
        all.push(Object.freeze({ ...item }))
      }
    }
    return Object.freeze({
      span: `${captured.start}/${captured.end}`,
      collections: Object.freeze(collections),
      items: Object.freeze(all),
    })
  }

  function beginRead() {
    controller?.abort()
    controller = new AbortController()
    return { run: ++generation, signal: controller.signal, captured: { ...span() } }
  }
  function current(run: number, captured: TripSpan) {
    return !disposed && run === generation && `${captured.start}/${captured.end}` === spanKey()
  }

  async function reload(): Promise<boolean> {
    if (disposed || pending.value || reordering.value) return false
    const { run, signal, captured } = beginRead()
    loading.value = true
    error.value = null
    try {
      const loaded = await readSnapshot(signal, captured)
      if (!current(run, captured) || pending.value) return false
      confirmed.value = loaded
      draft.value = latest.value = null
      savedNeedsReload.value = false
      actionFailure.value = null
      return true
    } catch (cause) {
      if (current(run, captured)) {
        controller?.abort()
        error.value = actionError(cause, '无法完整加载行程，请重试后再调整排序。')
      }
      return false
    } finally {
      if (run === generation && !disposed) loading.value = false
    }
  }

  async function submitPending(): Promise<boolean> {
    const request = pending.value
    if (!request || reordering.value || disposed) return false
    reordering.value = true
    actionFailure.value = null
    let result: WriteResult
    try {
      result = await reorderItineraryItems(
        tripId,
        request.body,
        request.operationId,
        request.baseline,
      )
    } catch (cause) {
      if (!disposed) {
        uncertain.value ||= !(cause instanceof ApiError) || (cause.problem?.status ?? 500) >= 500
        actionFailure.value = uncertain.value
          ? '排序结果尚未确认。当前顺序已保留，请原样重试，确认后再继续调整。'
          : cause instanceof ApiError &&
              ['COLLECTION_BASE_REQUIRED', 'ORDER_CHANGED'].includes(cause.code ?? '')
            ? '排序所依据的行程资料已变化或不完整。当前顺序已保留，请核对最新安排。'
            : actionError(cause, '排序未保存。当前顺序已保留，请核对最新安排。')
      }
      return false
    } finally {
      if (!disposed) reordering.value = false
    }
    if (disposed) return false
    pending.value = null
    uncertain.value = false
    intent.reset()
    feedback.value = writeWarnings(result)
    savedNeedsReload.value = true
    // 保存已确认；后续读取失败不能再次发送写请求，也不能拿原收据拼接新基线。
    if (!(await reload()))
      actionFailure.value = '排序已保存，但最新行程加载失败。请重新加载后再调整。'
    return true
  }

  async function moveItem(id: string, targetDate: string, index: number): Promise<boolean> {
    if (disposed || !canReorder.value || !Number.isInteger(index)) return false
    const snapshot = confirmed.value!
    const moving = snapshot.items.find((item) => item.id === id)
    if (!moving || !days.value.some((day) => day.date === targetDate)) return false
    const sourceDate = moving.scheduled_on
    const dates = [...new Set([sourceDate, targetDate])]
    const complete = dates.map((date) => snapshot.collections[date])
    if (complete.some((day) => !day?.complete)) return false
    const target = ordered(snapshot.items, targetDate).filter((item) => item.id !== id)
    const clamped = Math.max(0, Math.min(index, target.length))
    if (
      sourceDate === targetDate &&
      ordered(snapshot.items, targetDate).findIndex((item) => item.id === id) === clamped
    )
      return false
    target.splice(clamped, 0, moving)
    const body: ItineraryReorder = {
      days: dates.map((date) => ({
        date,
        items: (date === targetDate
          ? target
          : ordered(snapshot.items, date).filter((item) => item.id !== id)
        ).map((item) => ({ id: item.id, base_version: item.version })),
      })),
    }
    const original = complete.flatMap((day) => day!.items.map((item) => item.id))
    const submitted = body.days.flatMap((day) => day.items.map((item) => item.id))
    if (
      new Set(original).size !== original.length ||
      new Set(submitted).size !== original.length ||
      submitted.length !== original.length ||
      submitted.some((id) => !original.includes(id))
    )
      return false
    const baseline = new CollectionBaseline({
      complete: complete.every((day) => day!.complete),
      guards: complete.flatMap((day) => day!.baseline.guards),
    })
    for (const day of body.days) {
      day.items.forEach(Object.freeze)
      Object.freeze(day.items)
      Object.freeze(day)
    }
    Object.freeze(body.days)
    Object.freeze(body)
    pending.value = Object.freeze({
      body,
      baseline,
      operationId: intent.key({ tripId, ...baseline.intent(body) }),
    })
    draft.value = Object.freeze(
      snapshot.items.map((item) => {
        const day = body.days.find((day) => day.items.some((ref) => ref.id === item.id))
        return day
          ? Object.freeze({
              ...item,
              scheduled_on: day.date,
              sort_order: day.items.findIndex((ref) => ref.id === item.id),
            })
          : item
      }),
    )
    latest.value = null
    latestError.value = null
    uncertain.value = false
    return submitPending()
  }

  async function retryPending() {
    return uncertain.value ? submitPending() : false
  }

  async function loadLatest(): Promise<boolean> {
    if (!pending.value || uncertain.value || reordering.value || loadingLatest.value || disposed)
      return false
    const { run, signal, captured } = beginRead()
    loadingLatest.value = true
    latest.value = null
    latestError.value = null
    try {
      const candidate = await readSnapshot(
        signal,
        captured,
        pending.value.body.days.map((day) => day.date),
      )
      if (!current(run, captured) || !pending.value || uncertain.value) return false
      latest.value = candidate
      return true
    } catch (cause) {
      if (current(run, captured)) {
        controller?.abort()
        latestError.value = actionError(cause, '最新安排读取失败，当前排序和原基线已保留。')
      }
      return false
    } finally {
      if (!disposed && run === generation) loadingLatest.value = false
    }
  }

  function adoptLatest(): boolean {
    if (
      disposed ||
      !pending.value ||
      !latest.value ||
      uncertain.value ||
      reordering.value ||
      loadingLatest.value ||
      latest.value.span !== spanKey()
    )
      return false
    confirmed.value = latest.value
    draft.value = latest.value = pending.value = null
    actionFailure.value = error.value = latestError.value = null
    intent.reset()
    return true
  }

  watch(
    spanKey,
    () => {
      generation++
      controller?.abort()
      loading.value = loadingLatest.value = false
      latest.value = null
      if (!pending.value && !disposed) void reload()
    },
    { flush: 'sync' },
  )
  if (getCurrentScope())
    onScopeDispose(() => {
      disposed = true
      generation++
      controller?.abort()
    })

  return {
    items,
    days,
    confirmed,
    draft,
    latest,
    hasPending,
    comparison,
    canReorder,
    loading,
    loadingLatest,
    latestError,
    error,
    reordering,
    uncertain,
    savedNeedsReload,
    actionFailure,
    feedback,
    reload,
    moveItem,
    retryPending,
    loadLatest,
    adoptLatest,
  }
}
