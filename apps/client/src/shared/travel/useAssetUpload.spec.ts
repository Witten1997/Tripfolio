import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { Asset, UploadAuthorization } from '@/shared/api/assets'
import { json, problem } from '@/shared/travel/__tests__/fixtures'
import { useAssetUpload } from '@/shared/travel/useAssetUpload'

const { transport, putMock } = vi.hoisted(() => ({
  transport: vi.fn<(request: Request) => Promise<Response>>(),
  putMock: vi.fn<() => Promise<void>>(),
}))

vi.mock('@/shared/api/client', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/shared/api/client')>()
  return {
    ...original,
    api: original.createApiClient({
      baseUrl: 'http://api.test/api/v1',
      fetch: transport,
      refresh: async () => false,
    }),
  }
})

// 直传与摘要都不适合在 jsdom 里真跑：前者需要对象存储，后者需要安全上下文。
vi.mock('@/shared/api/assets', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/shared/api/assets')>()
  return {
    ...original,
    putToObjectStore: (
      _auth: UploadAuthorization,
      _file: Blob,
      options?: { onProgress?: (r: number) => void },
    ) => {
      options?.onProgress?.(0.5)
      options?.onProgress?.(1)
      return putMock()
    },
    sha256Hex: async () => 'a'.repeat(64),
  }
})

function asset(overrides: Partial<Asset> = {}): Asset {
  return {
    id: 'asset-1',
    trip_id: null,
    scope: 'avatar',
    original_name: 'me.jpg',
    status: 'uploading',
    upload_attempt: 1,
    upload_expires_at: '2026-09-14T09:30:00Z',
    media_type: null,
    byte_size: null,
    sha256: null,
    width: null,
    height: null,
    exif_taken_at_local: null,
    exif_latitude: null,
    exif_longitude: null,
    thumbnail_status: 'none',
    error_code: null,
    version: '1',
    created_at: '2026-09-14T09:00:00Z',
    updated_at: '2026-09-14T09:00:00Z',
    deleted_at: null,
    ...overrides,
  }
}

function authorization(attempt = 1): UploadAuthorization {
  return {
    upload_attempt: attempt,
    method: 'PUT',
    url: `https://store.test/staging/${attempt}`,
    required_headers: { 'Content-Type': 'image/jpeg' },
    expires_at: '2026-09-14T09:30:00Z',
  }
}

function writeResult(data: Asset | null, auth: UploadAuthorization | null, replayed = false) {
  return {
    data: {
      operation_id: crypto.randomUUID(),
      primary: { type: 'asset', id: data?.id ?? 'asset-1', version: data?.version ?? '1' },
      affected: [],
      commit_cursor: null,
      warnings: [],
      replayed,
      data,
      upload_authorization: auth,
    },
  }
}

const file = new File([new Uint8Array([1, 2, 3])], 'me.jpg', { type: 'image/jpeg' })

