import createClient from 'openapi-fetch'
import type { components, paths } from '@tripfolio/contracts/openapi/v1'

import { resolveApiBaseUrl } from './baseUrl'
import { api } from './client'
import { ApiError } from './problem'
import { versionHeaders } from './writes'

export type DeletionJob = components['schemas']['DeletionJob']
export type DeletionReceipt = components['schemas']['DeletionReceipt']

// 只读通道不安装普通会话中间件，不带 Cookie，也不会在 401 后刷新或替换 Deletion 授权。
const receiptClient = createClient<paths>({
  baseUrl: resolveApiBaseUrl(undefined, import.meta.env.VITE_API_BASE_URL),
  credentials: 'omit',
  cache: 'no-store',
})

export async function getDeletionJob(id: string): Promise<DeletionJob> {
  const { data, error } = await api.GET('/deletion-jobs/{job_id}', {
    params: { path: { job_id: id } },
    cache: 'no-store',
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function getTripDeletionJob(tripId: string): Promise<DeletionJob> {
  const { data, error } = await api.GET('/recycle-bin/trips/{trip_id}/deletion-job', {
    params: { path: { trip_id: tripId } },
    cache: 'no-store',
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function retryDeletionJob(id: string, version: string, operationId: string) {
  const { data, error } = await api.POST('/deletion-jobs/{job_id}/retry', {
    params: { path: { job_id: id }, header: versionHeaders(operationId, version) },
    body: { confirm: true },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function requestAccountDeletion(version: string, operationId: string) {
  const { data, error } = await api.POST('/account/deletion', {
    params: { header: versionHeaders(operationId, version) },
    body: { confirm: true },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function renewDeletionReceipt(id: string): Promise<DeletionReceipt> {
  const { data, error } = await api.POST('/account/deletion/{job_id}/receipt', {
    params: { path: { job_id: id } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function getAccountDeletion(receipt: DeletionReceipt): Promise<DeletionJob> {
  const { data, error } = await receiptClient.GET('/account/deletion/{job_id}', {
    params: { path: { job_id: receipt.job_id } },
    headers: { Authorization: `Deletion ${receipt.receipt_token}` },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}
