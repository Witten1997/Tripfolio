import type { components, operations } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import { versionHeaders, writeOutcome } from '@/shared/api/writes'

export type Trip = components['schemas']['Trip']
export type TripListItem = components['schemas']['TripListItem']
export type TripCreate = components['schemas']['TripCreate']
export type TripPatch = components['schemas']['TripPatch']
export type TripQuery = NonNullable<operations['listTrips']['parameters']['query']>
export type TrashedTrip = components['schemas']['TrashedTrip']

function isTrip(value: Record<string, unknown>) {
  return (
    typeof value.id === 'string' &&
    typeof value.version === 'string' &&
    typeof value.name === 'string' &&
    typeof value.currency_code === 'string' &&
    typeof value.start_date === 'string'
  )
}

export async function listTrips(query: TripQuery) {
  const { data, error } = await api.GET('/trips', { params: { query } })
  if (error || !data) throw new ApiError(error)
  return data
}

export async function getTrip(id: string): Promise<Trip> {
  const { data, error } = await api.GET('/trips/{trip_id}', { params: { path: { trip_id: id } } })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function createTrip(input: TripCreate, operationId: string) {
  const { data, error } = await api.POST('/trips', {
    params: { header: { 'Idempotency-Key': operationId } },
    body: input,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Trip>(data.data, isTrip)
}

export async function updateTrip(
  id: string,
  version: string,
  patch: TripPatch,
  operationId: string,
) {
  const { data, error } = await api.PATCH('/trips/{trip_id}', {
    params: { path: { trip_id: id }, header: versionHeaders(operationId, version) },
    body: patch,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Trip>(data.data, isTrip)
}

export async function archiveTrip(
  id: string,
  version: string,
  archived: boolean,
  operationId: string,
) {
  const { data, error } = await api.POST('/trips/{trip_id}/archive', {
    params: { path: { trip_id: id }, header: versionHeaders(operationId, version) },
    body: { archived },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Trip>(data.data, isTrip)
}

export async function trashTrip(id: string, version: string, operationId: string) {
  const { data, error } = await api.DELETE('/trips/{trip_id}', {
    params: { path: { trip_id: id }, header: versionHeaders(operationId, version) },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Trip>(data.data, isTrip)
}

export async function listTrashedTrips(query: { limit?: number; cursor?: string } = {}) {
  const { data, error } = await api.GET('/recycle-bin/trips', { params: { query } })
  if (error || !data) throw new ApiError(error)
  return data
}

export async function getTrashedTrip(id: string): Promise<TrashedTrip> {
  const { data, error } = await api.GET('/recycle-bin/trips/{trip_id}', {
    params: { path: { trip_id: id } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function restoreTrip(id: string, version: string, operationId: string) {
  const { data, error } = await api.POST('/recycle-bin/trips/{trip_id}/restore', {
    params: { path: { trip_id: id }, header: versionHeaders(operationId, version) },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Trip>(data.data, isTrip)
}

export async function purgeTrip(id: string, version: string, operationId: string) {
  const { data, error, response } = await api.POST('/recycle-bin/trips/{trip_id}/purge', {
    params: { path: { trip_id: id }, header: versionHeaders(operationId, version) },
    body: { confirm: true },
  })
  if (error || !data) throw new ApiError(error)
  return { ...writeOutcome<Trip>(data.data, isTrip), accepted: response.status === 202 }
}
