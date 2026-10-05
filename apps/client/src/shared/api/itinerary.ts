import type { components, operations } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import type { CollectionBaseline } from '@/shared/api/collectionGuards'
import { versionHeaders, writeOutcome, type WriteResult } from '@/shared/api/writes'

export type ItineraryItem = components['schemas']['ItineraryItem']
export type ItineraryCreate = components['schemas']['ItineraryCreate']
export type ItineraryPatch = components['schemas']['ItineraryPatch']
export type ItineraryReorder = components['schemas']['ItineraryReorder']
export type ItineraryKind = components['schemas']['ItineraryKind']
export type ItineraryQuery = NonNullable<operations['listItineraryItems']['parameters']['query']>

export { itineraryKindLabels } from '@/shared/travel/itineraryKinds'

function isItem(value: Record<string, unknown>) {
  return (
    typeof value.id === 'string' &&
    typeof value.version === 'string' &&
    typeof value.title === 'string' &&
    typeof value.scheduled_on === 'string'
  )
}

export async function listItineraryItems(
  tripId: string,
  query: ItineraryQuery = {},
  signal?: AbortSignal,
) {
  const { data, error } = await api.GET('/trips/{trip_id}/itinerary-items', {
    params: { path: { trip_id: tripId }, query },
    signal,
  })
  if (error || !data) throw new ApiError(error)
  return data
}

/** 行程页一次展示整趟旅行，按页大小上限循环翻页直到没有下一页。 */
export async function listAllItineraryItems(
  tripId: string,
  signal?: AbortSignal,
): Promise<ItineraryItem[]> {
  const items: ItineraryItem[] = []
  let cursor: string | undefined
  do {
    signal?.throwIfAborted()
    const page = await listItineraryItems(tripId, { limit: 100, cursor }, signal)
    signal?.throwIfAborted()
    items.push(...page.items)
    cursor = page.next_cursor ?? undefined
  } while (cursor)
  return items
}

export async function getItineraryItem(tripId: string, id: string): Promise<ItineraryItem> {
  const { data, error } = await api.GET('/trips/{trip_id}/itinerary-items/{item_id}', {
    params: { path: { trip_id: tripId, item_id: id } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function createItineraryItem(
  tripId: string,
  input: ItineraryCreate,
  operationId: string,
) {
  const { data, error } = await api.POST('/trips/{trip_id}/itinerary-items', {
    params: { path: { trip_id: tripId }, header: { 'Idempotency-Key': operationId } },
    body: input,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<ItineraryItem>(data.data, isItem)
}

export async function updateItineraryItem(
  tripId: string,
  id: string,
  version: string,
  patch: ItineraryPatch,
  operationId: string,
) {
  const { data, error } = await api.PATCH('/trips/{trip_id}/itinerary-items/{item_id}', {
    params: {
      path: { trip_id: tripId, item_id: id },
      header: versionHeaders(operationId, version),
    },
    body: patch,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<ItineraryItem>(data.data, isItem)
}

export async function deleteItineraryItem(
  tripId: string,
  id: string,
  version: string,
  operationId: string,
) {
  const { data, error } = await api.DELETE('/trips/{trip_id}/itinerary-items/{item_id}', {
    params: {
      path: { trip_id: tripId, item_id: id },
      header: versionHeaders(operationId, version),
    },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<ItineraryItem>(data.data, isItem)
}

export async function reorderItineraryItems(
  tripId: string,
  body: ItineraryReorder,
  operationId: string,
  baseline: CollectionBaseline,
): Promise<WriteResult> {
  const { data, error } = await api.POST('/trips/{trip_id}/itinerary-items/reorder', {
    params: {
      path: { trip_id: tripId },
      header: { 'Idempotency-Key': operationId, ...baseline.headers },
    },
    body,
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}
