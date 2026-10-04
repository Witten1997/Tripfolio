import type { DeletionJob } from '@/shared/api/deletion'
import { ApiError } from '@/shared/api/problem'

export const deletionStages: Record<DeletionJob['stage'], string> = {
  revoke_access: '停止访问与分享',
  remove_objects: '清理照片和附件',
  remove_rows: '清理旅行与账号数据',
  finalize: '核对清理结果',
  done: '清理完成',
}
export const deletionStatuses: Record<DeletionJob['status'], string> = {
  queued: '申请已受理',
  running: '正在清理',
  completed: '清理已完成',
  failed: '清理失败',
}

export function deletionError(cause: unknown): string {
  if (!(cause instanceof ApiError)) return '网络连接中断，结果尚未确认。请重试查询，不要重复申请。'
  switch (cause.code) {
    case 'ADMIN_LAST_AVAILABLE':
      return '当前账号是最后一位超级管理员，请先安排其他超级管理员后再注销。'
    case 'VERSION_CONFLICT':
      return '账号资料已发生变化，请核对最新账号信息后重新确认注销。'
    case 'REAUTH_REQUIRED':
      return '身份复验已失效，请重新输入当前密码并确认。'
    case 'INVALID_CREDENTIALS':
      return '当前密码不正确，请重新输入。'
    case 'ACCOUNT_DELETING':
      return '账号已进入注销流程，请恢复已有任务，不要再次申请。'
    case 'AUTH_REQUIRED':
    case 'SESSION_EXPIRED':
      return '原登录会话已失效，不能补领查询凭证。已有注销申请不会因此撤销。'
    default:
      return cause.message
  }
}

export function deletionProgress(job: DeletionJob): number | null {
  if (job.status === 'completed') return 100
  if (job.total_items === null || job.total_items <= 0) return null
  return Math.min(99, Math.max(0, Math.floor((job.processed_items / job.total_items) * 100)))
}

export function deletionJobHint(job: DeletionJob): string | null {
  switch (job.error_code) {
    case 'RECEIPT_RECOVERY_WINDOW':
      return '申请已受理，正在保留短暂的查询凭证恢复窗口，随后继续清理。'
    case 'UPLOAD_AUTHORIZATION_ACTIVE':
      return '正在等待已签发的上传授权失效，之后继续清理照片和附件。'
    default:
      return job.error_code
        ? `${job.status === 'failed' ? '失败' : '状态'}代码：${job.error_code}`
        : null
  }
}
