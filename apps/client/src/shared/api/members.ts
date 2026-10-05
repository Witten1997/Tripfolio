import type { components } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import { CollectionBaseline } from '@/shared/api/collectionGuards'
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

type ScopeRevision = components['schemas']['ScopeRevision']

export function captureMembersBaseline(
  tripId: string,
  revisions: readonly ScopeRevision[] | undefined,
): CollectionBaseline {
  if (
    !Array.isArray(revisions) ||
    revisions.length !== 1 ||
    revisions[0]?.kind !== 'members' ||
    revisions[0]?.scope_id !== tripId
  ) {
    throw new Error('成员资料缺少有效基线，请重新加载后再编辑。')
  }
  return new CollectionBaseline({
    complete: true,
    guards: [
      {
        kind: 'members',
        scope_id: tripId,
        revision: revisions[0].revision,
      },
    ],
  })
}

export async function listTripMembersWithBaseline(tripId: string): Promise<{
  members: TripMember[]
  baseline: CollectionBaseline
}> {
  const { data, error } = await api.GET('/trips/{trip_id}/members', {
    params: { path: { trip_id: tripId } },
  })
  if (error || !data) throw new ApiError(error)
  if (!Array.isArray(data.data)) throw new Error('成员资料不完整，请重新加载。')
  return { members: data.data, baseline: captureMembersBaseline(tripId, data.scope_revisions) }
}

/** 整体保存：数组顺序即排序；未出现的有效成员被删除。 */
export async function saveTripMembers(
  tripId: string,
  members: TripMemberInput[],
  operationId: string,
  baseline?: CollectionBaseline,
): Promise<WriteResult> {
  const { data, error } = await api.PUT('/trips/{trip_id}/members', {
    params: { path: { trip_id: tripId }, header: { 'Idempotency-Key': operationId } },
    headers: baseline?.headers,
    body: { members },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export function memberName(members: readonly TripMember[], id: string | null | undefined) {
  if (!id) return ''
  return members.find((m) => m.id === id)?.name ?? '已删除成员'
}
