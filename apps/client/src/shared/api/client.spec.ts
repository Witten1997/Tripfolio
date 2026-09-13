import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { createApiClient } from './client'
import { useSessionStore } from '@/shared/stores/session'

function jsonResponse(status: number, body: unknown, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

function problem(status: number, code: string) {
  return jsonResponse(status, {
    type: 'about:blank',
    title: code,
    status,
    code,
    request_id: 'req_test',
  })
}

describe('api client 刷新重放', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('注入内存中的 Bearer 令牌', async () => {
    useSessionStore().setAccessToken('t1', 900)
    const fetch = vi.fn(async (req: Request) => {
      expect(req.headers.get('Authorization')).toBe('Bearer t1')
      return jsonResponse(200, { data: { currencies: [], default_currency_code: 'CNY' } })
    })
    const api = createApiClient({ baseUrl: 'http://api.test/v1', fetch })
    const { response } = await api.GET('/metadata')
    expect(response.status).toBe(200)
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('401 后刷新一次并用新令牌重放原请求', async () => {
    const session = useSessionStore()
    session.setAccessToken('stale', 900)
    const seen: string[] = []
    const fetch = vi.fn(async (req: Request) => {
      seen.push(req.headers.get('Authorization') ?? '')
      if (seen.length === 1) return problem(401, 'SESSION_EXPIRED')
      return jsonResponse(200, { data: { id: 'a', email: 'x@example.com' } })
    })
    const refresh = vi.fn(async () => {
      session.setAccessToken('fresh', 900)
      return true
    })
    const api = createApiClient({ baseUrl: 'http://api.test/v1', fetch, refresh })
    const { data, response } = await api.GET('/account')
    expect(response.status).toBe(200)
    expect(data?.data.email).toBe('x@example.com')
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(seen).toEqual(['Bearer stale', 'Bearer fresh'])
  })

  it('重放时保留 PATCH 正文与头', async () => {
    const session = useSessionStore()
    session.setAccessToken('stale', 900)
    const bodies: string[] = []
    const fetch = vi.fn(async (req: Request) => {
      bodies.push(await req.text())
      if (bodies.length === 1) return problem(401, 'SESSION_EXPIRED')
      expect(req.method).toBe('PATCH')
      expect(req.headers.get('If-Match')).toBe('"1"')
      return jsonResponse(200, {
        data: { operation_id: 'op', affected: [], warnings: [], replayed: false },
      })
    })
    const api = createApiClient({
      baseUrl: 'http://api.test/v1',
      fetch,
      refresh: async () => {
        session.setAccessToken('fresh', 900)
        return true
      },
    })
    const { response } = await api.PATCH('/account', {
      params: { header: { 'Idempotency-Key': 'k', 'If-Match': '"1"' } },
      body: { nickname: '新' },
    })
    expect(response.status).toBe(200)
    expect(bodies).toEqual(['{"nickname":"新"}', '{"nickname":"新"}'])
  })

  it('刷新失败时清空会话并原样返回 401', async () => {
    const session = useSessionStore()
    session.setAccessToken('stale', 900)
    const fetch = vi.fn(async () => problem(401, 'SESSION_EXPIRED'))
    const api = createApiClient({
      baseUrl: 'http://api.test/v1',
      fetch,
      refresh: async () => false,
    })
    const { error, response } = await api.GET('/account')
    expect(response.status).toBe(401)
    expect(error?.code).toBe('SESSION_EXPIRED')
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(session.isAuthenticated).toBe(false)
  })

  it('重放兼容浏览器原生 fetch 的接收者约束', async () => {
    const session = useSessionStore()
    session.setAccessToken('stale', 900)
    let attempts = 0
    const fetch = async function (this: unknown) {
      if (this !== undefined) throw new TypeError('Illegal invocation')
      attempts++
      if (attempts === 1) return problem(401, 'SESSION_EXPIRED')
      return jsonResponse(200, { data: { id: 'a', email: 'x@example.com' } })
    }
    const api = createApiClient({
      baseUrl: 'http://api.test/v1',
      fetch,
      refresh: async () => {
        session.setAccessToken('fresh', 900)
        return true
      },
    })
    const { data } = await api.GET('/account')
    expect(data?.data.email).toBe('x@example.com')
    expect(attempts).toBe(2)
  })

  it('认证接口自身的 401 不触发刷新', async () => {
    const fetch = vi.fn(async () => problem(401, 'INVALID_CREDENTIALS'))
    const refresh = vi.fn(async () => true)
    const api = createApiClient({ baseUrl: 'http://api.test/v1', fetch, refresh })
    const { error } = await api.POST('/auth/login', {
      body: { email: 'a@b.c', password: 'x', client: { kind: 'web', device_id: 'd' } },
    })
    expect(error?.code).toBe('INVALID_CREDENTIALS')
    expect(refresh).not.toHaveBeenCalled()
  })

  it('密码复验的 SESSION_EXPIRED 刷新一次并保留密码正文重放', async () => {
    const session = useSessionStore()
    session.setAccessToken('stale', 900)
    const bodies: string[] = []
    const authorizations: string[] = []
    const password = 'correct password 文本'
    const fetch = vi.fn(async (request: Request) => {
      expect(request.method).toBe('POST')
      expect(new URL(request.url).pathname).toBe('/v1/auth/reauthenticate')
      bodies.push(await request.text())
      authorizations.push(request.headers.get('Authorization') ?? '')
      if (bodies.length === 1) return problem(401, 'SESSION_EXPIRED')
      return new Response(null, { status: 204 })
    })
    const refresh = vi.fn(async () => {
      session.setAccessToken('fresh', 900)
      return true
    })
    const api = createApiClient({ baseUrl: 'http://api.test/v1', fetch, refresh })
    const { error, response } = await api.POST('/auth/reauthenticate', { body: { password } })
    expect(response.status).toBe(204)
    expect(error).toBeUndefined()
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(bodies).toEqual([JSON.stringify({ password }), JSON.stringify({ password })])
    expect(authorizations).toEqual(['Bearer stale', 'Bearer fresh'])
    expect(session.accessToken).toBe('fresh')
  })

  it('密码复验的 INVALID_CREDENTIALS 不刷新、不退出，错误正文仍可读取', async () => {
    const session = useSessionStore()
    session.setAccessToken('current', 900)
    const fetch = vi.fn(async () => problem(401, 'INVALID_CREDENTIALS'))
    const refresh = vi.fn(async () => false)
    const api = createApiClient({ baseUrl: 'http://api.test/v1', fetch, refresh })
    const { error, response } = await api.POST('/auth/reauthenticate', {
      body: { password: 'wrong password' },
    })
    expect(response.status).toBe(401)
    expect(error?.code).toBe('INVALID_CREDENTIALS')
    expect(refresh).not.toHaveBeenCalled()
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(session.accessToken).toBe('current')
    expect(session.isAuthenticated).toBe(true)
  })
})
