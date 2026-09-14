import type { components, operations } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import { versionHeaders, writeOutcome } from '@/shared/api/writes'

export type LedgerEntry = components['schemas']['LedgerEntry']
export type LedgerCreate = components['schemas']['LedgerCreate']
export type LedgerPatch = components['schemas']['LedgerPatch']
export type LedgerKind = components['schemas']['LedgerKind']
export type LedgerQuery = NonNullable<operations['listLedgerEntries']['parameters']['query']>

export const ledgerKindLabels: Record<LedgerKind, string> = {
  expense: '支出',
  refund: '退款',
}

function isEntry(value: Record<string, unknown>) {
  return (
    typeof value.id === 'string' &&
    typeof value.version === 'string' &&
    typeof value.kind === 'string' &&
    typeof value.amount === 'string' &&
    typeof value.occurred_on === 'string'
  )
}

export async function listLedgerEntries(tripId: string, query: LedgerQuery = {}) {
  const { data, error } = await api.GET('/trips/{trip_id}/ledger-entries', {
    params: { path: { trip_id: tripId }, query },
  })
  if (error || !data) throw new ApiError(error)
  return data
}

/** 退款表单要挑选可关联的原支出，按页大小上限取完整列表。 */
export async function listAllLedgerEntries(
  tripId: string,
  query: Omit<LedgerQuery, 'limit' | 'cursor'> = {},
): Promise<LedgerEntry[]> {
  const items: LedgerEntry[] = []
  let cursor: string | undefined
  do {
    const page = await listLedgerEntries(tripId, { ...query, limit: 100, cursor })
    items.push(...page.items)
    cursor = page.next_cursor ?? undefined
  } while (cursor)
  return items
}

export async function getLedgerEntry(tripId: string, id: string): Promise<LedgerEntry> {
  const { data, error } = await api.GET('/trips/{trip_id}/ledger-entries/{entry_id}', {
    params: { path: { trip_id: tripId, entry_id: id } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function createLedgerEntry(tripId: string, input: LedgerCreate, operationId: string) {
  const { data, error } = await api.POST('/trips/{trip_id}/ledger-entries', {
    params: { path: { trip_id: tripId }, header: { 'Idempotency-Key': operationId } },
    body: input,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<LedgerEntry>(data.data, isEntry)
}

export async function updateLedgerEntry(
  tripId: string,
  id: string,
  version: string,
  patch: LedgerPatch,
  operationId: string,
) {
  const { data, error } = await api.PATCH('/trips/{trip_id}/ledger-entries/{entry_id}', {
    params: {
      path: { trip_id: tripId, entry_id: id },
      header: versionHeaders(operationId, version),
    },
    body: patch,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<LedgerEntry>(data.data, isEntry)
}

export async function deleteLedgerEntry(
  tripId: string,
  id: string,
  version: string,
  operationId: string,
) {
  const { data, error } = await api.DELETE('/trips/{trip_id}/ledger-entries/{entry_id}', {
    params: {
      path: { trip_id: tripId, entry_id: id },
      header: versionHeaders(operationId, version),
    },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<LedgerEntry>(data.data, isEntry)
}
