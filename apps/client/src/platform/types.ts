/**
 * 平台接口：业务代码只依赖这些类型，具体实现按运行环境在 src/platform/index.ts 装配。
 * 网页在 P0 只在线使用；安卓由 Capacitor 插件实现；鸿蒙阶段补充 ohos 实现。
 */

import type { SqlConnectionOpener } from './localdb/executor'

export type PlatformKind = 'web' | 'android' | 'harmony'

/** 非敏感的本地偏好（主题等）。网页用 localStorage，安卓用 Preferences 插件。 */
export interface KeyValueStore {
  get(key: string): Promise<string | null>
  set(key: string, value: string): Promise<void>
  remove(key: string): Promise<void>
}

/** 凭证等敏感值的持久存储。安卓由 Keystore 加密；网页不持久化凭证（刷新令牌在 HttpOnly Cookie）。 */
export interface SecureStorage {
  get(key: string): Promise<string | null>
  set(key: string, value: string): Promise<void>
  remove(key: string): Promise<void>
}

/** 网络状态，用于同步触发与离线提示。 */
export interface NetworkStatus {
  isOnline(): Promise<boolean>
  /** 订阅变化，返回取消订阅函数。 */
  onChange(listener: (online: boolean) => void): () => void
}

export interface Platform {
  kind: PlatformKind
  isNative: boolean
  secureStorage: SecureStorage
  network: NetworkStatus
  preferences: KeyValueStore
  /** 本地数据库；网页在 P0 为 null（在线使用）。 */
  localDatabase: SqlConnectionOpener | null
}
