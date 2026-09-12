import type { SecureStorage } from '../types'

/**
 * 网页端不把凭证写入可被脚本读取的持久存储：刷新令牌由 HttpOnly Cookie 承担，
 * 这里只提供会话级（标签页关闭即失效）的存储，满足接口一致性。
 */
export const webSecureStorage: SecureStorage = {
  async get(key) {
    return window.sessionStorage.getItem(key)
  },
  async set(key, value) {
    window.sessionStorage.setItem(key, value)
  },
  async remove(key) {
    window.sessionStorage.removeItem(key)
  },
}
