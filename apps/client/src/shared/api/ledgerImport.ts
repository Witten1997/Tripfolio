import type { components } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import type { CollectionBaseline } from '@/shared/api/collectionGuards'
import { captureMembersBaseline } from '@/shared/api/members'
import type { WriteResult } from '@/shared/api/writes'

export type LedgerImportPreview = components['schemas']['LedgerImportPreview']
type DeepReadonly<T> = T extends object ? { readonly [P in keyof T]: DeepReadonly<T[P]> } : T
export interface PreparedLedgerImport {
  readonly tripId: string
  readonly file: File
  readonly preview: DeepReadonly<LedgerImportPreview>
  readonly baseline: CollectionBaseline
}

function freeze<T>(value: T): DeepReadonly<T> {
  if (value !== null && typeof value === 'object') {
    for (const child of Object.values(value)) freeze(child)
    Object.freeze(value)
  }
  return value as DeepReadonly<T>
}

/** 文件与摘要、分摊预览及成员基线只能取自这一次上传。 */
export async function prepareLedgerImport(
  tripId: string,
  file: File,
  signal?: AbortSignal,
): Promise<PreparedLedgerImport> {
  const source = await previewLedgerImport(tripId, file, signal)
  signal?.throwIfAborted()
  if (
    !source ||
    !/^[a-f0-9]{64}$/.test(source.digest) ||
    !Array.isArray(source.rows) ||
    !Array.isArray(source.errors) ||
    !Array.isArray(source.warnings) ||
    !Number.isInteger(source.count) ||
    source.count < 0 ||
    source.count > 1000 ||
    source.count !== source.rows.length ||
    !Number.isInteger(source.valid_count) ||
    source.valid_count < 0 ||
    source.valid_count > source.count ||
    (source.errors.length === 0 && source.valid_count !== source.count) ||
    typeof source.total_amount !== 'string' ||
    typeof source.currency_code !== 'string' ||
    source.warnings.some((warning) => typeof warning !== 'string') ||
    source.errors.some(
      (error) =>
        !error ||
        !Number.isInteger(error.row) ||
        typeof error.column !== 'string' ||
        typeof error.message !== 'string',
    ) ||
    source.rows.some(
      (row) =>
        !row ||
        !Number.isInteger(row.row) ||
        ['amount', 'category', 'split_mode', 'occurred_on', 'notes', 'payer'].some(
          (key) => typeof Reflect.get(row, key) !== 'string',
        ) ||
        !Array.isArray(row.participants) ||
        row.participants.some((member) => typeof member !== 'string') ||
        !Array.isArray(row.splits) ||
        row.splits.some(
          (share) => !share || typeof share.member !== 'string' || typeof share.amount !== 'string',
        ),
    )
  )
    throw new Error('导入预览不完整，请保留文件并重新预览。')
  const preview = freeze(structuredClone(source))
  const baseline = captureMembersBaseline(tripId, preview.scope_revisions)
  return Object.freeze({ tripId, file, preview, baseline })
}

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
  signal?: AbortSignal,
): Promise<LedgerImportPreview> {
  const { data, error } = await api.POST('/trips/{trip_id}/ledger-import-preview', {
    params: { path: { trip_id: tripId } },
    headers: { 'Content-Type': 'application/octet-stream' },
    body: '',
    bodySerializer: () => file,
    signal,
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function commitLedgerImport(
  tripId: string,
  file: File,
  digest: string,
  operationId: string,
  baseline?: CollectionBaseline,
): Promise<WriteResult> {
  if (
    baseline &&
    (baseline.guards.length !== 1 ||
      baseline.guards[0]?.kind !== 'members' ||
      baseline.guards[0]?.scope_id !== tripId)
  )
    throw new Error('导入成员基线与本次旅行不匹配。')
  const { data, error } = await api.POST('/trips/{trip_id}/ledger-import', {
    params: {
      path: { trip_id: tripId },
      query: { preview_digest: digest },
      header: { 'Idempotency-Key': operationId },
    },
    headers: { 'Content-Type': 'application/octet-stream', ...baseline?.headers },
    body: '',
    bodySerializer: () => file,
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}
