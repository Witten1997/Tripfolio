import type { LedgerCreate, LedgerEntry, LedgerKind, LedgerPatch } from '@/shared/api/ledger'
import { canonicalizeAmount } from '@/shared/money'
import { validDate } from '@/shared/travel/itineraryDraft'
import { DraftError } from '@/shared/travel/tripDraft'

/** 记账表单草稿：金额以字符串编辑，关联原支出用 '' 表示不关联。 */
export interface LedgerDraft {
  kind: LedgerKind
  amount: string
  split_count: number
  category_id: string
  occurred_on: string
  refunded_entry_id: string
  notes: string
}

/** 校验后的字段集合：契约创建正文去掉 id；金额始终携带币种。 */
export type LedgerValues = Omit<LedgerCreate, 'id'>

export const ledgerFieldLabels: Record<string, string> = {
  kind: '类型',
  amount: '金额',
  split_count: '均摊人数',
  category_id: '分类',
  occurred_on: '实际日期',
  refunded_entry_id: '关联原支出',
  notes: '备注',
}

export function emptyLedgerDraft(): LedgerDraft {
  return {
    kind: 'expense',
    amount: '',
    split_count: 1,
    category_id: '',
    occurred_on: '',
    refunded_entry_id: '',
    notes: '',
  }
}

export function ledgerDraftFrom(entry: LedgerEntry): LedgerDraft {
  return {
    kind: entry.kind,
    amount: entry.amount,
    split_count: entry.split_count,
    category_id: entry.category_id,
    occurred_on: entry.occurred_on,
    refunded_entry_id: entry.refunded_entry_id ?? '',
    notes: entry.notes,
  }
}

export interface TripMoney {
  currency: string
  minorUnits: number
}

/** 金额是否为正：规范化后去掉小数点与前导零应至少有一位非零数字。 */
function isPositive(canonical: string): boolean {
  return /[1-9]/.test(canonical)
}

export function validateLedgerDraft(draft: LedgerDraft, money: TripMoney): LedgerValues {
  const errors: Record<string, string> = {}
  let amount: string | null = null
  const rawAmount = draft.amount.trim()
  if (!rawAmount) {
    errors.amount = '请输入金额'
  } else {
    try {
      amount = canonicalizeAmount(rawAmount, money.minorUnits)
      if (!isPositive(amount)) errors.amount = '金额须大于 0'
    } catch (cause) {
      errors.amount = cause instanceof Error ? cause.message : '金额格式不正确'
    }
  }
  if (!draft.category_id) errors.category_id = '请选择账单分类'
  if (!Number.isInteger(draft.split_count) || draft.split_count < 1 || draft.split_count > 9999) {
    errors.split_count = '均摊人数须为 1 至 9999'
  }
  // 表单默认当天，日期必填：清空后 PATCH 会发出契约不允许的 null
  const occurredOn = draft.occurred_on.trim()
  if (!occurredOn) errors.occurred_on = '请选择实际日期'
  else if (!validDate(occurredOn)) errors.occurred_on = '请选择有效的实际日期'
  if (draft.notes.length > 4000) errors.notes = '备注最多 4000 个字符'
  // 关联原支出只对退款有意义
  const refunded = draft.kind === 'refund' ? draft.refunded_entry_id || null : null
  if (Object.keys(errors).length) throw new DraftError(errors)
  const values: LedgerValues = {
    kind: draft.kind,
    amount: amount!,
    split_count: draft.kind === 'refund' ? 1 : draft.split_count,
    currency_code: money.currency,
    category_id: draft.category_id,
    occurred_on: occurredOn,
    notes: draft.notes,
    refunded_entry_id: refunded,
  }
  return values
}

const patchFields = [
  'amount',
  'split_count',
  'category_id',
  'occurred_on',
  'notes',
  'refunded_entry_id',
] as const

/** kind 不可改，不进 PATCH；金额改动时按契约附带币种；refunded_entry_id 显式 null 表示解除关联。 */
export function changedLedgerFields(values: LedgerValues, baseline: LedgerEntry): LedgerPatch {
  const patch: Record<string, unknown> = {}
  for (const key of patchFields) {
    const next = values[key] ?? null
    const prev = baseline[key] ?? null
    if (next !== prev) patch[key] = next
  }
  if ('amount' in patch) patch.currency_code = values.currency_code
  return patch as LedgerPatch
}

/** 表单预览使用定点整数，按币种最小单位四舍五入。 */
export function splitPreview(amount: string, count: number, minorUnits: number): string | null {
  if (!Number.isInteger(count) || count < 1 || count > 9999) return null
  try {
    const canonical = canonicalizeAmount(amount.trim(), minorUnits)
    const digits = canonical.replace('.', '')
    const divisor = BigInt(count)
    const units = (BigInt(digits) + divisor / 2n) / divisor
    if (minorUnits === 0) return units.toString()
    const padded = units.toString().padStart(minorUnits + 1, '0')
    return `${padded.slice(0, -minorUnits)}.${padded.slice(-minorUnits)}`
  } catch {
    return null
  }
}
