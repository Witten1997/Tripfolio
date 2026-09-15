import { describe, expect, it, vi } from 'vitest'

const session = vi.hoisted(() => ({
  read: vi.fn(() => {
    throw new Error('访客不得访问会话')
  }),
  restore: vi.fn(),
}))
vi.mock('@/shared/stores/session', () => ({ useSessionStore: session.read }))
vi.mock('@/shared/api/auth', () => ({ restoreSession: session.restore }))
vi.mock('@/share/SharePage.vue', () => ({ default: { template: '<div>分享</div>' } }))

import { createAppRouter } from './index'

describe('真实访客导航', () => {
  it('进入分享页不实例化 session store，也不刷新', async () => {
    const router = createAppRouter('desktop')
    await router.push('/s/t000000000000000000001')
    expect(router.currentRoute.value.name).toBe('share')
    expect(session.read).not.toHaveBeenCalled()
    expect(session.restore).not.toHaveBeenCalled()
  })
})
