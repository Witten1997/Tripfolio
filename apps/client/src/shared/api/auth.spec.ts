import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api, createApiClient } from './client'
import { readCsrfCookie, refreshSession, register, restoreSession } from './auth'
import { clearDeletion, saveDeletion } from '@/shared/deletion/storage'
import { useSessionStore, type AuthResult } from '@/shared/stores/session'

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

/** 去掉 crypto.randomUUID，模拟非安全上下文（纯 http 自托管）里的浏览器。 */
function withoutRandomUUID() {
  // jsdom 里 randomUUID 挂在 crypto 实例自己身上，所以直接遮蔽实例属性而不是原型。
  const own = Object.getOwnPropertyDescriptor(crypto, 'randomUUID')
  const proto = own ? null : Object.getOwnPropertyDescriptor(Crypto.prototype, 'randomUUID')
  Object.defineProperty(crypto, 'randomUUID', { configurable: true, value: undefined })
  return () => {
    delete (crypto as { randomUUID?: unknown }).randomUUID
    if (own) Object.defineProperty(crypto, 'randomUUID', own)
    else if (proto) Object.defineProperty(Crypto.prototype, 'randomUUID', proto)
  }
}

const authResult: AuthResult = {
  access_token: 'fresh',
  token_type: 'Bearer',
  expires_in_seconds: 900,
  csrf_token: 'csrf-2',
  account: {
    id: 'acc',
    email: 'a@example.com',
    nickname: '甲',
    avatar_asset_id: null,
    default_timezone: 'Asia/Shanghai',
    status: 'active',
    version: '1',
    created_at: '2026-09-12T00:00:00Z',
    updated_at: '2026-09-12T00:00:00Z',
  },
}

