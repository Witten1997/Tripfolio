import type { components } from '@tripfolio/contracts/openapi/v1'

import { api } from '@/shared/api/client'
import { ApiError, logout } from '@/shared/api/auth'
import { randomId } from '@/shared/randomId'
import { useSessionStore } from '@/shared/stores/session'

export type Account = components['schemas']['Account']
export type Session = components['schemas']['Session']
export type AccountPatch = components['schemas']['AccountPatch']

/** 拉取本人资料并同步到 store。 */
export async function fetchAccount(): Promise<Account> {
  const { data, error } = await api.GET('/account')
  if (error || !data) throw new ApiError(error)
  useSessionStore().setAccount(data.data)
  return data.data
}

/** PATCH /account：以当前 version 作 If-Match 基线；返回更新后的资料并写入 store。 */
export async function updateAccount(baseVersion: string, patch: AccountPatch): Promise<Account> {
  const { data, error } = await api.PATCH('/account', {
    params: { header: { 'Idempotency-Key': randomId(), 'If-Match': `"${baseVersion}"` } },
    body: patch,
  })
  if (error || !data) throw new ApiError(error)
  const account = data.data.data as Account
  useSessionStore().setAccount(account)
  return account
}

export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  const { error } = await api.POST('/account/password', {
    body: { current_password: currentPassword, new_password: newPassword },
  })
  if (error) throw new ApiError(error)
}

export async function listSessions(): Promise<Session[]> {
  const { data, error } = await api.GET('/account/sessions')
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function revokeSession(sessionId: string): Promise<void> {
  const { error } = await api.DELETE('/account/sessions/{session_id}', {
    params: { path: { session_id: sessionId } },
  })
  if (error) throw new ApiError(error)
}

export { logout }
