import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import type { components } from '@tripfolio/contracts/openapi/v1'

export type Account = components['schemas']['Account']
export type AuthResult = components['schemas']['AuthResult']

/** 安装标识（device_id）：不是授权凭据，只用于会话列表区分设备；网页放 localStorage 即可。 */
const DEVICE_ID_KEY = 'tripfolio.device_id'

export function deviceId(): string {
  let id = window.localStorage.getItem(DEVICE_ID_KEY)
  if (!id) {
    id = crypto.randomUUID()
    window.localStorage.setItem(DEVICE_ID_KEY, id)
  }
  return id
}

/**
 * 访问令牌只存在内存中（接口设计 3.1）。刷新令牌网页端在 HttpOnly Cookie，
 * Capacitor 端在平台安全存储，都不经过本 store。CSRF 令牌是非 HttpOnly Cookie 的镜像，
 * 刷新请求必须把它放进 X-CSRF-Token 头（双提交）。
 */
export const useSessionStore = defineStore('session', () => {
  const accessToken = ref<string | null>(null)
  const accessTokenExpiresAt = ref<number | null>(null)
  const csrfToken = ref<string | null>(null)
  const account = ref<Account | null>(null)
  /** 是否已经尝试过用 Cookie 恢复会话（应用启动时只做一次）。 */
  const restored = ref(false)

  const isAuthenticated = computed(() => accessToken.value !== null)

  function setAccessToken(token: string, expiresInSeconds: number) {
    accessToken.value = token
    accessTokenExpiresAt.value = Date.now() + expiresInSeconds * 1000
  }

  /** 登录、注册、刷新成功后统一调用。 */
  function applyAuthResult(result: AuthResult) {
    setAccessToken(result.access_token, result.expires_in_seconds)
    if (result.csrf_token) csrfToken.value = result.csrf_token
    account.value = result.account
  }

  function setAccount(next: Account) {
    account.value = next
  }

  /** 访问令牌是否将在 skewSeconds 内过期（用于提前刷新）。 */
  function expiresWithin(skewSeconds: number): boolean {
    return (
      accessTokenExpiresAt.value !== null &&
      accessTokenExpiresAt.value - Date.now() <= skewSeconds * 1000
    )
  }

  function clear() {
    accessToken.value = null
    accessTokenExpiresAt.value = null
    csrfToken.value = null
    account.value = null
  }

  return {
    accessToken,
    accessTokenExpiresAt,
    csrfToken,
    account,
    restored,
    isAuthenticated,
    setAccessToken,
    applyAuthResult,
    setAccount,
    expiresWithin,
    clear,
  }
})
