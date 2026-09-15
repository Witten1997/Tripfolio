import { ref } from 'vue'

import { platform } from '@/platform'
import {
  disableTripShare,
  enableTripShare,
  getTripShare,
  rotateTripShare,
  type TripShare,
} from '@/shared/api/shares'
import { actionError } from '@/shared/api/writes'

export interface TripShareDeps {
  get: (tripId: string) => Promise<TripShare | null>
  enable: (tripId: string) => Promise<TripShare>
  rotate: (tripId: string) => Promise<TripShare>
  disable: (tripId: string) => Promise<void>
  copy: (text: string) => Promise<void>
}

const defaultDeps: TripShareDeps = {
  get: getTripShare,
  enable: enableTripShare,
  rotate: rotateTripShare,
  disable: disableTripShare,
  copy: (text) => platform.clipboard.writeText(text),
}

export type TripShareStatus = 'idle' | 'loading' | 'off' | 'on' | 'error'

/** 分享对话框的状态与动作；网络与剪贴板经依赖注入，便于测试。 */
export function useTripShare(tripId: string, deps: TripShareDeps = defaultDeps) {
  const status = ref<TripShareStatus>('idle')
  const share = ref<TripShare | null>(null)
  const busy = ref(false)
  const error = ref<string | null>(null)
  const copied = ref(false)
  let generation = 0

  async function load() {
    const request = ++generation
    status.value = 'loading'
    error.value = null
    copied.value = false
    try {
      const loaded = await deps.get(tripId)
      if (request !== generation) return
      share.value = loaded
      status.value = loaded ? 'on' : 'off'
    } catch (cause) {
      if (request !== generation) return
      error.value = actionError(cause, '无法读取分享状态，请稍后重试')
      status.value = 'error'
    }
  }

  async function mutate(action: () => Promise<TripShare | null>, fallback: string) {
    if (busy.value) return
    busy.value = true
    error.value = null
    copied.value = false
    try {
      const next = await action()
      share.value = next
      status.value = next ? 'on' : 'off'
    } catch (cause) {
      error.value = actionError(cause, fallback)
    } finally {
      busy.value = false
    }
  }

  const enable = () => mutate(() => deps.enable(tripId), '无法生成分享链接，请稍后重试')
  const rotate = () => mutate(() => deps.rotate(tripId), '无法重新生成链接，请稍后重试')
  const disable = () =>
    mutate(async () => {
      await deps.disable(tripId)
      return null
    }, '无法关闭分享，请稍后重试')

  async function copy() {
    if (!share.value) return
    try {
      await deps.copy(share.value.url)
      copied.value = true
    } catch {
      error.value = '复制失败，请手动选中链接复制'
    }
  }

  return { status, share, busy, error, copied, load, enable, rotate, disable, copy }
}
