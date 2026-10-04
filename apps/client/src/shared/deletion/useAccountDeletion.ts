import { computed, onScopeDispose, ref, shallowRef } from 'vue'
import { fetchAccount } from '@/shared/api/account'
import { logout, reauthenticate } from '@/shared/api/auth'
import {
  requestAccountDeletion,
  renewDeletionReceipt,
  type DeletionReceipt,
} from '@/shared/api/deletion'
import { ApiError } from '@/shared/api/problem'
import { randomId } from '@/shared/randomId'
import { useSessionStore } from '@/shared/stores/session'
import { deletionError } from './presentation'
import { clearDeletion, readDeletion, saveDeletion, type PendingDeletion } from './storage'

export function useAccountDeletion() {
  const session = useSessionStore()
  const busy = ref(false)
  const loading = ref(false)
  const error = ref<string | null>(null)
  const resetKey = ref(0)
  const pending = shallowRef<PendingDeletion | null>(null)
  let unsavedReceipt: DeletionReceipt | null = null
  let disposed = false
  onScopeDispose(() => {
    disposed = true
    unsavedReceipt = null
  })
  const canRecover = computed(
    () =>
      !!pending.value && session.account?.id === pending.value.accountId && session.isAuthenticated,
  )

  async function load() {
    const saved = readDeletion()
    if (saved?.kind === 'pending') {
      pending.value = saved
      return
    }
    loading.value = true
    error.value = null
    try {
      await fetchAccount()
    } catch (cause) {
      error.value = deletionError(cause)
    } finally {
      loading.value = false
    }
  }

  async function keepReceipt(receipt: DeletionReceipt) {
    const accountId = pending.value?.accountId
    if (!accountId || disposed || session.account?.id !== accountId) return false
    unsavedReceipt = receipt
    pending.value = { ...pending.value!, jobId: receipt.job_id }
    saveDeletion({ kind: 'receipt', accountId, receipt })
    // 必须完成写入及读回，再撤销普通会话。网络失败也不能恢复普通登录能力。
    try {
      await logout()
    } catch {
      /* 本地已清空；进度页不再走普通认证。 */
    }
    session.restored = true
    unsavedReceipt = null
    return true
  }

  async function submit(password: string) {
    if (busy.value || loading.value || !session.account || pending.value) return false
    busy.value = true
    error.value = null
    let sent = false
    try {
      await reauthenticate(password)
      const request: PendingDeletion = {
        kind: 'pending',
        accountId: session.account.id,
        version: session.account.version,
        operationId: randomId(),
      }
      saveDeletion(request)
      pending.value = request
      sent = true
      const receipt = await requestAccountDeletion(request.version, request.operationId)
      return await keepReceipt(receipt)
    } catch (cause) {
      error.value =
        cause instanceof Error && !(cause instanceof ApiError) && !sent
          ? cause.message
          : deletionError(cause)
      // 只有明确拒绝才能解除待确认状态；连接丢失和服务端异常均保留原幂等请求。
      if (
        cause instanceof ApiError &&
        cause.problem &&
        cause.problem.status < 500 &&
        cause.code !== 'ACCOUNT_DELETING'
      ) {
        clearDeletion()
        pending.value = null
        resetKey.value++
        if (cause.code === 'VERSION_CONFLICT') {
          await load()
          error.value = deletionError(cause)
        }
      }
      if (unsavedReceipt)
        error.value =
          '注销申请已受理，但查询凭证尚未可靠保存。请允许站点保存数据后点击重试保存，不要关闭页面。'
      return false
    } finally {
      busy.value = false
    }
  }

  async function recover() {
    const request = pending.value
    if (busy.value || !request || !canRecover.value) return false
    busy.value = true
    error.value = null
    try {
      const receipt =
        unsavedReceipt ??
        (request.jobId
          ? await renewDeletionReceipt(request.jobId)
          : await requestAccountDeletion(request.version, request.operationId))
      return await keepReceipt(receipt)
    } catch (cause) {
      error.value = unsavedReceipt
        ? '查询凭证仍无法保存，请检查浏览器存储权限后重试。'
        : deletionError(cause)
      if (cause instanceof ApiError && cause.code === 'REAUTH_REQUIRED') {
        clearDeletion()
        pending.value = null
        resetKey.value++
      }
      if (
        cause instanceof ApiError &&
        ['SESSION_EXPIRED', 'AUTH_REQUIRED'].includes(cause.code ?? '')
      )
        session.clear()
      return false
    } finally {
      busy.value = false
    }
  }

  return { busy, loading, error, resetKey, pending, canRecover, load, submit, recover }
}
