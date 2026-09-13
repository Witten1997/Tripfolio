import { Preferences } from '@capacitor/preferences'

import type { KeyValueStore } from '../types'

/** 安卓：应用私有 SharedPreferences；不加密，只放主题等非敏感偏好。 */
export const capacitorPreferences: KeyValueStore = {
  async get(key) {
    const { value } = await Preferences.get({ key })
    return value ?? null
  },
  async set(key, value) {
    await Preferences.set({ key, value })
  },
  async remove(key) {
    await Preferences.remove({ key })
  },
}
