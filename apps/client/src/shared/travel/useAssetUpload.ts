import { reactive } from 'vue'
import {
  assetFailureMessage,
  authorizeAssetUpload,
  confirmAssetUpload,
  createAsset,
  getAsset,
  putToObjectStore,
  sha256Hex,
  type Asset,
  type AssetCreate,
  type AssetScope,
  type UploadAuthorization,
} from '@/shared/api/assets'
import { ApiError } from '@/shared/api/auth'
import { randomId } from '@/shared/randomId'

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

export function useAssetUpload(onRegistered?: (assetId: string) => void) {
  const state = reactive<UploadState>({
    phase: 'idle',
    progress: 0,
    assetId: null,
    asset: null,
    error: null,
  })
  let file: File | null = null
  let input: StartUploadInput | null = null
  let creation: { body: AssetCreate; operationId: string } | null = null
  let controller: AbortController | null = null
  let generation = 0
  let running = false
  let confirmed = false
  let confirmKey = ''
  let confirmAttempt = 0
  let registered = false

  function register(assetId: string) {
    state.assetId = assetId
    if (!registered) {
      registered = true
      onRegistered?.(assetId)
    }
  }
  function cancel() {
    generation++
    controller?.abort()
    running = false
    state.phase = 'idle'
    state.progress = 0
  }
  function reset() {
    cancel()
    file = null
    input = null
    creation = null
    registered = false
    confirmed = false
    confirmKey = ''
    confirmAttempt = 0
    Object.assign(state, { phase: 'idle', progress: 0, assetId: null, asset: null, error: null })
  }
  function describe(cause: unknown): string {
    if (cause instanceof DOMException && cause.name === 'AbortError') return '已取消'
    if (cause instanceof ApiError) {
      if (cause.code === 'DEPENDENCY_UNAVAILABLE') return '文件服务暂时不可用，请稍后重试。'
      if (cause.code === 'TRIP_DELETED') return '这趟旅行已进入回收站，不能添加文件。'
    }
    return cause instanceof Error ? cause.message : '上传失败，请重试'
  }
  async function poll(assetId: string, run: number): Promise<Asset | null> {
    const deadline = Date.now() + 60_000
    while (run === generation) {
      const asset = await getAsset(assetId)
      if (run !== generation) return null
      state.asset = asset
      state.phase = asset.status
      if (asset.status === 'ready') {
        state.progress = 1
        state.error = null
        return asset
      }
      if (asset.status === 'failed') {
        confirmed = false
        state.error = assetFailureMessage(asset)
        return asset
      }
      if (Date.now() >= deadline) {
        state.error = '文件仍在处理中，可刷新状态查看。'
        return asset
      }
      await new Promise((resolve) => setTimeout(resolve, 800))
    }
    return null
  }
  async function upload(assetId: string, authorization: UploadAuthorization, run: number) {
    if (!file || run !== generation) return null
    state.phase = 'uploading'
    state.progress = 0
    confirmed = false
    controller = new AbortController()
    await putToObjectStore(authorization, file, {
      signal: controller.signal,
      onProgress: (ratio) => {
        if (run === generation) state.progress = ratio
      },
    })
    if (run !== generation) return null
    state.phase = 'processing'
    // 确认响应丢失时重放同一请求，不重新 PUT 已开始处理的暂存对象。
    confirmKey = randomId()
    confirmAttempt = authorization.upload_attempt
    confirmed = true
    const asset = await confirmAssetUpload(assetId, confirmAttempt, confirmKey)
    if (run !== generation) return null
    if (asset) state.asset = asset
    return poll(assetId, run)
  }
  async function create(run: number) {
    if (!input || !file) return null
    if (!creation) {
      const digest = await sha256Hex(file)
      if (run !== generation) return null
      creation = {
        body: {
          id: randomId(),
          scope: input.scope,
          ...(input.tripId ? { trip_id: input.tripId } : {}),
          original_name: file.name,
          expected_size: file.size,
          declared_media_type: file.type || 'application/octet-stream',
          ...(digest ? { client_sha256: digest } : {}),
        },
        operationId: randomId(),
      }
    }
    const result = await createAsset(creation.body, creation.operationId)
    if (run !== generation) return null
    state.asset = result.asset
    register(creation.body.id)
    if (result.asset?.status === 'ready' || result.asset?.status === 'processing')
      return poll(creation.body.id, run)
    if (!result.authorization) throw new Error('未能取得上传授权，请重试。')
    return upload(creation.body.id, result.authorization, run)
  }
  async function execute(work: (run: number) => Promise<Asset | null>) {
    if (running) return null
    const run = generation
    running = true
    state.phase = 'preparing'
    state.error = null
    try {
      return await work(run)
    } catch (cause) {
      if (run === generation) {
        state.phase = 'failed'
        state.error = describe(cause)
      }
      return null
    } finally {
      if (run === generation) running = false
    }
  }
  async function start(target: StartUploadInput) {
    reset()
    input = target
    file = target.file
    return execute(create)
  }
  async function retry() {
    return execute(async (run) => {
      if (!state.assetId) return create(run)
      const id = state.assetId
      if (confirmed || state.asset?.status === 'processing' || state.asset?.status === 'ready') {
        const current = await getAsset(id)
        if (run !== generation) return null
        state.asset = current
        if (current.status === 'processing' || current.status === 'ready') return poll(id, run)
        if (
          current.status === 'uploading' &&
          confirmed &&
          current.upload_attempt === confirmAttempt &&
          Date.parse(current.upload_expires_at ?? '') > Date.now()
        ) {
          await confirmAssetUpload(id, confirmAttempt, confirmKey)
          if (run !== generation) return null
          return poll(id, run)
        }
      }
      if (!file) throw new Error('请重新选择原文件后重试。')
      const result = await authorizeAssetUpload(id, randomId())
      if (run !== generation) return null
      state.asset = result.asset ?? state.asset
      if (!result.authorization) return poll(id, run)
      return upload(id, result.authorization, run)
    })
  }
  async function resume(asset: Asset, original: File) {
    reset()
    file = original
    state.asset = asset
    register(asset.id)
    return retry()
  }
  async function refresh() {
    if (!state.assetId) return null
    return execute((run) => poll(state.assetId!, run))
  }
  return { state, start, retry, resume, refresh, cancel, reset }
}
