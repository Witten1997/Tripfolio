import type { components } from '@tripfolio/contracts/openapi/v1'

import { api, registerRefreshHandler } from '@/shared/api/client'
import { deviceId, useSessionStore } from '@/shared/stores/session'

export type Problem = components['schemas']['Problem']
type ClientInfo = components['schemas']['ClientInfo']

/** 网页壳固定为 web 客户端；Capacitor 壳在接入安卓时改为 android 并走正文 refresh_token。 */
function webClient(): ClientInfo {
  return { kind: 'web', device_id: deviceId(), device_name: navigator.userAgent.slice(0, 120) }
}

/** 把 problem+json 转成可直接展示的文案；字段错误优先。 */
export function problemMessage(problem: Problem | undefined, fallback = '请求失败'): string {
  if (!problem) return fallback
  const field = problem.errors?.[0]
  if (field?.message) return field.message
  return problem.detail || problem.title || fallback
}

/** 从非 HttpOnly 的 CSRF Cookie 读取值（页面刷新后内存为空时使用）。 */
export function readCsrfCookie(): string | null {
  const match = document.cookie
    .split(';')
    .map((c) => c.trim())
    .find((c) => c.startsWith('__Host-tripfolio_csrf=') || c.startsWith('tripfolio_csrf='))
  if (!match) return null
  return decodeURIComponent(match.slice(match.indexOf('=') + 1)) || null
}

let inflightRefresh: Promise<boolean> | null = null

/**
 * 用 HttpOnly Cookie 刷新会话（双提交 CSRF）。并发调用共享同一个请求；
 * 成功写入新的访问令牌与账号，失败返回 false 并由调用方决定是否清会话。
 */
export function refreshSession(): Promise<boolean> {
  if (inflightRefresh) return inflightRefresh
  // 清理放在 .finally 里而不是函数体内：函数体没有 await 时会同步结束，早于下面的赋值。
  const run = (async () => {
    const session = useSessionStore()
    const csrf = session.csrfToken ?? readCsrfCookie()
    if (!csrf) return false
    try {
      const { data } = await api.POST('/auth/refresh', {
        body: {},
        headers: { 'X-CSRF-Token': csrf },
      })
      if (!data) return false
      session.applyAuthResult(data.data)
      return true
    } catch {
      return false
    }
  })().finally(() => {
    inflightRefresh = null
  })
  inflightRefresh = run
  return run
}

registerRefreshHandler(refreshSession)

/** 应用启动时尝试用 Cookie 恢复登录态，只做一次。 */
export async function restoreSession(): Promise<boolean> {
  const session = useSessionStore()
  if (session.restored) return session.isAuthenticated
  const ok = await refreshSession()
  if (!ok) session.clear()
  session.restored = true
  return ok
}

export async function requestEmailChallenge(purpose: 'register' | 'reset_password', email: string) {
  const { data, error } = await api.POST('/auth/email-challenges', { body: { purpose, email } })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function register(input: {
  challengeId: string
  email: string
  code: string
  password: string
  nickname: string
}) {
  const { data, error } = await api.POST('/auth/register', {
    body: {
      challenge_id: input.challengeId,
      email: input.email,
      code: input.code,
      password: input.password,
      nickname: input.nickname,
      client: webClient(),
    },
  })
  if (error || !data) throw new ApiError(error)
  useSessionStore().applyAuthResult(data.data)
  return data.data.account
}

export async function login(email: string, password: string) {
  const { data, error } = await api.POST('/auth/login', {
    body: { email, password, client: webClient() },
  })
  if (error || !data) throw new ApiError(error)
  useSessionStore().applyAuthResult(data.data)
  return data.data.account
}

export async function resetPassword(input: {
  challengeId: string
  email: string
  code: string
  newPassword: string
}) {
  const { error } = await api.POST('/auth/password-reset', {
    body: {
      challenge_id: input.challengeId,
      email: input.email,
      code: input.code,
      new_password: input.newPassword,
    },
  })
  if (error) throw new ApiError(error)
}

/** 永久清理前复验当前密码；认证状态仅由服务端绑定到当前会话。 */
export async function reauthenticate(password: string): Promise<void> {
  const { error } = await api.POST('/auth/reauthenticate', { body: { password } })
  if (error) throw new ApiError(error)
}

/** 退出：服务端撤销会话并清 Cookie；无论结果如何本地都清空。 */
export async function logout() {
  const session = useSessionStore()
  try {
    await api.POST('/auth/logout')
  } finally {
    session.clear()
  }
}

/** 业务错误：携带 problem+json，页面用 message 展示、用 problem.code 分支。 */
export class ApiError extends Error {
  readonly problem: Problem | undefined
  constructor(problem: Problem | undefined, fallback = '请求失败，请稍后再试') {
    super(problemMessage(problem, fallback))
    this.name = 'ApiError'
    this.problem = problem
  }
  get code(): string | undefined {
    return this.problem?.code
  }
}
