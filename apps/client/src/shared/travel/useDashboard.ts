import { onBeforeUnmount, ref, watch, type Ref } from 'vue'

import { ApiError } from '@/shared/api/auth'
import {
  getDashboard,
  type DashboardPlace,
  type DashboardQuery,
  type DashboardSnapshot,
} from '@/shared/api/dashboard'
import { updateItineraryItem } from '@/shared/api/itinerary'
import { actionError, createWriteIntent, writeWarnings } from '@/shared/api/writes'
import { useSessionStore } from '@/shared/stores/session'

export function useDashboard(query: Ref<DashboardQuery>) {
  const session = useSessionStore()
  const snapshot = ref<DashboardSnapshot | null>(null)
  const loading = ref(false)
  const error = ref('')
  const saving = ref('')
  const writeError = ref('')
  const notice = ref('')
  const intent = createWriteIntent()
  let generation = 0
  let controller: AbortController | undefined
  let disposed = false
  let locationAfter = ''

  async function load(clear = false) {
    const current = ++generation
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal
    const accountId = session.account?.id
    if (clear) {
      snapshot.value = null
      locationAfter = ''
    }
    error.value = ''
    loading.value = !!accountId
    if (!accountId) return
    try {
      for (let batch = 0; batch < 5; batch++) {
        const next = await getDashboard(
          { ...query.value, location_after: locationAfter || undefined },
          signal,
        )
        if (disposed || current !== generation || session.account?.id !== accountId) return
        snapshot.value = next
        locationAfter = next.next_location_after
        if (!locationAfter) break
      }
    } catch (cause) {
      if (!disposed && current === generation && !signal.aborted)
        error.value = actionError(cause, '看板加载失败，请检查网络后重试。')
    } finally {
      if (!disposed && current === generation) loading.value = false
    }
  }

  async function togglePlace(place: DashboardPlace) {
    if (saving.value || loading.value || place.kind === 'transport') return
    const accountId = session.account?.id
    saving.value = place.id
    writeError.value = ''
    notice.value = ''
    const patch = { footprint_excluded: !place.excluded }
    const operationId = intent.key({ accountId, id: place.id, version: place.version, patch })
    try {
      const outcome = await updateItineraryItem(
        place.trip_id,
        place.id,
        place.version,
        patch,
        operationId,
      )
      if (disposed || accountId !== session.account?.id) return
      intent.reset()
      notice.value = writeWarnings(outcome.result).join(' ')
      await load()
      if (error.value) writeError.value = '设置已保存，但统计刷新失败，请刷新看板。'
    } catch (cause) {
      if (disposed || accountId !== session.account?.id) return
      writeError.value = actionError(cause, '保存失败，请重试；页面尚未更改此地点的设置。')
      if (
        cause instanceof ApiError &&
        ['VERSION_CONFLICT', 'RESOURCE_GONE', 'TRIP_DELETED'].includes(cause.code ?? '')
      )
        await load()
    } finally {
      if (!disposed && accountId === session.account?.id) saving.value = ''
    }
  }

  watch(
    [query, () => session.account?.id],
    () => {
      saving.value = ''
      writeError.value = ''
      notice.value = ''
      intent.reset()
      void load(true)
    },
    { immediate: true, deep: true },
  )
  onBeforeUnmount(() => {
    disposed = true
    generation++
    controller?.abort()
  })
  return { snapshot, loading, error, saving, writeError, notice, load, togglePlace }
}
