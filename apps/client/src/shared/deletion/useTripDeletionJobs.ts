import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import { getDeletionJob, getTripDeletionJob, type DeletionJob } from '@/shared/api/deletion'
import { useSessionStore } from '@/shared/stores/session'

export interface TrackedJob {
  tripId: string
  job: DeletionJob | null
  error: string | null
}

export function useTripDeletionJobs() {
  const session = useSessionStore()
  const tasks = shallowRef<TrackedJob[]>([])
  const loading = ref(false)
  let generation = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  const storageKey = () => `tripfolio.trip-deletions:${session.account?.id ?? ''}`

  function remember() {
    if (!session.account) return
    try {
      window.sessionStorage.setItem(
        storageKey(),
        JSON.stringify(tasks.value.map((task) => task.tripId)),
      )
    } catch {
      /* 服务端仍可按回收站旅行恢复活动任务。 */
    }
  }

  function track(tripId: string) {
    if (!tasks.value.some((task) => task.tripId === tripId)) {
      tasks.value = [{ tripId, job: null, error: null }, ...tasks.value].slice(0, 30)
      remember()
    }
    void refresh()
  }

  async function refresh() {
    if (loading.value || !session.isAuthenticated || !tasks.value.length) return
    clearTimeout(timer)
    const request = generation
    loading.value = true
    const current = [...tasks.value]
    try {
      // 限制并发，避免大量回收站记录同时轮询。
      for (const task of current) {
        try {
          const job = task.job
            ? await getDeletionJob(task.job.id)
            : await getTripDeletionJob(task.tripId)
          if (request !== generation) return
          tasks.value = tasks.value.map((item) =>
            item.tripId === task.tripId ? { ...item, job, error: null } : item,
          )
        } catch {
          if (request !== generation) return
          tasks.value = tasks.value.map((item) =>
            item.tripId === task.tripId
              ? { ...item, error: '无法查询清理结果，请刷新重试。' }
              : item,
          )
        }
      }
    } finally {
      if (request === generation) {
        loading.value = false
        if (
          tasks.value.some((task) => !task.job || ['queued', 'running'].includes(task.job.status))
        )
          timer = setTimeout(
            () => void refresh(),
            tasks.value.some((task) => task.error) ? 10000 : 4000,
          )
      }
    }
  }

  function dismiss(tripId: string) {
    tasks.value = tasks.value.filter((task) => task.tripId !== tripId)
    remember()
  }
  watch(
    () => session.account?.id,
    () => {
      generation++
      clearTimeout(timer)
      loading.value = false
      tasks.value = []
      if (!session.account) return
      try {
        const ids: unknown = JSON.parse(window.sessionStorage.getItem(storageKey()) ?? '[]')
        if (Array.isArray(ids))
          tasks.value = ids
            .filter((id): id is string => typeof id === 'string')
            .slice(0, 30)
            .map((tripId) => ({ tripId, job: null, error: null }))
      } catch {
        /* 无有效本地记录时从回收站发现任务。 */
      }
      void refresh()
    },
    { immediate: true },
  )
  onScopeDispose(() => {
    generation++
    clearTimeout(timer)
  })
  return { tasks, loading, track, refresh, dismiss }
}
