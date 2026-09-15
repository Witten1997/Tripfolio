import type { components } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'

export type TripShare = components['schemas']['TripShare']

/** 未开启分享时返回 null；其余错误抛出。分享接口不带幂等键与 If-Match。 */
export async function getTripShare(tripId: string): Promise<TripShare | null> {
  const { data, error, response } = await api.GET('/trips/{trip_id}/share', {
    params: { path: { trip_id: tripId } },
  })
  if (response.status === 404 && error?.code === 'SHARE_NOT_FOUND') return null
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function enableTripShare(tripId: string): Promise<TripShare> {
  const { data, error } = await api.PUT('/trips/{trip_id}/share', {
    params: { path: { trip_id: tripId } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function rotateTripShare(tripId: string): Promise<TripShare> {
  const { data, error } = await api.POST('/trips/{trip_id}/share/rotate', {
    params: { path: { trip_id: tripId } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function disableTripShare(tripId: string): Promise<void> {
  const { error, response } = await api.DELETE('/trips/{trip_id}/share', {
    params: { path: { trip_id: tripId } },
  })
  if (error || !response.ok) throw new ApiError(error)
}
