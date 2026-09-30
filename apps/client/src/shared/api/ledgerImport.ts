import type { components } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'

export type LedgerImportPreview = components['schemas']['LedgerImportPreview']

export async function downloadLedgerTemplate(tripId: string): Promise<Blob> {
  const { data, error } = await api.GET('/trips/{trip_id}/ledger-import-template', {
    params: { path: { trip_id: tripId } },
    parseAs: 'blob',
  })
  if (error || !data) throw new ApiError(error)
  return data
}

export async function previewLedgerImport(
  tripId: string,
  file: File,
): Promise<LedgerImportPreview> {
  const { data, error } = await api.POST('/trips/{trip_id}/ledger-import-preview', {
    params: { path: { trip_id: tripId } },
    headers: { 'Content-Type': 'application/octet-stream' },
    body: '',
    bodySerializer: () => file,
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function commitLedgerImport(
  tripId: string,
  file: File,
  digest: string,
  operationId: string,
) {
  const { data, error } = await api.POST('/trips/{trip_id}/ledger-import', {
    params: {
      path: { trip_id: tripId },
      query: { preview_digest: digest },
      header: { 'Idempotency-Key': operationId },
    },
    headers: { 'Content-Type': 'application/octet-stream' },
    body: '',
    bodySerializer: () => file,
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}