describe('useAssetUpload', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    transport.mockReset()
    putMock.mockReset()
    putMock.mockResolvedValue(undefined)
    vi.useFakeTimers({ shouldAdvanceTime: true })
  })

  it('按登记→直传→确认→轮询的顺序推进，ready 后返回资产', async () => {
    const calls: string[] = []
    transport.mockImplementation(async (request) => {
      const path = new URL(request.url).pathname
      calls.push(`${request.method} ${path}`)
      if (path === '/api/v1/assets') return json(201, writeResult(asset(), authorization()))
      if (path.endsWith('/confirm')) {
        return json(202, writeResult(asset({ status: 'processing' }), null))
      }
      // 第一次 GET 仍在处理，第二次就绪：证明确实轮询而不是只读一次。
      const polls = calls.filter((c) => c.startsWith('GET /api/v1/assets/')).length
      return json(200, {
        data:
          polls === 1
            ? asset({ status: 'processing' })
            : asset({ status: 'ready', thumbnail_status: 'ready', media_type: 'image/jpeg' }),
      })
    })

    const upload = useAssetUpload()
    const promise = upload.start({ file, scope: 'avatar' })
    await vi.advanceTimersByTimeAsync(2000)
    const result = await promise

    expect(result?.status).toBe('ready')
    expect(upload.state.phase).toBe('ready')
    expect(upload.state.progress).toBe(1)
    expect(upload.state.error).toBeNull()
    // 资产 ID 由客户端生成（契约要求），断言路径形状而不是固定 ID。
    const assetId = upload.state.assetId
    expect(assetId).toMatch(/^[0-9a-f-]{36}$/)
    // 顺序固定：创建在直传前，确认在直传后，之后才轮询。
    expect(calls[0]).toBe('POST /api/v1/assets')
    expect(calls[1]).toBe(`POST /api/v1/assets/${assetId}/confirm`)
    expect(calls.slice(2).every((c) => c === `GET /api/v1/assets/${assetId}`)).toBe(true)
    expect(calls.slice(2).length).toBeGreaterThanOrEqual(2)
    expect(putMock).toHaveBeenCalledOnce()
  })

  it('创建时带上声明信息与客户端摘要', async () => {
    let body: Record<string, unknown> = {}
    transport.mockImplementation(async (request) => {
      const path = new URL(request.url).pathname
      if (path === '/api/v1/assets') {
        body = (await request.json()) as Record<string, unknown>
        return json(201, writeResult(asset(), authorization()))
      }
      if (path.endsWith('/confirm'))
        return json(202, writeResult(asset({ status: 'processing' }), null))
      return json(200, { data: asset({ status: 'ready' }) })
    })

    const upload = useAssetUpload()
    const promise = upload.start({ file, scope: 'trip', tripId: 'trip-9' })
    await vi.advanceTimersByTimeAsync(2000)
    await promise

    expect(body.scope).toBe('trip')
    expect(body.trip_id).toBe('trip-9')
    expect(body.original_name).toBe('me.jpg')
    expect(body.expected_size).toBe(file.size)
    expect(body.declared_media_type).toBe('image/jpeg')
    expect(body.client_sha256).toBe('a'.repeat(64))
  })

  it('worker 判定失败时给出可读原因，不把原始代码抛给用户', async () => {
    transport.mockImplementation(async (request) => {
      const path = new URL(request.url).pathname
      if (path === '/api/v1/assets') return json(201, writeResult(asset(), authorization()))
      if (path.endsWith('/confirm'))
        return json(202, writeResult(asset({ status: 'processing' }), null))
      return json(200, { data: asset({ status: 'failed', error_code: 'CHECKSUM_MISMATCH' }) })
    })

    const upload = useAssetUpload()
    const promise = upload.start({ file, scope: 'avatar' })
    await vi.advanceTimersByTimeAsync(2000)
    const result = await promise

    expect(result?.status).toBe('failed')
    expect(upload.state.phase).toBe('failed')
    expect(upload.state.error).toBe('文件在传输中损坏，请重新上传。')
    expect(upload.state.error).not.toContain('CHECKSUM_MISMATCH')
  })

  it('重试复用同一资产开新尝试，不重新创建资产', async () => {
    const calls: string[] = []
    let failFirst = true
    transport.mockImplementation(async (request) => {
      const path = new URL(request.url).pathname
      calls.push(`${request.method} ${path}`)
      if (path === '/api/v1/assets') return json(201, writeResult(asset(), authorization(1)))
      if (path.endsWith('/upload-authorization')) {
        return json(200, writeResult(asset({ upload_attempt: 2 }), authorization(2)))
      }
      if (path.endsWith('/confirm'))
        return json(202, writeResult(asset({ status: 'processing' }), null))
      if (failFirst) {
        failFirst = false
        return json(200, { data: asset({ status: 'failed', error_code: 'UPLOAD_MISSING' }) })
      }
      return json(200, { data: asset({ status: 'ready', upload_attempt: 2 }) })
    })

    const upload = useAssetUpload()
    let promise = upload.start({ file, scope: 'avatar' })
    await vi.advanceTimersByTimeAsync(2000)
    await promise
    expect(upload.state.phase).toBe('failed')

    promise = upload.retry()
    await vi.advanceTimersByTimeAsync(2000)
    const result = await promise

    expect(result?.status).toBe('ready')
    // 只创建过一次资产；重试走 upload-authorization。
    expect(calls.filter((c) => c === 'POST /api/v1/assets')).toHaveLength(1)
    expect(calls.filter((c) => c.endsWith('/upload-authorization'))).toHaveLength(1)
    // 确认用的是新尝试序号。
    const confirmBodies = calls.filter((c) => c.endsWith('/confirm'))
    expect(confirmBodies).toHaveLength(2)
  })

  it('创建被拒时映射为可读文案并停在 failed', async () => {
    transport.mockImplementation(async () =>
      problem('REQUEST_TOO_LARGE', 413, { detail: 'image/jpeg 最大 20 MiB' }),
    )

    const upload = useAssetUpload()
    const result = await upload.start({ file, scope: 'avatar' })

    expect(result).toBeNull()
    expect(upload.state.phase).toBe('failed')
    expect(upload.state.error).toContain('20 MiB')
    expect(putMock).not.toHaveBeenCalled()
  })

  it('对象存储未配置时提示稍后重试', async () => {
    transport.mockImplementation(async () => problem('DEPENDENCY_UNAVAILABLE', 503))

    const upload = useAssetUpload()
    await upload.start({ file, scope: 'avatar' })

    expect(upload.state.error).toBe('文件服务暂时不可用，请稍后重试。')
  })

  it('直传失败停在 failed 且不发确认请求', async () => {
    const calls: string[] = []
    transport.mockImplementation(async (request) => {
      const path = new URL(request.url).pathname
      calls.push(path)
      return json(201, writeResult(asset(), authorization()))
    })
    putMock.mockRejectedValue(new Error('直传失败：存储返回 403'))

    const upload = useAssetUpload()
    const result = await upload.start({ file, scope: 'avatar' })

    expect(result).toBeNull()
    expect(upload.state.phase).toBe('failed')
    expect(upload.state.error).toContain('403')
    expect(calls.some((p) => p.endsWith('/confirm'))).toBe(false)
  })

  it('reset 清空状态，取消后不留错误', async () => {
    transport.mockImplementation(async () => json(201, writeResult(asset(), authorization())))
    putMock.mockRejectedValue(new DOMException('已取消', 'AbortError'))

    const upload = useAssetUpload()
    await upload.start({ file, scope: 'avatar' })
    expect(upload.state.phase).toBe('failed')
    expect(upload.state.error).toBe('已取消')

    upload.reset()
    expect(upload.state.phase).toBe('idle')
    expect(upload.state.assetId).toBeNull()
    expect(upload.state.error).toBeNull()
    expect(upload.state.progress).toBe(0)
  })
})
