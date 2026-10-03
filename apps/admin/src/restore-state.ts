import { shallowRef } from 'vue'
import type { components } from '@tripfolio/contracts/openapi/admin'
import { adminBasePath } from './config'

export type RestoreJob = components['schemas']['RestoreJob']
const key = `${adminBasePath}:restore`
type PendingRestore = { id: string; token: string }
function read(): PendingRestore | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(key) || 'null')
    return typeof value?.id === 'string' && typeof value?.token === 'string' ? value : null
  } catch {
    return null
  }
}
export const pendingRestore = shallowRef<PendingRestore | null>(read())
export function saveRestore(value: PendingRestore | null) {
  pendingRestore.value = value
  try {
    if (value) sessionStorage.setItem(key, JSON.stringify(value))
    else sessionStorage.removeItem(key)
  } catch {
    /* The current tab can still display the running task. */
  }
}
