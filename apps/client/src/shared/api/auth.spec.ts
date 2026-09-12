import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from './client'
import { readCsrfCookie, refreshSession, restoreSession } from './auth'
import { useSessionStore } from '@/shared/stores/session'

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
