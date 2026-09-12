import { SecureStorage as NativeSecureStorage } from '@aparajita/capacitor-secure-storage'

import type { SecureStorage } from '../types'

/** 安卓：AES-GCM + Android Keystore 加密后存入应用私有 SharedPreferences。 */
export const capacitorSecureStorage: SecureStorage = {
  async get(key) {
    const value = await NativeSecureStorage.get(key)
    return typeof value === 'string' ? value : null
  },
  async set(key, value) {
    await NativeSecureStorage.set(key, value)
  },
  async remove(key) {
    await NativeSecureStorage.remove(key)
  },
}
