import type { components, operations } from '@tripfolio/contracts/openapi/v1'

import { ApiError, type Problem } from '@/shared/api/auth'
import { api } from '@/shared/api/client'

export type GeoPlace = components['schemas']['GeoPlace']
export type GeoCoordinate = components['schemas']['GeoCoordinate']
export type GeoRoute = components['schemas']['GeoRoute']
export type TravelMode = components['schemas']['GeoTravelMode']
export type PlaceQuery = operations['searchPlaces']['parameters']['query']

export const travelModeLabels: Record<TravelMode, string> = {
  driving: '驾车',
  walking: '步行',
  cycling: '骑行',
}

/** 503 只有带 Retry-After 才自动重试，避免对凭证错误／耗尽配额重复算路。 */
export class GeoRouteError extends ApiError {
  readonly retryAfterMs: number | null
  constructor(problem: Problem | undefined, retryAfter: string | null) {
    super(problem)
    this.name = 'GeoRouteError'
    this.retryAfterMs = parseRetryAfter(retryAfter)
  }
}

export function parseRetryAfter(value: string | null, now = Date.now()): number | null {
  if (!value?.trim()) return null
  const raw = value.trim()
  if (/^\d+$/.test(raw)) {
    const delay = Number(raw) * 1000
    return Number.isFinite(delay) ? delay : null
  }
  const date = Date.parse(raw)
  return Number.isFinite(date) ? Math.max(0, date - now) : null
}

export async function searchPlaces(query: PlaceQuery, signal?: AbortSignal): Promise<GeoPlace[]> {
  const { data, error } = await api.GET('/geo/places', { params: { query }, signal })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function reverseGeocode(
  point: GeoCoordinate,
  signal?: AbortSignal,
): Promise<GeoPlace> {
  const { data, error } = await api.GET('/geo/reverse-geocode', {
    params: { query: point },
    signal,
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function calculateRoute(
  from: GeoCoordinate,
  to: GeoCoordinate,
  mode: TravelMode,
  signal?: AbortSignal,
): Promise<GeoRoute> {
  const { data, error, response } = await api.GET('/geo/routes', {
    params: {
      query: {
        origin_latitude: from.latitude,
        origin_longitude: from.longitude,
        destination_latitude: to.latitude,
        destination_longitude: to.longitude,
        mode,
      },
    },
    signal,
  })
  if (error || !data) throw new GeoRouteError(error, response.headers.get('Retry-After'))
  return data.data
}
