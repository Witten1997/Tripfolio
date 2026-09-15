import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from './client'
import { readCsrfCookie, refreshSession, register, restoreSession } from './auth'
import { useSessionStore } from '@/shared/stores/session'

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

const authResult = {
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
    document.cookie = 'tripfolio_csrf=csrf-1; path=/'
  })
  afterEach(() => {
    vi.restoreAllMocks()
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
