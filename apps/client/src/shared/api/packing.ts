import type { components, operations } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import { versionHeaders, writeOutcome } from '@/shared/api/writes'

export type PackingItem = components['schemas']['PackingItem']
export type PackingCreate = components['schemas']['PackingCreate']
export type PackingPatch = components['schemas']['PackingPatch']
export type PackingCategory = components['schemas']['PackingCategory']
export type PackingStatus = components['schemas']['PackingStatus']
export type PreparedPackingStatus = Exclude<PackingStatus, 'packed'>
export type PackingBatchCreate = components['schemas']['PackingBatchCreate']
export type PackingBatchResult = components['schemas']['PackingBatchResult']
export type PackingLibrary = components['schemas']['PackingLibrary']
export type PackingQuery = NonNullable<operations['listPackingItems']['parameters']['query']>

export const packingCategoryLabels: Record<PackingCategory, string> = {
  documents: '证件',
  electronics: '数码产品',
  clothing: '服装',
  daily: '生活用品',
  food: '食品饮料',
  medicine: '药品',
  other: '其他',
}

export const packingCategoryOrder: PackingCategory[] = [
  'documents',
  'electronics',
  'clothing',
  'daily',
  'food',
  'medicine',
  'other',
]

export const packingStatusLabels: Record<PackingStatus, string> = {
  pending: '待准备',
  ready: '已准备',
  packed: '已准备',
}

export const packingStatusOrder: PreparedPackingStatus[] = ['pending', 'ready']

/** 兼容历史三态，不修改服务器保存的历史值。 */
export function normalizePackingStatus(status: PackingStatus): PreparedPackingStatus {
  return status === 'pending' ? 'pending' : 'ready'
}

function isItem(value: Record<string, unknown>) {
  return (
    typeof value.id === 'string' &&
    typeof value.version === 'string' &&
    typeof value.name === 'string' &&
    typeof value.category === 'string'
  )
}

export async function listPackingItems(tripId: string, query: PackingQuery = {}) {
  const { data, error } = await api.GET('/trips/{trip_id}/packing-items', {
    params: { path: { trip_id: tripId }, query },
  })
  if (error || !data) throw new ApiError(error)
  return data
}

export async function listAllPackingItems(tripId: string): Promise<PackingItem[]> {
  const items: PackingItem[] = []
  let cursor: string | undefined
  do {
    const page = await listPackingItems(tripId, { limit: 100, cursor })
    items.push(...page.items)
    cursor = page.next_cursor ?? undefined
  } while (cursor)
  return items
}

export async function getPackingItem(tripId: string, id: string): Promise<PackingItem> {
  const { data, error } = await api.GET('/trips/{trip_id}/packing-items/{item_id}', {
    params: { path: { trip_id: tripId, item_id: id } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function getPackingLibrary(): Promise<PackingLibrary> {
  const { data, error } = await api.GET('/packing-library')
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function createPackingItem(tripId: string, input: PackingCreate, operationId: string) {
  const { data, error } = await api.POST('/trips/{trip_id}/packing-items', {
    params: { path: { trip_id: tripId }, header: { 'Idempotency-Key': operationId } },
    body: input,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<PackingItem>(data.data, isItem)
}

export async function createPackingItems(
  tripId: string,
  input: PackingBatchCreate,
  operationId: string,
): Promise<PackingBatchResult> {
  const { data, error } = await api.POST('/trips/{trip_id}/packing-items/batch', {
    params: { path: { trip_id: tripId }, header: { 'Idempotency-Key': operationId } },
    body: input,
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function updatePackingItem(
  tripId: string,
  id: string,
  version: string,
  patch: PackingPatch,
  operationId: string,
) {
  const { data, error } = await api.PATCH('/trips/{trip_id}/packing-items/{item_id}', {
    params: {
      path: { trip_id: tripId, item_id: id },
      header: versionHeaders(operationId, version),
    },
    body: patch,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<PackingItem>(data.data, isItem)
}

export async function deletePackingItem(
  tripId: string,
  id: string,
  version: string,
  operationId: string,
) {
  const { data, error } = await api.DELETE('/trips/{trip_id}/packing-items/{item_id}', {
    params: {
      path: { trip_id: tripId, item_id: id },
      header: versionHeaders(operationId, version),
    },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<PackingItem>(data.data, isItem)
}
