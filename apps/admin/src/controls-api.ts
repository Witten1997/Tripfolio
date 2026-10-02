import type { components } from '@tripfolio/contracts/openapi/admin'
import { identity, request } from './api'

export type UserControl = 'ban' | 'unban' | 'force-logout'
export type SharingRestriction = components['schemas']['SharingRestriction']
export async function controlUser(id: string, action: UserControl, reason: string) {
  await request<void>(`/users/${encodeURIComponent(id)}/${action}`, {
    method: 'POST',
    body: JSON.stringify({ reason }),
  })
  if (identity.value?.id === id) identity.value = null
}
export async function loadSharingRestriction(id: string) {
  return (
    await request<{ data: SharingRestriction }>(
      `/trips/${encodeURIComponent(id)}/sharing-restriction`,
    )
  ).data
}
export async function controlSharing(
  id: string,
  restricted: boolean,
  version: number,
  reason: string,
) {
  return (
    await request<{ data: SharingRestriction }>(
      `/trips/${encodeURIComponent(id)}/sharing-restriction`,
      {
        method: 'PUT',
        body: JSON.stringify({ restricted, version, reason }),
      },
    )
  ).data
}
