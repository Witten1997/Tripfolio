import { createRouter, createWebHistory, type Router } from 'vue-router'

import { restoreSession } from '@/shared/api/auth'
import { useSessionStore } from '@/shared/stores/session'
import type { Shell } from '@/shell/pickShell'

import { buildRoutes } from './routes'

/** 网页与 Capacitor（https://localhost）都使用 HTML5 history。 */
export function createAppRouter(shell: Shell): Router {
  const router = createRouter({
    history: createWebHistory(import.meta.env.BASE_URL),
    routes: buildRoutes(shell),
  })
  router.beforeEach(async (to) => {
    const session = useSessionStore()
    // 首次导航先用 Cookie 恢复会话；恢复失败也不阻塞公开页
    if (!session.restored) await restoreSession()
    if (to.meta.guestOnly && session.isAuthenticated) return { name: 'trips' }
    if (!to.meta.public && !session.isAuthenticated) {
      return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
    }
    return true
  })
  return router
}
