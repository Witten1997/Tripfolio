import { computed, onScopeDispose, ref } from 'vue'

import { ApiError, requestEmailChallenge } from '@/shared/api/auth'

export type ChallengePurpose = 'register' | 'reset_password'

/**
 * 邮箱验证码申请与倒计时，注册与找回密码共用。
 * 服务端每邮箱 60 秒一次；成功后按返回的 retry_after_seconds 倒计时，429 时按 Retry-After 倒计时。
 */
export function useEmailChallenge(purpose: ChallengePurpose) {
  const challengeId = ref<string | null>(null)
  const sending = ref(false)
  const secondsLeft = ref(0)
  const error = ref<string | null>(null)
  let timer: ReturnType<typeof setInterval> | null = null

  const canSend = computed(() => !sending.value && secondsLeft.value === 0)

  function startCountdown(seconds: number) {
    stopCountdown()
    secondsLeft.value = Math.max(0, Math.floor(seconds))
    if (secondsLeft.value === 0) return
    timer = setInterval(() => {
      secondsLeft.value -= 1
      if (secondsLeft.value <= 0) stopCountdown()
    }, 1000)
  }

  function stopCountdown() {
    if (timer) clearInterval(timer)
    timer = null
    secondsLeft.value = 0
  }

  async function send(email: string): Promise<boolean> {
    if (!canSend.value) return false
    error.value = null
    sending.value = true
    try {
      const result = await requestEmailChallenge(purpose, email)
      challengeId.value = result.challenge_id
      startCountdown(result.retry_after_seconds)
      return true
    } catch (cause) {
      if (cause instanceof ApiError) {
        error.value = cause.message
        if (cause.code === 'RATE_LIMITED') startCountdown(60)
      } else {
        error.value = '网络错误，请稍后再试'
      }
      return false
    } finally {
      sending.value = false
    }
  }

  onScopeDispose(stopCountdown)

  return { challengeId, sending, secondsLeft, error, canSend, send }
}

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function isEmail(value: string): boolean {
  return EMAIL.test(value.trim()) && value.length <= 254
}

/** 与服务端一致：10–128 个字符（按 Unicode 码点计）。 */
export function isValidPassword(value: string): boolean {
  const n = Array.from(value).length
  return n >= 10 && n <= 128
}

export function isSixDigits(value: string): boolean {
  return /^[0-9]{6}$/.test(value)
}
