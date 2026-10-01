import type { components, operations } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'

export type DashboardSnapshot = components['schemas']['DashboardSnapshot']
export type DashboardTrip = components['schemas']['DashboardTrip']
export type DashboardPlace = components['schemas']['DashboardPlace']
export type DashboardQuery = NonNullable<operations['getDashboard']['parameters']['query']>

export async function getDashboard(query: DashboardQuery, signal?: AbortSignal) {
  const { data, error } = await api.GET('/dashboard', { params: { query }, signal })
  if (error || !data) throw new ApiError(error)
  return data.data
}