describe('refreshSession', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    clearDeletion()
    document.cookie = 'tripfolio_csrf=csrf-1; path=/'
  })
  afterEach(() => {
    vi.restoreAllMocks()
    clearDeletion()
    document.cookie = 'tripfolio_csrf=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT'
  })

  it('读取 CSRF Cookie', () => {
    expect(readCsrfCookie()).toBe('csrf-1')
  })

  it('并发调用只发一次请求，成功后写入令牌、CSRF 与账号', async () => {
    const post = vi.spyOn(api, 'POST').mockImplementation(async (_path, init) => {
      expect((init as { headers: Record<string, string> }).headers['X-CSRF-Token']).toBe('csrf-1')
      await new Promise((r) => setTimeout(r, 5))
      return { data: { data: authResult }, response: new Response() } as never
    })
    const [a, b] = await Promise.all([refreshSession(), refreshSession()])
    expect(a && b).toBe(true)
    expect(post).toHaveBeenCalledTimes(1)
    const session = useSessionStore()
    expect(session.accessToken).toBe('fresh')
    expect(session.csrfToken).toBe('csrf-2')
    expect(session.account?.nickname).toBe('甲')
  })

  it('没有 CSRF 时直接失败，不发请求', async () => {
    document.cookie = 'tripfolio_csrf=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT'
    const post = vi.spyOn(api, 'POST')
    expect(await refreshSession()).toBe(false)
    expect(post).not.toHaveBeenCalled()
  })

  it('另一标签页连续轮转 Cookie 后，每次都使用当前 Cookie 而非旧内存镜像', async () => {
    const session = useSessionStore()
    session.csrfToken = 'old-tab-csrf'
    const post = vi
      .spyOn(api, 'POST')
      .mockResolvedValue({ data: { data: authResult }, response: new Response() } as never)

    document.cookie = 'tripfolio_csrf=other-tab-1; path=/'
    expect(await refreshSession()).toBe(true)
    document.cookie = 'tripfolio_csrf=other-tab-2; path=/'
    expect(await refreshSession()).toBe(true)

    expect(
      post.mock.calls.map((call) => {
        const init = call[1] as { headers: Record<string, string> }
        return init.headers['X-CSRF-Token']
      }),
    ).toEqual(['other-tab-1', 'other-tab-2'])
    expect(session.accessToken).toBe('fresh')
    expect(session.csrfToken).toBe('csrf-2')
  })

  it('Cookie 已消失时不以旧内存 CSRF 发送刷新请求', async () => {
    useSessionStore().csrfToken = 'old-tab-csrf'
    document.cookie = 'tripfolio_csrf=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT'
    const post = vi
      .spyOn(api, 'POST')
      .mockResolvedValue({ data: { data: authResult }, response: new Response() } as never)
    expect(await refreshSession()).toBe(false)
    expect(post).not.toHaveBeenCalled()
  })

  it('刷新失败释放单页请求，后续调用读取新的 Cookie 再尝试', async () => {
    const session = useSessionStore()
    session.setAccessToken('current', 900)
    session.csrfToken = 'old-tab-csrf'
    const post = vi
      .spyOn(api, 'POST')
      .mockRejectedValueOnce(new Error('network failure'))
      .mockResolvedValueOnce({ data: { data: authResult }, response: new Response() } as never)
    expect(await refreshSession()).toBe(false)
    expect(session.accessToken).toBe('current')
    document.cookie = 'tripfolio_csrf=retry-cookie; path=/'
    expect(await refreshSession()).toBe(true)
    expect(post).toHaveBeenCalledTimes(2)
    const retry = post.mock.calls[1]?.[1] as { headers: Record<string, string> }
    expect(retry.headers['X-CSRF-Token']).toBe('retry-cookie')
  })

  it('注销状态阻止刷新，刷新过程中进入注销也不重新写入认证结果', async () => {
    const session = useSessionStore()
    session.setAccessToken('current', 900)
    const pending = {
      kind: 'pending',
      accountId: 'acc',
      operationId: 'delete-op',
      version: '1',
    } as const
    saveDeletion(pending)
    const post = vi.spyOn(api, 'POST').mockImplementation(async () => {
      saveDeletion(pending)
      return { data: { data: authResult }, response: new Response() } as never
    })
    expect(await refreshSession()).toBe(false)
    expect(post).not.toHaveBeenCalled()
    clearDeletion()
    expect(await refreshSession()).toBe(false)
    expect(post).toHaveBeenCalledTimes(1)
    expect(session.accessToken).toBe('current')
  })

  it('旧访问令牌 401 后用当前 Cookie 续期并原样重放业务请求', async () => {
    const session = useSessionStore()
    session.applyAuthResult({ ...authResult, access_token: 'expired', csrf_token: 'old-tab-csrf' })
    document.cookie = 'tripfolio_csrf=other-tab-current; path=/'
    const observed: Array<{ path: string; method: string; body: string; headers: Headers }> = []
    const response = (status: number, body: unknown) =>
      new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      })
    const fetch = vi.fn(async (request: Request) => {
      const path = new URL(request.url).pathname
      observed.push({
        path,
        method: request.method,
        body: await request.text(),
        headers: new Headers(request.headers),
      })
      if (path.endsWith('/auth/refresh')) {
        if (request.headers.get('X-CSRF-Token') !== readCsrfCookie()) {
          return response(403, { code: 'CSRF_FAILED' })
        }
        document.cookie = 'tripfolio_csrf=csrf-2; path=/'
        return response(200, { data: authResult })
      }
      if (request.headers.get('Authorization') === 'Bearer expired') {
        return response(401, { code: 'SESSION_EXPIRED' })
      }
      return response(200, {
        data: { operation_id: 'operation', affected: [], warnings: [], replayed: true },
      })
    })
    const transport = createApiClient({
      baseUrl: 'https://api.test/api/v1',
      fetch,
      refresh: refreshSession,
    })
    // Inject only the isolated transport; keep the real refresh and replay logic.
    vi.spyOn(api, 'POST').mockImplementation(transport.POST)
    const guards = '[{"kind":"members","scope_id":"trip","revision":"sha256:original"}]'
    const result = await transport.PATCH('/trips/{trip_id}/ledger-entries/{entry_id}', {
      params: {
        path: { trip_id: 'trip', entry_id: 'entry' },
        header: { 'Idempotency-Key': 'operation', 'If-Match': '"7"' },
      },
      headers: { 'X-Collection-Guards': guards },
      body: { amount: '24.00', notes: '保留输入' },
    })
    expect(result.response.status).toBe(200)
    expect(observed.map((request) => request.path)).toEqual([
      '/api/v1/trips/trip/ledger-entries/entry',
      '/api/v1/auth/refresh',
      '/api/v1/trips/trip/ledger-entries/entry',
    ])
    expect(observed[1]?.body).toBe('{}')
    expect(observed[1]?.headers.get('X-CSRF-Token')).toBe('other-tab-current')
    const original = observed[0]!
    const replay = observed[2]!
    expect(replay.method).toBe(original.method)
    expect(replay.method).toBe('PATCH')
    expect(replay.body).toBe(original.body)
    expect(replay.body).toBe(JSON.stringify({ amount: '24.00', notes: '保留输入' }))
    for (const header of ['Idempotency-Key', 'If-Match', 'X-Collection-Guards']) {
      expect(replay.headers.get(header)).toBe(original.headers.get(header))
    }
    expect(replay.headers.get('Authorization')).toBe('Bearer fresh')
    expect(session.account?.id).toBe('acc')
    expect(session.isAuthenticated).toBe(true)
    expect(fetch).toHaveBeenCalledTimes(3)
  })

  it('restoreSession 只尝试一次，失败时清空会话', async () => {
    const post = vi
      .spyOn(api, 'POST')
      .mockResolvedValue({ error: { code: 'SESSION_EXPIRED' }, response: new Response() } as never)
    expect(await restoreSession()).toBe(false)
    expect(await restoreSession()).toBe(false)
    expect(post).toHaveBeenCalledTimes(1)
    expect(useSessionStore().restored).toBe(true)
  })
})

// 回归：注册与登录要用 deviceId() 拼 client.device_id，而它依赖只在安全上下文里存在的
// crypto.randomUUID。纯 http 部署（http://域名:端口）下该函数不存在，异常发生在 fetch 之前，
// 页面只显示「网络错误，请稍后再试」，服务端连请求都收不到。
describe('register 在非安全上下文下', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    window.localStorage.clear()
  })
  afterEach(() => {
    vi.restoreAllMocks()
    window.localStorage.clear()
  })

  it('crypto.randomUUID 缺失时仍发出请求，device_id 是合法 UUID', async () => {
    const restore = withoutRandomUUID()
    try {
      expect(() => crypto.randomUUID()).toThrow(TypeError)
      const post = vi
        .spyOn(api, 'POST')
        .mockResolvedValue({ data: { data: authResult }, response: new Response() } as never)
      await register({
        challengeId: 'challenge-1',
        email: 'a@example.com',
        code: '123456',
        password: 'Tripfolio!2026',
        nickname: '甲',
      })
      const init = post.mock.calls[0]?.[1] as { body: { client: { device_id: string } } }
      expect(init.body.client.device_id).toMatch(UUID_V4)
    } finally {
      restore()
    }
  })
})
