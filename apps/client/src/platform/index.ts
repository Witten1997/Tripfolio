import { Capacitor } from '@capacitor/core'

import { capacitorLocalDatabase } from './capacitor/localDatabase'
import { capacitorNetwork } from './capacitor/network'
import { capacitorPreferences } from './capacitor/preferences'
import { capacitorSecureStorage } from './capacitor/secureStorage'
import type { Platform, PlatformKind } from './types'
import { webClipboard } from './web/clipboard'
import { webNetwork } from './web/network'
import { webPreferences } from './web/preferences'
import { webSecureStorage } from './web/secureStorage'

export type * from './types'

function detectKind(): PlatformKind {
  if (!Capacitor.isNativePlatform()) return 'web'
  // Capacitor 的平台名在鸿蒙化版本中为 harmony/ohos；未知原生平台按安卓处理
  const name = Capacitor.getPlatform()
  return name === 'harmony' || name === 'ohos' ? 'harmony' : 'android'
}

function createPlatform(): Platform {
  const kind = detectKind()
  const isNative = kind !== 'web'
  return {
    kind,
    clipboard: webClipboard,
    isNative,
    secureStorage: isNative ? capacitorSecureStorage : webSecureStorage,
    network: isNative ? capacitorNetwork : webNetwork,
    preferences: isNative ? capacitorPreferences : webPreferences,
    // 网页在 P0 只在线使用；浏览器内 SQLite（wa-sqlite）留待网页离线需求确定后接入
    localDatabase: isNative ? capacitorLocalDatabase : null,
  }
}

/** 进程内单例，启动时确定，运行期间不变。 */
export const platform: Platform = createPlatform()
