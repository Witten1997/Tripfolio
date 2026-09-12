import createClient, { type Client, type Middleware } from 'openapi-fetch'

import type { paths } from '@tripfolio/contracts/openapi/v1'

import { useSessionStore } from '@/shared/stores/session'

export type ApiClient = Client<paths>

/** 401 后尝试刷新并重放一次的请求；认证接口本身不重放。 */
const NO_REPLAY_PATHS = new Set([
  '/auth/email-challenges',
  '/auth/register',
  '/auth/login',
  '/auth/refresh',
  '/auth/logout',
  '/auth/password-reset',
])

export interface ApiClientOptions {
  baseUrl?: string
  fetch?: (input: Request) => Promise<Response>
  /** 刷新会话；成功返回 true。默认实现在 auth.ts 中注册，避免循环依赖。 */
  refresh?: () => Promise<boolean>
}

let refreshHandler: (() => Promise<boolean>) | null = null

/** 由 auth.ts 在模块加载时注册真实的刷新实现。 */
export function registerRefreshHandler(handler: () => Promise<boolean>) {
  refreshHandler = handler
}

/**
 * 与契约同源的类型安全客户端。所有业务请求经过它：
 * - onRequest 注入内存中的 Bearer 令牌，并保留请求副本供重放；
 * - onResponse 遇到 401 时刷新一次会话并用新令牌重放原请求；刷新失败则清空会话，把 401 原样返回。
 */
export function createApiClient(options: ApiClientOptions = {}): ApiClient {
  const client = createClient<paths>({
    baseUrl: options.baseUrl ?? import.meta.env.VITE_API_BASE_URL ?? '/api/v1',
    fetch: options.fetch,
  })
  const refresh = () => (options.refresh ?? refreshHandler ?? (async () => false))()
  const pending = new Map<string, Request>()

  const authorization: Middleware = {
    onRequest({ request, id, schemaPath }) {
      const session = useSessionStore()
      if (session.accessToken) {
        request.headers.set('Authorization', `Bearer ${session.accessToken}`)
      }
      if (!NO_REPLAY_PATHS.has(schemaPath)) {
        pending.set(id, request.clone())
      }
      return request
    },
    async onResponse({ response, id, options: merged }) {
      const original = pending.get(id)
      pending.delete(id)
      if (response.status !== 401 || !original) return response
      const session = useSessionStore()
      if (!(await refresh())) {
        session.clear()
        return response
      }
      const replay = new Request(original)
      replay.headers.set('Authorization', `Bearer ${session.accessToken}`)
      return merged.fetch(replay)
    },
    onError({ id }) {
      pending.delete(id)
    },
  }
  client.use(authorization)
  return client
}

export const api: ApiClient = createApiClient()
