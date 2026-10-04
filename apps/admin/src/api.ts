import { shallowRef } from 'vue'
import { adminAPIPath } from './config'
import type { components } from '@tripfolio/contracts/openapi/admin'

export type AdminIdentity = components['schemas']['AdminIdentity']
export type ManagedSession = components['schemas']['ManagedSession']
export type Overview = components['schemas']['Overview']
export type User = components['schemas']['User']
export type UserPage = components['schemas']['UserPage']
export type UserDetail = components['schemas']['UserDetail']
export type AdminTripPage = components['schemas']['AdminTripPage']
export type AdminTripDetail = components['schemas']['AdminTripDetail']
export type AdminTripMutation = components['schemas']['AdminTripMutation']

export async function mutateTrip(id: string, body: AdminTripMutation) {
  return (
    await request<{ data: components['schemas']['AdminTripMutationResult'] }>(
      `/trips/${encodeURIComponent(id)}/lifecycle`,
      { method: 'POST', body: JSON.stringify(body) },
    )
  ).data
}
export type AuditEntry = components['schemas']['AuditEntry']
export type AuditPage = components['schemas']['AuditPage']
export type RuntimeStatus = components['schemas']['RuntimeStatus']
export type JobPage = components['schemas']['JobPage']
export type DeletionJobPage = components['schemas']['DeletionJobPage']
export const identity = shallowRef<AdminIdentity | null>(null)
export const connectionError = shallowRef('')
let initialized = false
export const setupRequired = shallowRef(false)
let setupChecked = false

export async function checkSetup(force = false) {
  if (setupChecked && !force) return
  try {
    const status = await request<components['schemas']['SetupStatus']>('/setup')
    setupRequired.value = status.required
  } catch (error) {
    if (!(error instanceof AdminApiError) || error.status !== 404) {
      connectionError.value = error instanceof Error ? error.message : '初始化状态暂不可用。'
      throw error
    }
    setupRequired.value = false
  }
  setupChecked = true
  connectionError.value = ''
}

export async function initializeAdmin(body: components['schemas']['SetupRequest']) {
  await request<void>('/setup', { method: 'POST', body: JSON.stringify(body) })
  setupRequired.value = false
  setupChecked = true
  initialized = false
}

export class AdminApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public code: string,
  ) {
    super(message)
  }
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body) headers.set('Content-Type', 'application/json')
  if (identity.value) headers.set('X-Admin-CSRF', identity.value.csrf_token)
  let response: Response
  try {
    response = await fetch(`${adminAPIPath}${path}`, {
      ...init,
      headers,
      credentials: 'same-origin',
      cache: 'no-store',
    })
  } catch {
    throw new AdminApiError('暂时无法连接后台，请检查网络后重试。', 0, 'NETWORK_ERROR')
  }
  if (!response.ok) {
    const problem = await response.json().catch(() => ({}))
    if (problem.code === 'ADMIN_SESSION_EXPIRED') identity.value = null
    throw new AdminApiError(
      problem.detail || '操作未完成，请稍后重试。',
      response.status,
      problem.code || 'REQUEST_FAILED',
    )
  }
  return response.status === 204 ? (undefined as T) : response.json()
}

export async function restoreSession() {
  if (initialized) return
  try {
    identity.value = (await request<{ data: AdminIdentity }>('/session')).data
    connectionError.value = ''
    initialized = true
  } catch (error) {
    if (error instanceof AdminApiError && error.status === 401) {
      initialized = true
      identity.value = null
      connectionError.value = ''
    } else {
      connectionError.value = error instanceof Error ? error.message : '连接失败，请重试。'
    }
  }
}

export async function login(email: string, password: string) {
  const result = await request<{ data: AdminIdentity }>('/login', {
    method: 'POST',
    body: JSON.stringify({ email, password }),
  })
  identity.value = result.data
  connectionError.value = ''
  initialized = true
}

export async function logout() {
  await request<void>('/session', { method: 'DELETE' })
  identity.value = null
}

export async function listSessions() {
  return (await request<{ data: ManagedSession[] }>('/sessions')).data
}

export async function revokeSession(id: string) {
  await request<void>(`/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (id === identity.value?.session_id) identity.value = null
}

export async function reauthenticate(password: string) {
  await request<void>('/reauthenticate', { method: 'POST', body: JSON.stringify({ password }) })
}

export async function loadOverview() {
  return (await request<{ data: Overview }>('/overview')).data
}

export async function listUsers(query: {
  q: string
  status: string
  registeredFrom: string
  registeredTo: string
  page: number
  pageSize: number
}) {
  const params = new URLSearchParams({
    page: String(query.page),
    page_size: String(query.pageSize),
  })
  if (query.q.trim()) params.set('q', query.q.trim())
  if (query.status) params.set('status', query.status)
  if (query.registeredFrom) params.set('registered_from', query.registeredFrom)
  if (query.registeredTo) params.set('registered_to', query.registeredTo)
  return request<UserPage>(`/users?${params}`)
}

export async function loadUser(id: string) {
  return (await request<{ data: UserDetail }>(`/users/${encodeURIComponent(id)}`)).data
}

export function listTrips(query: Record<string, string>) {
  const params = new URLSearchParams(Object.entries(query).filter(([, value]) => value !== ''))
  return request<AdminTripPage>(`/trips?${params}`)
}
export async function loadTrip(id: string) {
  return (await request<{ data: AdminTripDetail }>(`/trips/${encodeURIComponent(id)}`)).data
}

export function listAudits(query: Record<string, string>) {
  const params = new URLSearchParams(Object.entries(query).filter(([, value]) => value !== ''))
  return request<AuditPage>(`/audits?${params}`)
}

export async function loadRuntime() {
  return (await request<{ data: RuntimeStatus }>('/runtime')).data
}

export function listJobs(state: string, page: number) {
  return request<JobPage>(
    `/jobs?${new URLSearchParams({ state, page: String(page), page_size: '20' })}`,
  )
}

export function listDeletionJobs(state: string, page: number) {
  return request<DeletionJobPage>(
    `/deletion-jobs?${new URLSearchParams({ state, page: String(page), page_size: '20' })}`,
  )
}
