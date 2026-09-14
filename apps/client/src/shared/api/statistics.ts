import type { components, operations } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'

export type TripStatistics = components['schemas']['TripStatistics']
export type StatisticsTotals = components['schemas']['StatisticsTotals']
export type TripBudget = components['schemas']['TripBudget']
export type CategoryTotals = components['schemas']['CategoryTotals']
export type DailyTotals = components['schemas']['DailyTotals']
export type StatisticsQuery = NonNullable<operations['getTripStatistics']['parameters']['query']>

export async function getTripStatistics(
  tripId: string,
  query: StatisticsQuery = {},
): Promise<TripStatistics> {
  const { data, error } = await api.GET('/trips/{trip_id}/statistics', {
    params: { path: { trip_id: tripId }, query },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}
