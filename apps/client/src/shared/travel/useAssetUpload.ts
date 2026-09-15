import { reactive, ref } from 'vue'

import {
  assetFailureMessage,
  authorizeAssetUpload,
  confirmAssetUpload,
  createAsset,
  getAsset,
  putToObjectStore,
  sha256Hex,
  type Asset,
  type AssetScope,
  type UploadAuthorization,
} from '@/shared/api/assets'
import { ApiError } from '@/shared/api/auth'
import { randomId } from '@/shared/randomId'

/** 一次上传的界面状态。progress 是 0–1；只有 uploading 阶段有意义。 */
export type UploadPhase = 'idle' | 'preparing' | 'uploading' | 'processing' | 'ready' | 'failed'

export interface UploadState {
  phase: UploadPhase
  progress: number
  assetId: string | null
  asset: Asset | null
  error: string | null
}

export interface StartUploadInput {
  file: File
  scope: AssetScope
  tripId?: string
}

/** 轮询 worker 处理结果的间隔与上限：校验通常在一秒内完成，缩略图稍慢。 */
const POLL_INTERVAL_MS = 800
const POLL_TIMEOUT_MS = 60_000

/**
 * 驱动完整上传链路：登记资产 → 直传对象存储 → 确认 → 轮询处理结果。
 *
 * 关键点：
 *   - 服务端不信任「上传成功」声明，确认后必须等 worker 校验，因此要轮询到 ready/failed。
 *   - 直传或确认失败后不重建资产：调用 retry 用 upload-authorization 开新尝试，
 *     资产 ID 与已建立的引用（照片说明、票据关联）都保持不变。
 */
export function useAssetUpload() {
  const state = reactive<UploadState>({
    phase: 'idle',
    progress: 0,
    assetId: null,
    asset: null,
    error: null,
  })
  const pending = ref<{ file: File; scope: AssetScope; tripId?: string } | null>(null)
  let controller: AbortController | null = null
  let cancelled = false

  function reset() {
    controller?.abort()
    controller = null
    cancelled = false
    pending.value = null
    state.phase = 'idle'
    state.progress = 0
    state.assetId = null
    state.asset = null
    state.error = null
  }

  function cancel() {
    cancelled = true
    controller?.abort()
    state.phase = 'idle'
    state.progress = 0
  }

  async function runUpload(assetId: string, authorization: UploadAuthorization, file: File) {
    state.phase = 'uploading'
    state.progress = 0
    controller = new AbortController()
    await putToObjectStore(authorization, file, {
      signal: controller.signal,
      onProgress: (ratio) => {
        state.progress = ratio
      },
    })
    if (cancelled) return null

    state.phase = 'processing'
    await confirmAssetUpload(assetId, authorization.upload_attempt, randomId())
    return pollUntilSettled(assetId)
  }

  /** 轮询到 ready 或 failed；超时不改状态，交由界面提示用户稍后查看。 */
  async function pollUntilSettled(assetId: string): Promise<Asset | null> {
    const deadline = Date.now() + POLL_TIMEOUT_MS
    for (;;) {
      if (cancelled) return null
      const asset = await getAsset(assetId)
      state.asset = asset
      if (asset.status === 'ready') {
        // 缩略图可能还在生成；原图已可用，不再等待。
        state.phase = 'ready'
        state.progress = 1
        return asset
      }
      if (asset.status === 'failed') {
        state.phase = 'failed'
        state.error = assetFailureMessage(asset)
        return asset
      }
      if (Date.now() > deadline) {
        state.error = '文件仍在处理中，请稍后刷新查看。'
        return asset
      }
      await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS))
    }
  }

  function describe(cause: unknown): string {
    if (cause instanceof DOMException && cause.name === 'AbortError') return '已取消'
    if (cause instanceof ApiError) {
      switch (cause.code) {
        case 'REQUEST_TOO_LARGE':
          return cause.message || '文件超出大小限制。'
        case 'UNSUPPORTED_MEDIA_TYPE':
          return cause.message || '文件类型不支持。'
        case 'DEPENDENCY_UNAVAILABLE':
          return '文件服务暂时不可用，请稍后重试。'
        case 'TRIP_DELETED':
          return '这趟旅行已进入回收站，不能添加文件。'
        default:
          return cause.message
      }
    }
    return cause instanceof Error ? cause.message : '上传失败，请重试'
  }

  /** 开始一次上传。返回最终资产（ready 或 failed）；取消或出错时返回 null。 */
  async function start(input: StartUploadInput): Promise<Asset | null> {
    reset()
    pending.value = { file: input.file, scope: input.scope, tripId: input.tripId }
    state.phase = 'preparing'
    try {
      const assetId = randomId()
      const digest = await sha256Hex(input.file)
      const { authorization, asset } = await createAsset(
        {
          id: assetId,
          scope: input.scope,
          ...(input.tripId ? { trip_id: input.tripId } : {}),
          original_name: input.file.name,
          expected_size: input.file.size,
          declared_media_type: input.file.type || 'application/octet-stream',
          ...(digest ? { client_sha256: digest } : {}),
        },
        randomId(),
      )
      state.assetId = assetId
      state.asset = asset
      if (!authorization) {
        state.phase = 'failed'
        state.error = '未能取得上传授权，请重试。'
        return null
      }
      return await runUpload(assetId, authorization, input.file)
    } catch (cause) {
      state.phase = cancelled ? 'idle' : 'failed'
      state.error = cancelled ? null : describe(cause)
      return null
    }
  }

  /** 失败后重试：复用同一资产开新尝试，不重建资产也不丢已建立的引用。 */
  async function retry(): Promise<Asset | null> {
    const target = pending.value
    if (!target || !state.assetId) return null
    cancelled = false
    state.error = null
    state.phase = 'preparing'
    try {
      const { authorization } = await authorizeAssetUpload(state.assetId, randomId())
      if (!authorization) {
        state.phase = 'failed'
        state.error = '这个文件已不能重新上传，请重新选择文件。'
        return null
      }
      return await runUpload(state.assetId, authorization, target.file)
    } catch (cause) {
      state.phase = cancelled ? 'idle' : 'failed'
      state.error = cancelled ? null : describe(cause)
      return null
    }
  }

  return { state, start, retry, cancel, reset }
}
