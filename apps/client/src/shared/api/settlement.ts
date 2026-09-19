import type { components } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'

export type TripSettlement = components['schemas']['TripSettlement']
export type MemberSettlement = components['schemas']['MemberSettlement']
export type SettlementTransfer = components['schemas']['SettlementTransfer']

export async function getTripSettlement(tripId: string): Promise<TripSettlement> {
  const { data, error } = await api.GET('/trips/{trip_id}/settlement', {
    params: { path: { trip_id: tripId } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}
