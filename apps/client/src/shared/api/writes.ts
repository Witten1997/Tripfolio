import type { components } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'

export type WriteResult = components['schemas']['WriteResult']

export interface WriteOutcome<T> {
  result: WriteResult
  resource: T | null
}

/** 写入重放可能只剩操作回执，不能把 nullable data 当成当前资源使用。 */
export function writeOutcome<T>(
  result: WriteResult,
  isResource: (value: Record<string, unknown>) => boolean,
): WriteOutcome<T> {
  return {
    result,
    resource: result.data && isResource(result.data) ? (result.data as T) : null,
  }
}

function fingerprint(value: unknown): string {
  return JSON.stringify(value, (_, item: unknown) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) return item
    return Object.fromEntries(
      Object.entries(item).sort(([left], [right]) => (left < right ? -1 : left > right ? 1 : 0)),
    )
  })
}

/** 只存内存；网络结果不明时，相同请求沿用操作编号，正文或版本变化则换号。 */
export function createWriteIntent() {
  let previous: { signature: string; id: string } | null = null
  return {
    key(request: unknown): string {
      const signature = fingerprint(request)
      if (!previous || previous.signature !== signature) {
        previous = { signature, id: crypto.randomUUID() }
      }
      return previous.id
    },
    reset() {
      previous = null
    },
  }
}

export function versionHeaders(operationId: string, version: string) {
  return { 'Idempotency-Key': operationId, 'If-Match': `"${version}"` }
}

const warningLabels: Record<string, string> = {
  MERGED_WITH_NEWER_VERSION: '已保留其他设备的修改，并合并本次改动。',
  REFUNDS_UNLINKED: '关联到这笔支出的退款已解除关联并保留为独立退款。',
  ITINERARY_OUTSIDE_TRIP_DATES: '已有行程超出新的旅行日期；记录已保留，请检查并调整安排。',
  TIMEZONE_INTERPRETATION_CHANGED: '旅行时区已更改；已有行程的当地时间会按新时区解释，请检查安排。',
}

export function writeWarnings(result: WriteResult): string[] {
  return result.warnings.map((warning) => warningLabels[warning] ?? warning)
}

export function actionError(cause: unknown, fallback = '网络连接失败，请重试'): string {
  if (!(cause instanceof ApiError)) return fallback
  switch (cause.code) {
    case 'CURRENCY_LOCKED':
      return '这趟旅行已有账目，记账币种已锁定。删除全部有效账目后才可更改币种。'
    case 'CURRENCY_AMOUNTS_EXIST':
      return '请先清空总预算并保存，同时清空行程中的预计费用，再更改币种；已有金额不会自动换算。'
    case 'CATEGORY_IN_USE':
      return '这个分类仍被账目使用，暂时不能删除。可以改名、调整排序，或先修改相关账目的分类。'
    case 'VERSION_CONFLICT':
      return '记录已在其他设备修改。请检查最新内容后再操作。'
    case 'RESTORE_UNAVAILABLE':
      return '旅行已超过恢复截止时间，或已请求永久清理，无法恢复。'
    case 'TRIP_DELETED':
      return '这趟旅行已进入回收站，请到回收站查看。'
    case 'RESOURCE_GONE':
      return '这条记录已被删除。'
    case 'ORDER_CHANGED':
      return '这些日期的行程已经变化，请刷新后重新排序。'
    case 'CURRENCY_MISMATCH':
      return '预计费用的币种必须与旅行币种一致。'
    default:
      return cause.message
  }
}

export function fieldErrors(cause: unknown): Record<string, string> {
  if (!(cause instanceof ApiError)) return {}
  return Object.fromEntries(cause.problem?.errors?.map((item) => [item.field, item.message]) ?? [])
}
