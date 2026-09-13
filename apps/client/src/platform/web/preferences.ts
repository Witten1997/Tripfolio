import type { KeyValueStore } from '../types'

/** 网页：非敏感偏好放 localStorage，跨标签页与刷新保留。 */
export const webPreferences: KeyValueStore = {
  async get(key) {
    return window.localStorage.getItem(key)
  },
  async set(key, value) {
    window.localStorage.setItem(key, value)
  },
  async remove(key) {
    window.localStorage.removeItem(key)
  },
}
