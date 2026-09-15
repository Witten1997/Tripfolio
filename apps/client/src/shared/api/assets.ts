import type { components, operations } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'

export type Asset = components['schemas']['Asset']
export type AssetCreate = components['schemas']['AssetCreate']
export type AssetScope = components['schemas']['AssetScope']
export type AssetStatus = components['schemas']['AssetStatus']
export type ThumbnailStatus = components['schemas']['ThumbnailStatus']
export type UploadAuthorization = components['schemas']['UploadAuthorization']
export type DownloadAuthorization = components['schemas']['DownloadAuthorization']
export type DownloadVariant = components['schemas']['DownloadVariant']
export type AssetQuery = NonNullable<operations['listTripAssets']['parameters']['query']>

/** 资产写响应带内嵌的上传授权；重放时 data 可能为空，但授权仍会重新签发。 */
export interface AssetWriteOutcome {
  asset: Asset | null
  authorization: UploadAuthorization | null
  replayed: boolean
}

function isAsset(value: Record<string, unknown> | null | undefined): value is Asset {
  return (
    !!value &&
    typeof value.id === 'string' &&
    typeof value.status === 'string' &&
    typeof value.upload_attempt === 'number'
  )
}

function toOutcome(data: components['schemas']['AssetWriteResult']): AssetWriteOutcome {
  return {
    asset: isAsset(data.data) ? data.data : null,
    authorization: data.upload_authorization ?? null,
    replayed: data.replayed ?? false,
  }
}

export async function createAsset(
  input: AssetCreate,
  operationId: string,
): Promise<AssetWriteOutcome> {
  const { data, error } = await api.POST('/assets', {
    params: { header: { 'Idempotency-Key': operationId } },
    body: input,
  })
  if (error || !data) throw new ApiError(error)
  return toOutcome(data.data)
}

/** 续签当前尝试，或在失败/过期后开启新尝试。 */
export async function authorizeAssetUpload(
  assetId: string,
  operationId: string,
): Promise<AssetWriteOutcome> {
  const { data, error } = await api.POST('/assets/{asset_id}/upload-authorization', {
    params: { path: { asset_id: assetId }, header: { 'Idempotency-Key': operationId } },
  })
  if (error || !data) throw new ApiError(error)
  return toOutcome(data.data)
}

export async function confirmAssetUpload(
  assetId: string,
  uploadAttempt: number,
  operationId: string,
): Promise<Asset | null> {
  const { data, error } = await api.POST('/assets/{asset_id}/confirm', {
    params: { path: { asset_id: assetId }, header: { 'Idempotency-Key': operationId } },
    body: { upload_attempt: uploadAttempt },
  })
  if (error || !data) throw new ApiError(error)
  return isAsset(data.data.data) ? data.data.data : null
}

export async function getAsset(assetId: string): Promise<Asset> {
  const { data, error } = await api.GET('/assets/{asset_id}', {
    params: { path: { asset_id: assetId } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function listTripAssets(tripId: string, query: AssetQuery = {}) {
  const { data, error } = await api.GET('/trips/{trip_id}/assets', {
    params: { path: { trip_id: tripId }, query },
  })
  if (error || !data) throw new ApiError(error)
  return data
}

export async function authorizeAssetDownload(
  assetId: string,
  variant: DownloadVariant = 'original',
  disposition: 'inline' | 'attachment' = 'inline',
): Promise<DownloadAuthorization> {
  const { data, error } = await api.GET('/assets/{asset_id}/download', {
    params: { path: { asset_id: assetId }, query: { variant, disposition } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

/** 批量授权：相册按页取缩略图地址时用，一次最多 100 个。 */
export async function authorizeAssetDownloads(
  items: { asset_id: string; variant?: DownloadVariant }[],
  disposition: 'inline' | 'attachment' = 'inline',
): Promise<DownloadAuthorization[]> {
  const { data, error } = await api.POST('/assets/download-authorizations', {
    // variant 在契约里有默认值因而是必填；这里补齐默认再发送。
    body: {
      items: items.map((item) => ({
        asset_id: item.asset_id,
        variant: item.variant ?? 'original',
      })),
      disposition,
    },
  })
  if (error || !data) throw new ApiError(error)
  return data.data.items
}

/**
 * 按授权直传对象存储。用 XMLHttpRequest 而不是 fetch：只有它能报告上传进度。
 * required_headers 必须原样发送，否则存储端拒绝签名。
 */
export function putToObjectStore(
  authorization: UploadAuthorization,
  file: Blob,
  options: { onProgress?: (ratio: number) => void; signal?: AbortSignal } = {},
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open(authorization.method, authorization.url, true)
    for (const [name, value] of Object.entries(authorization.required_headers)) {
      // 浏览器禁止脚本设置 Content-Length，它由 body 自动决定，跳过即可。
      if (name.toLowerCase() === 'content-length') continue
      xhr.setRequestHeader(name, value)
    }
    if (options.onProgress) {
      xhr.upload.addEventListener('progress', (event) => {
        if (event.lengthComputable) options.onProgress?.(event.loaded / event.total)
      })
    }
    xhr.addEventListener('load', () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        options.onProgress?.(1)
        resolve()
      } else {
        reject(new Error(`直传失败：存储返回 ${xhr.status}`))
      }
    })
    xhr.addEventListener('error', () => reject(new Error('直传失败：网络错误')))
    xhr.addEventListener('abort', () => reject(new DOMException('已取消', 'AbortError')))
    options.signal?.addEventListener('abort', () => xhr.abort(), { once: true })
    xhr.send(file)
  })
}

/** 计算文件的 SHA-256，作为客户端摘要交服务端校验；不可用时返回 undefined。 */
export async function sha256Hex(file: Blob): Promise<string | undefined> {
  if (!crypto.subtle) return undefined
  try {
    const digest = await crypto.subtle.digest('SHA-256', await file.arrayBuffer())
    return Array.from(new Uint8Array(digest))
      .map((byte) => byte.toString(16).padStart(2, '0'))
      .join('')
  } catch {
    // 非安全上下文（http 且非 localhost）没有 subtle；跳过摘要不影响上传。
    return undefined
  }
}

export const assetStatusLabels: Record<AssetStatus, string> = {
  uploading: '上传中',
  processing: '处理中',
  ready: '已完成',
  failed: '失败',
}

/** worker 写回的失败代码转为可读文案；未登记的代码原样显示以便排查。 */
const failureLabels: Record<string, string> = {
  UPLOAD_MISSING: '没有收到文件内容，请重新上传。',
  UPLOAD_MODIFIED: '文件在上传过程中发生变化，请重新上传。',
  CHECKSUM_MISMATCH: '文件在传输中损坏，请重新上传。',
  UNSUPPORTED_MEDIA_TYPE: '文件类型不支持，请上传 JPEG、PNG、WebP 图片或 PDF。',
  FILE_TOO_LARGE: '文件超出大小限制。',
  IMAGE_TOO_LARGE: '图片尺寸过大，无法处理。',
  CORRUPT_IMAGE: '图片已损坏，无法读取。',
  PROCESSING_FAILED: '文件处理失败，请重新上传。',
}

export function assetFailureMessage(asset: Asset): string {
  if (!asset.error_code) return '文件处理失败，请重新上传。'
  return failureLabels[asset.error_code] ?? asset.error_code
}
