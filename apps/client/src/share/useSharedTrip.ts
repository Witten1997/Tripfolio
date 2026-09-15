import { ref, shallowRef } from 'vue'

import { ApiError } from '@/shared/api/problem'

import {
  getSharedRoutes,
  getSharedTrip,
  listAllSharedItineraryItems,
  type PublicItineraryItem,
  type PublicRoutes,
  type PublicTrip,
  type SharedTravelMode,
} from './api'
import type { ShareClient } from './shareClient'

export type SharedTripError = 'invalid' | 'deleted' | 'network'
export type SharedRoutesState = 'idle' | 'loading' | 'ready' | 'unavailable'

export interface SharedTripDeps {
  trip: (client: ShareClient) => Promise<PublicTrip>
  items: (client: ShareClient) => Promise<PublicItineraryItem[]>
  routes: (client: ShareClient, mode: SharedTravelMode) => Promise<PublicRoutes>
}

const defaultDeps: SharedTripDeps = {
  trip: getSharedTrip,
  items: listAllSharedItineraryItems,
  routes: getSharedRoutes,
}

/** 失效链接、已删除旅行与网络错误分别呈现，不向访客暴露主人或旅行是否存在的更多信息。 */
export function classifySharedError(cause: unknown): SharedTripError {
  if (!(cause instanceof ApiError)) return 'network'
  switch (cause.code) {
    case 'TRIP_DELETED':
      return 'deleted'
    case 'SHARE_NOT_FOUND':
    case 'ACCOUNT_DELETING':
    case 'AUTH_REQUIRED':
      return 'invalid'
    default:
      return 'network'
  }
}

export function useSharedTrip(client: ShareClient, deps: SharedTripDeps = defaultDeps) {
  const trip = shallowRef<PublicTrip | null>(null)
  const items = shallowRef<PublicItineraryItem[]>([])
  const loading = ref(false)
  const error = ref<SharedTripError | null>(null)
  const routes = shallowRef<PublicRoutes | null>(null)
  const routesState = ref<SharedRoutesState>('idle')
  let generation = 0
  let routesGeneration = 0

  async function load() {
    const request = ++generation
    loading.value = true
    error.value = null
    try {
      const [loadedTrip, loadedItems] = await Promise.all([deps.trip(client), deps.items(client)])
      if (request !== generation) return
      trip.value = loadedTrip
      items.value = loadedItems
    } catch (cause) {
      if (request !== generation) return
      error.value = classifySharedError(cause)
    } finally {
      if (request === generation) loading.value = false
    }
  }

  /** 算路失败（配额耗尽 503、限流 429、网络）统一降级为示意连线：不重试，不阻塞行程展示。 */
  async function loadRoutes(mode: SharedTravelMode) {
    const request = ++routesGeneration
    routesState.value = 'loading'
    try {
      const result = await deps.routes(client, mode)
      if (request !== routesGeneration) return
      routes.value = result
      routesState.value = 'ready'
    } catch {
      if (request !== routesGeneration) return
      routes.value = null
      routesState.value = 'unavailable'
    }
  }

  return { trip, items, loading, error, routes, routesState, load, loadRoutes }
}
