import type { components } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import { versionHeaders, writeOutcome } from '@/shared/api/writes'

export type RoutePlan = components['schemas']['RoutePlan']
export type RouteLeg = components['schemas']['RouteLeg']
export type RouteLegMode = components['schemas']['RouteLegModePatch']['mode']

export async function getRoutePlan(tripId: string): Promise<RoutePlan> {
  const { data, error } = await api.GET('/trips/{trip_id}/route-plan', {
    params: { path: { trip_id: tripId } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function recalculateRoutePlan(tripId: string, operationId: string) {
  const { data, error } = await api.POST('/trips/{trip_id}/route-plan/recalculate', {
    params: { path: { trip_id: tripId }, header: { 'Idempotency-Key': operationId } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function updateRouteLegMode(
  tripId: string,
  legId: string,
  version: string,
  mode: RouteLegMode,
  operationId: string,
) {
  const { data, error } = await api.PATCH('/trips/{trip_id}/route-legs/{leg_id}', {
    params: {
      path: { trip_id: tripId, leg_id: legId },
      header: versionHeaders(operationId, version),
    },
    body: { mode },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<RouteLeg>(data.data, (value) => typeof value.id === 'string')
}
