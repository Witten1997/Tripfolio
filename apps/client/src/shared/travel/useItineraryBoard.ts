import { computed, ref, shallowRef } from 'vue'

import { ApiError } from '@/shared/api/auth'
import {
  listAllItineraryItems,
  reorderItineraryItems,
  type ItineraryItem,
  type ItineraryReorder,
} from '@/shared/api/itinerary'
import { actionError, createWriteIntent, writeWarnings } from '@/shared/api/writes'
import { groupByDay, type TripDay } from '@/shared/travel/tripDays'

export interface TripSpan {
  start: string
  end: string
}

/** 行程页的数据与重排：一次拉全整趟旅行的项目，按天分组；拖动落到重排接口。 */
export function useItineraryBoard(tripId: string, span: () => TripSpan) {
  const items = shallowRef<ItineraryItem[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  const reordering = ref(false)
  const actionFailure = ref<string | null>(null)
  const feedback = ref<string[]>([])
  const intent = createWriteIntent()
  let generation = 0

  const days = computed<TripDay[]>(() => {
    const { start, end } = span()
    return groupByDay(start, end, items.value)
  })

  async function reload() {
    const request = ++generation
    loading.value = true
    error.value = null
    try {
      const loaded = await listAllItineraryItems(tripId)
      if (request === generation) items.value = loaded
    } catch (cause) {
      if (request === generation) error.value = actionError(cause, '无法加载行程，请检查网络后重试')
    } finally {
      if (request === generation) loading.value = false
    }
  }

  function dayItems(date: string): ItineraryItem[] {
    return days.value.find((d) => d.date === date)?.items ?? []
  }

  /** 把 id 放到 targetDate 的 index 位置；提交源日与目标日的完整集合。位置不变时不请求。 */
  async function moveItem(id: string, targetDate: string, index: number): Promise<boolean> {
    if (reordering.value) return false
    const moving = items.value.find((i) => i.id === id)
    if (!moving) return false
    const sourceDate = moving.scheduled_on
    const target = dayItems(targetDate).filter((i) => i.id !== id)
    const clamped = Math.max(0, Math.min(index, target.length))
    target.splice(clamped, 0, moving)
    const days: ItineraryReorder['days'] = []
    if (sourceDate !== targetDate) {
      days.push({
        date: sourceDate,
        items: dayItems(sourceDate)
          .filter((i) => i.id !== id)
          .map((i) => ({ id: i.id, base_version: i.version })),
      })
    }
    days.push({
      date: targetDate,
      items: target.map((i) => ({ id: i.id, base_version: i.version })),
    })
    const unchanged =
      sourceDate === targetDate && dayItems(targetDate).findIndex((i) => i.id === id) === clamped
    if (unchanged) return false

    reordering.value = true
    actionFailure.value = null
    const body: ItineraryReorder = { days }
    try {
      const result = await reorderItineraryItems(tripId, body, intent.key(body))
      intent.reset()
      const warnings = writeWarnings(result)
      feedback.value = warnings
      await reload()
      return true
    } catch (cause) {
      actionFailure.value =
        cause instanceof ApiError && cause.code === 'ORDER_CHANGED'
          ? '这些日期的行程已经变化，已重新加载，请再试一次。'
          : actionError(cause, '网络连接中断，排序结果尚未确认。已重新加载，请核对后再试。')
      await reload()
      return false
    } finally {
      reordering.value = false
    }
  }

  return { items, days, loading, error, reordering, actionFailure, feedback, reload, moveItem }
}
