import type { DeletionReceipt } from '@/shared/api/deletion'

export const DELETION_STORAGE_KEY = 'tripfolio.account-deletion'
const OWNER_KEY = 'tripfolio.account-owner'

export interface PendingDeletion {
  kind: 'pending'
  accountId: string
  operationId: string
  version: string
  jobId?: string
}
export type SavedDeletion =
  | PendingDeletion
  | { kind: 'receipt'; accountId: string; receipt: DeletionReceipt }
  | { kind: 'expired' | 'invalid'; accountId: string }

export function saveDeletion(value: SavedDeletion) {
  const serialized = JSON.stringify(value)
  try {
    window.localStorage.setItem(DELETION_STORAGE_KEY, serialized)
    if (window.localStorage.getItem(DELETION_STORAGE_KEY) !== serialized) throw new Error()
  } catch {
    throw new Error(
      '无法可靠保存注销查询凭证。请允许此站点保存浏览器数据，然后重试；暂时不要关闭此页面。',
    )
  }
}

export function clearDeletion() {
  window.localStorage.removeItem(DELETION_STORAGE_KEY)
}

export function readDeletion(): SavedDeletion | null {
  try {
    const value = JSON.parse(
      window.localStorage.getItem(DELETION_STORAGE_KEY) ?? 'null',
    ) as SavedDeletion | null
    if (!value) return null
    if (typeof value.accountId !== 'string') {
      clearDeletion()
      return null
    }
    const owner = window.localStorage.getItem(OWNER_KEY)
    if (owner && owner !== value.accountId) {
      clearDeletion()
      return null
    }
    if (
      value.kind === 'pending' &&
      typeof value.operationId === 'string' &&
      typeof value.version === 'string'
    )
      return value
    if (value.kind === 'expired' || value.kind === 'invalid') return value
    if (
      value.kind === 'receipt' &&
      typeof value.receipt?.job_id === 'string' &&
      typeof value.receipt.receipt_token === 'string'
    ) {
      if (Date.parse(value.receipt.receipt_expires_at) > Date.now()) return value
      const expired = { kind: 'expired' as const, accountId: value.accountId }
      saveDeletion(expired)
      return expired
    }
    clearDeletion()
  } catch {
    return null
  }
  return null
}

/** 登录另一个账号时清除前一个账号的查询能力；跨标签页通过 storage 事件同步。 */
export function bindDeletionOwner(accountId: string) {
  try {
    const existing = readDeletion()
    if (existing && existing.accountId !== accountId) clearDeletion()
    window.localStorage.setItem(OWNER_KEY, accountId)
  } catch {
    /* 存储不可用时不阻断普通登录；注销申请会在写前单独验证。 */
  }
}
