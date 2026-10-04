import type { components, operations } from '@tripfolio/contracts/openapi/v1'
import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import { versionHeaders, writeOutcome } from '@/shared/api/writes'

export type Document = components['schemas']['Document']
export type DocumentCreate = components['schemas']['DocumentCreate']
export type DocumentPatch = components['schemas']['DocumentPatch']
export type DocumentQuery = NonNullable<operations['listDocuments']['parameters']['query']>

export async function listDocuments(tripId: string, query: DocumentQuery = {}) {
  const { data, error } = await api.GET('/trips/{trip_id}/documents', {
    params: { path: { trip_id: tripId }, query },
  })
  if (error || !data) throw new ApiError(error)
  return data
}
export async function getDocument(tripId: string, id: string): Promise<Document> {
  const { data, error } = await api.GET('/trips/{trip_id}/documents/{document_id}', {
    params: { path: { trip_id: tripId, document_id: id } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}
const isResource = (v: Record<string, unknown>) =>
  typeof v.id === 'string' && typeof v.version === 'string'
export async function createDocument(tripId: string, body: DocumentCreate, operationId: string) {
  const { data, error } = await api.POST('/trips/{trip_id}/documents', {
    params: { path: { trip_id: tripId }, header: { 'Idempotency-Key': operationId } },
    body,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Document>(data.data, isResource)
}
export async function updateDocument(
  tripId: string,
  id: string,
  version: string,
  body: DocumentPatch,
  operationId: string,
) {
  const { data, error } = await api.PATCH('/trips/{trip_id}/documents/{document_id}', {
    params: {
      path: { trip_id: tripId, document_id: id },
      header: versionHeaders(operationId, version),
    },
    body,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Document>(data.data, isResource)
}
export async function deleteDocument(
  tripId: string,
  id: string,
  version: string,
  operationId: string,
) {
  const { data, error } = await api.DELETE('/trips/{trip_id}/documents/{document_id}', {
    params: {
      path: { trip_id: tripId, document_id: id },
      header: versionHeaders(operationId, version),
    },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Document>(data.data, isResource)
}
