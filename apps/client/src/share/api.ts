import type { components } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/problem'

import type { ShareClient } from './shareClient'

export type PublicTrip = components['schemas']['PublicTrip']
export type PublicItineraryItem = components['schemas']['PublicItineraryItem']
export type PublicRoutes = components['schemas']['PublicRoutes']
export type PublicLeg = components['schemas']['PublicLeg']
export type SharedTravelMode = components['schemas']['GeoTravelMode']

export async function getSharedTrip(client: ShareClient): Promise<PublicTrip> {
  const { data, error } = await client.GET('/public/trip')
  if (error || !data) throw new ApiError(error)
  return data.data
}

/** 访客页一次展示整趟旅行，按页大小上限循环翻页直到没有下一页。 */
export async function listAllSharedItineraryItems(
  client: ShareClient,
): Promise<PublicItineraryItem[]> {
  const items: PublicItineraryItem[] = []
  let cursor: string | undefined
  do {
    const { data, error } = await client.GET('/public/itinerary-items', {
      params: { query: { limit: 100, cursor } },
    })
    if (error || !data) throw new ApiError(error)
    items.push(...data.items)
    cursor = data.next_cursor ?? undefined
  } while (cursor)
  return items
}

export async function getSharedRoutes(
  client: ShareClient,
  mode: SharedTravelMode,
): Promise<PublicRoutes> {
  const { data, error } = await client.GET('/public/routes', { params: { query: { mode } } })
  if (error || !data) throw new ApiError(error)
  return data.data
}
