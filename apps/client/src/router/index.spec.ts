import { describe, expect, it, vi } from 'vitest'

import { resolveNavigation } from './index'

const to = (meta: Record<string, boolean>, fullPath = '/x') => ({ meta, fullPath })

describe('resolveNavigation', () => {
  it('访客分享页不恢复会话也不要求登录', async () => {
    const restore = vi.fn(async () => false)
    const result = await resolveNavigation(
      to({ public: true, share: true }, '/s/abc'),
      { restored: false, isAuthenticated: false },
      restore,
    )
    expect(result).toBe(true)
    expect(restore).not.toHaveBeenCalled()
  })

  it('受保护页面先恢复会话，仍未登录则跳登录并带回跳', async () => {
    const session = { restored: false, isAuthenticated: false }
    const restore = vi.fn(async () => {
      session.restored = true
    })
    const result = await resolveNavigation(to({}, '/trips/1'), session, restore)
    expect(restore).toHaveBeenCalledTimes(1)
    expect(result).toEqual({ name: 'login', query: { redirect: '/trips/1' } })
  })

  it('已登录访问仅访客页跳旅行列表；公开页放行', async () => {
    expect(
      await resolveNavigation(
        to({ public: true, guestOnly: true }),
        { restored: true, isAuthenticated: true },
        vi.fn(),
      ),
    ).toEqual({ name: 'trips' })
    expect(
      await resolveNavigation(
        to({ public: true }),
        { restored: true, isAuthenticated: false },
        vi.fn(),
      ),
    ).toBe(true)
  })
})
