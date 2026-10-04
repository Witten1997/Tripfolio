import type { components, operations } from '@tripfolio/contracts/openapi/v1'
import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import { versionHeaders, writeOutcome } from '@/shared/api/writes'

export type Photo = components['schemas']['Photo']
export type PhotoCreate = components['schemas']['PhotoCreate']
export type PhotoPatch = components['schemas']['PhotoPatch']
export type PhotoQuery = NonNullable<operations['listPhotos']['parameters']['query']>

export async function listPhotos(tripId: string, query: PhotoQuery = {}) {
  const { data, error } = await api.GET('/trips/{trip_id}/photos', {
    params: { path: { trip_id: tripId }, query },
  })
  if (error || !data) throw new ApiError(error)
  return data
}
export async function getPhoto(tripId: string, id: string): Promise<Photo> {
  const { data, error } = await api.GET('/trips/{trip_id}/photos/{photo_id}', {
    params: { path: { trip_id: tripId, photo_id: id } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}
const isResource = (v: Record<string, unknown>) =>
  typeof v.id === 'string' && typeof v.version === 'string'
export async function createPhoto(tripId: string, body: PhotoCreate, operationId: string) {
  const { data, error } = await api.POST('/trips/{trip_id}/photos', {
    params: { path: { trip_id: tripId }, header: { 'Idempotency-Key': operationId } },
    body,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Photo>(data.data, isResource)
}
export async function updatePhoto(
  tripId: string,
  id: string,
  version: string,
  body: PhotoPatch,
  operationId: string,
) {
  const { data, error } = await api.PATCH('/trips/{trip_id}/photos/{photo_id}', {
    params: {
      path: { trip_id: tripId, photo_id: id },
      header: versionHeaders(operationId, version),
    },
    body,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Photo>(data.data, isResource)
}
export async function deletePhoto(
  tripId: string,
  id: string,
  version: string,
  operationId: string,
) {
  const { data, error } = await api.DELETE('/trips/{trip_id}/photos/{photo_id}', {
    params: {
      path: { trip_id: tripId, photo_id: id },
      header: versionHeaders(operationId, version),
    },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Photo>(data.data, isResource)
}
