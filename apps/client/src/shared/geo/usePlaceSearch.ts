import { onScopeDispose, ref, shallowRef, watch } from 'vue'

import { ApiError } from '@/shared/api/auth'
import { searchPlaces, type GeoCoordinate, type GeoPlace } from '@/shared/api/geo'

export function usePlaceSearch(initialCity = '', near: () => GeoCoordinate | null = () => null) {
  const keyword = ref('')
  const city = ref(initialCity)
  const results = shallowRef<GeoPlace[]>([])
  const loading = ref(false)
  const searched = ref(false)
  const error = ref<string | null>(null)
  let timer: ReturnType<typeof setTimeout> | undefined
  let controller: AbortController | undefined
  let generation = 0

  async function search() {
    if (timer) clearTimeout(timer)
    const request = ++generation
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal
    const q = keyword.value.trim()
    if (!q) {
      results.value = []
      searched.value = false
      loading.value = false
      return
    }
    loading.value = true
    error.value = null
    try {
      const point = near()
      const places = await searchPlaces(
        { q, city: city.value.trim() || undefined, ...point },
        signal,
      )
      if (request === generation) results.value = places
    } catch (cause) {
      if (request !== generation || signal.aborted) return
      results.value = []
      if (!(cause instanceof ApiError && cause.code === 'RESOURCE_NOT_FOUND')) {
        error.value =
          cause instanceof ApiError && cause.code === 'RATE_LIMITED'
            ? '搜索较频繁，请稍后再试；也可使用地图选点'
            : '地点搜索暂不可用，请重试或使用地图选点'
      }
    } finally {
      if (request === generation) {
        loading.value = false
        searched.value = true
      }
    }
  }

  watch(
    [keyword, city],
    () => {
      generation++
      controller?.abort()
      if (timer) clearTimeout(timer)
      loading.value = false
      error.value = null
      results.value = []
      searched.value = false
      if (keyword.value.trim().length >= 2)
        timer = setTimeout(() => {
          void search()
        }, 450)
    },
    { flush: 'sync' },
  )
  onScopeDispose(() => {
    generation++
    controller?.abort()
    if (timer) clearTimeout(timer)
  })
  return { keyword, city, results, loading, searched, error, search }
}
