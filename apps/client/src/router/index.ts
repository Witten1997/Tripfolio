import {
  createRouter,
  createWebHistory,
  type RouteLocationNormalized,
  type RouteLocationRaw,
  type Router,
} from 'vue-router'

import type { Shell } from '@/shell/pickShell'

import { buildRoutes } from './routes'

export interface GuardSession {
  restored: boolean
  isAuthenticated: boolean
}

/**
 * 导航规则：访客分享页完全不触碰会话（不发刷新请求）；其余页面首次导航先用 Cookie 恢复会话，
 * 恢复失败也不阻塞公开页；已登录访问仅访客页跳旅行列表；未登录访问受保护页跳登录并带回跳地址。
 */
export async function resolveNavigation(
  to: Pick<RouteLocationNormalized, 'meta' | 'fullPath'>,
  session: GuardSession,
  restore: () => Promise<unknown>,
): Promise<true | RouteLocationRaw> {
  if (to.meta.share) return true
  if (!session.restored) await restore()
  if (to.meta.guestOnly && session.isAuthenticated) return { name: 'trips' }
  if (!to.meta.public && !session.isAuthenticated) {
    return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
  }
  return true
}

/** 网页与 Capacitor（https://localhost）都使用 HTML5 history。 */
export function createAppRouter(shell: Shell): Router {
  const router = createRouter({
    history: createWebHistory(import.meta.env.BASE_URL),
    routes: buildRoutes(shell),
  })
  router.beforeEach(async (to) => {
    if (to.meta.share) return true
    const { restoreSession } = await import('@/shared/api/auth')
    const { useSessionStore } = await import('@/shared/stores/session')
    return resolveNavigation(to, useSessionStore(), restoreSession)
  })
  return router
}
