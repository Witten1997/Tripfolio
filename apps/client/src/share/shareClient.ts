import createClient, { type Client, type Middleware } from 'openapi-fetch'

import type { paths } from '@tripfolio/contracts/openapi/v1'

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
    baseUrl: options.baseUrl ?? import.meta.env.VITE_API_BASE_URL ?? '/api/v1',
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
