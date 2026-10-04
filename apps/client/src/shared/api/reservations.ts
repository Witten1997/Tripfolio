import type { components, operations } from '@tripfolio/contracts/openapi/v1'
import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import { versionHeaders, writeOutcome } from '@/shared/api/writes'

export type Reservation = components['schemas']['Reservation']
export type ReservationCreate = components['schemas']['ReservationCreate']
export type ReservationPatch = components['schemas']['ReservationPatch']
export type ReservationQuery = NonNullable<operations['listReservations']['parameters']['query']>

export async function listReservations(tripId: string, query: ReservationQuery = {}) {
  const { data, error } = await api.GET('/trips/{trip_id}/reservations', {
    params: { path: { trip_id: tripId }, query },
  })
  if (error || !data) throw new ApiError(error)
  return data
}
export async function getReservation(tripId: string, id: string): Promise<Reservation> {
  const { data, error } = await api.GET('/trips/{trip_id}/reservations/{reservation_id}', {
    params: { path: { trip_id: tripId, reservation_id: id } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}
const isResource = (v: Record<string, unknown>) =>
  typeof v.id === 'string' && typeof v.version === 'string'
export async function createReservation(
  tripId: string,
  body: ReservationCreate,
  operationId: string,
) {
  const { data, error } = await api.POST('/trips/{trip_id}/reservations', {
    params: { path: { trip_id: tripId }, header: { 'Idempotency-Key': operationId } },
    body,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Reservation>(data.data, isResource)
}
export async function updateReservation(
  tripId: string,
  id: string,
  version: string,
  body: ReservationPatch,
  operationId: string,
) {
  const { data, error } = await api.PATCH('/trips/{trip_id}/reservations/{reservation_id}', {
    params: {
      path: { trip_id: tripId, reservation_id: id },
      header: versionHeaders(operationId, version),
    },
    body,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Reservation>(data.data, isResource)
}
export async function deleteReservation(
  tripId: string,
  id: string,
  version: string,
  operationId: string,
) {
  const { data, error } = await api.DELETE('/trips/{trip_id}/reservations/{reservation_id}', {
    params: {
      path: { trip_id: tripId, reservation_id: id },
      header: versionHeaders(operationId, version),
    },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Reservation>(data.data, isResource)
}
