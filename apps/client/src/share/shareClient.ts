import createClient, { type Client, type Middleware } from 'openapi-fetch'

import type { paths } from '@tripfolio/contracts/openapi/v1'

import { resolveApiBaseUrl } from '@/shared/api/baseUrl'

export type ShareClient = Client<paths>

export interface ShareClientOptions {
  baseUrl?: string
  fetch?: (input: Request) => Promise<Response>
}

/**
 * 访客客户端：只注入 Authorization: Share <token>，不读会话、401 不刷新也不重放。
 * 与登录态的 api 单例完全隔离，访客页因此不会触发任何会话逻辑。令牌只在内存中。
 */
export function createShareClient(token: string, options: ShareClientOptions = {}): ShareClient {
  const client = createClient<paths>({
    // 与登录态客户端同一套前缀解析：空串视为未配置，回退到同源 /api/v1。
    baseUrl: resolveApiBaseUrl(options.baseUrl, import.meta.env.VITE_API_BASE_URL),
    fetch: options.fetch,
  })
  const shareAuth: Middleware = {
    onRequest({ request }) {
      request.headers.set('Authorization', `Share ${token}`)
      return request
    },
  }
  client.use(shareAuth)
  return client
}
