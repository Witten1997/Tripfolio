import type { components } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import type { WriteResult } from '@/shared/api/writes'

export type TripMember = components['schemas']['TripMember']
export type TripMemberInput = components['schemas']['TripMemberInput']

export async function listTripMembers(tripId: string): Promise<TripMember[]> {
  const { data, error } = await api.GET('/trips/{trip_id}/members', {
    params: { path: { trip_id: tripId } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

/** 整体保存：数组顺序即排序；未出现的有效成员被删除。 */
export async function saveTripMembers(
  tripId: string,
  members: TripMemberInput[],
  operationId: string,
): Promise<WriteResult> {
  const { data, error } = await api.PUT('/trips/{trip_id}/members', {
    params: { path: { trip_id: tripId }, header: { 'Idempotency-Key': operationId } },
    body: { members },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export function memberName(members: readonly TripMember[], id: string | null | undefined) {
  if (!id) return ''
  return members.find((m) => m.id === id)?.name ?? '已删除成员'
}
