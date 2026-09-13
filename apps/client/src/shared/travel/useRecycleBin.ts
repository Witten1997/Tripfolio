import { onScopeDispose, ref, shallowRef } from 'vue'

import { ApiError, reauthenticate } from '@/shared/api/auth'
import {
  getTrashedTrip,
  listTrashedTrips,
  purgeTrip,
  restoreTrip,
  type TrashedTrip,
} from '@/shared/api/trips'
import { actionError, createWriteIntent, writeWarnings } from '@/shared/api/writes'
import { useCursorPage } from '@/shared/travel/useCursorPage'

export function canRestoreTrip(trip: TrashedTrip, now = Date.now()) {
  return (
    !trip.purge_requested_at &&
    !!trip.purge_after_at &&
    new Date(trip.purge_after_at).valueOf() > now
  )
}

export function retentionLabel(trip: TrashedTrip, now = Date.now()) {
  if (trip.purge_requested_at) return '已请求永久清理，无法恢复'
  if (!trip.purge_after_at) return '恢复截止时间未知'
  const remaining = new Date(trip.purge_after_at).valueOf() - now
  if (!Number.isFinite(remaining)) return '恢复截止时间未知'
  if (remaining <= 0) return '已超过恢复截止时间'
  const hours = Math.ceil(remaining / 3600000)
  if (hours < 24) return `剩余约 ${hours} 小时`
  return `剩余约 ${Math.floor(hours / 24)} 天 ${hours % 24} 小时`
}

export function formatTripTime(value: string | null) {
  if (!value) return '—'
  return new Date(value).toLocaleString('zh-CN', { hour12: false })
}

export function useRecycleBin() {
  const page = useCursorPage(() => ({ limit: 30 }), listTrashedTrips)
  const now = ref(Date.now())
  const timer = setInterval(() => {
    now.value = Date.now()
  }, 60000)
  onScopeDispose(() => clearInterval(timer))
  const busy = ref<string | null>(null)
  const error = ref<string | null>(null)
  const feedback = ref<string | null>(null)
  const selected = shallowRef<TrashedTrip | null>(null)
  const purgeOpened = ref(false)
  const loadingSelected = ref(false)
  const selectionError = ref<string | null>(null)
  const purging = ref(false)
  const confirmed = ref(false)
  const password = ref('')
  const purgeError = ref<string | null>(null)
  const intents = new Map<string, ReturnType<typeof createWriteIntent>>()
  let selectionGeneration = 0

  function operationKey(action: string, trip: TrashedTrip) {
    const slot = `${action}:${trip.id}`
    const intent = intents.get(slot) ?? createWriteIntent()
    intents.set(slot, intent)
    return { intent, key: intent.key({ action, id: trip.id, version: trip.version }) }
  }

  async function restore(trip: TrashedTrip) {
    if (busy.value || purging.value) return false
    if (!canRestoreTrip(trip)) {
      error.value = '这趟旅行已无法恢复，请刷新查看最新状态。'
      return false
    }
    busy.value = trip.id
    error.value = null
    const operation = operationKey('restore', trip)
    try {
      const outcome = await restoreTrip(trip.id, trip.version, operation.key)
      operation.intent.reset()
      feedback.value = [
        '整趟旅行已恢复，可在旅行列表中查看。',
        ...writeWarnings(outcome.result),
      ].join(' ')
      await page.reload()
      return true
    } catch (cause) {
      error.value = actionError(cause, '网络连接中断，恢复结果尚未确认。可重试或刷新列表确认状态。')
      if (
        cause instanceof ApiError &&
        ['VERSION_CONFLICT', 'RESTORE_UNAVAILABLE'].includes(cause.code ?? '')
      )
        await page.reload()
      return false
    } finally {
      busy.value = null
    }
  }

  async function loadSelected() {
    if (!selected.value || !purgeOpened.value) return
    const request = ++selectionGeneration
    loadingSelected.value = true
    selectionError.value = null
    confirmed.value = false
    try {
      const trip = await getTrashedTrip(selected.value.id)
      if (request === selectionGeneration && purgeOpened.value) selected.value = trip
    } catch (cause) {
      if (request === selectionGeneration)
        selectionError.value = actionError(cause, '无法加载最新旅行，暂时不能请求永久清理')
    } finally {
      if (request === selectionGeneration) loadingSelected.value = false
    }
  }

  async function openPurge(trip: TrashedTrip) {
    if (purging.value || busy.value || trip.purge_requested_at) return
    selected.value = trip
    purgeOpened.value = true
    confirmed.value = false
    password.value = ''
    purgeError.value = null
    await loadSelected()
  }

  function closePurge() {
    if (purging.value) return
    selectionGeneration++
    purgeOpened.value = false
    password.value = ''
    confirmed.value = false
    loadingSelected.value = false
  }

  async function purge() {
    const trip = selected.value
    if (
      purging.value ||
      busy.value ||
      !purgeOpened.value ||
      !trip ||
      loadingSelected.value ||
      selectionError.value
    )
      return false
    purgeError.value = null
    if (trip.purge_requested_at) {
      purgeError.value = '永久清理已请求，无需重复提交。'
      return false
    }
    if (!confirmed.value) {
      purgeError.value = '请先确认整趟旅行及关联数据永久清理后无法恢复。'
      return false
    }
    if (!password.value) {
      purgeError.value = '请输入当前账号密码以完成验证。'
      return false
    }
    purging.value = true
    const operation = operationKey('purge', trip)
    try {
      await reauthenticate(password.value)
      password.value = ''
      const outcome = await purgeTrip(trip.id, trip.version, operation.key)
      operation.intent.reset()
      // 202 仅为请求受理。即便重放没有资源，也不能乐观地把它当成已删除或允许恢复。
      const requested = outcome.resource ?? {
        ...trip,
        purge_requested_at: new Date().toISOString(),
      }
      page.items.value = page.items.value.map((item) => (item.id === trip.id ? requested : item))
      selected.value = requested
      feedback.value = [
        '永久清理请求已受理，等待清理完成。这趟旅行已无法恢复。',
        ...writeWarnings(outcome.result),
      ].join(' ')
      purgeOpened.value = false
      confirmed.value = false
      await page.reload()
      return true
    } catch (cause) {
      purgeError.value = actionError(
        cause,
        '网络连接中断，清理请求结果尚未确认。重新验证密码后可重试同一请求。',
      )
      if (cause instanceof ApiError && cause.code === 'VERSION_CONFLICT') {
        await loadSelected()
        purgeError.value = '旅行已在其他设备修改，请检查最新内容并重新勾选确认。'
      }
      return false
    } finally {
      password.value = ''
      purging.value = false
    }
  }

  return {
    ...page,
    now,
    busy,
    error: page.error,
    actionError: error,
    feedback,
    selected,
    purgeOpened,
    loadingSelected,
    selectionError,
    purging,
    confirmed,
    password,
    purgeError,
    restore,
    openPurge,
    closePurge,
    loadSelected,
    purge,
  }
}
