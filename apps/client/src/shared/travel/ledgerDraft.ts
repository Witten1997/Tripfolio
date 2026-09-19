import type { LedgerCreate, LedgerEntry, LedgerKind, LedgerPatch } from '@/shared/api/ledger'
import type { TripMember } from '@/shared/api/members'
import { canonicalizeAmount } from '@/shared/money'
import { validDate } from '@/shared/travel/itineraryDraft'
import { DraftError } from '@/shared/travel/tripDraft'

export type SplitMode = LedgerEntry['split_mode']

export const splitModeLabels: Record<SplitMode, string> = {
  personal: '个人',
  even: '均摊',
  ratio: '按比例',
}

/** 记账表单草稿：金额以字符串编辑，关联原支出用 '' 表示不关联。 */
export interface LedgerDraft {
  kind: LedgerKind
  amount: string
  category_id: string
  occurred_on: string
  refunded_entry_id: string
  notes: string
  payer_member_id: string
  split_mode: SplitMode
  participant_member_ids: string[]
}

/** 校验后的字段集合：契约创建正文去掉 id；金额始终携带币种。 */
export type LedgerValues = Omit<LedgerCreate, 'id'>

export const ledgerFieldLabels: Record<string, string> = {
  kind: '类型',
  amount: '金额',
  category_id: '分类',
  occurred_on: '实际日期',
  refunded_entry_id: '关联原支出',
  notes: '备注',
  payer_member_id: '付款人',
  split_mode: '分摊模式',
  participant_member_ids: '参与人',
}

export function emptyLedgerDraft(): LedgerDraft {
  return {
    kind: 'expense',
    amount: '',
    category_id: '',
    occurred_on: '',
    refunded_entry_id: '',
    notes: '',
    payer_member_id: '',
    split_mode: 'personal',
    participant_member_ids: [],
  }
}

export function ledgerDraftFrom(entry: LedgerEntry): LedgerDraft {
  return {
    kind: entry.kind,
    amount: entry.amount,
    category_id: entry.category_id,
    occurred_on: entry.occurred_on,
    refunded_entry_id: entry.refunded_entry_id ?? '',
    notes: entry.notes,
    payer_member_id: entry.payer_member_id,
    split_mode: entry.split_mode,
    participant_member_ids: entry.splits.map((s) => s.member_id),
  }
}

/** 新建时的成员默认：付款人为「我」，参与人为全员。 */
export function defaultMemberDraft(members: readonly TripMember[]): Partial<LedgerDraft> {
  const self = members.find((m) => m.is_self) ?? members[0]
  return {
    payer_member_id: self?.id ?? '',
    participant_member_ids: members.map((m) => m.id),
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
  // 表单默认当天，日期必填：清空后 PATCH 会发出契约不允许的 null
  const occurredOn = draft.occurred_on.trim()
  if (!occurredOn) errors.occurred_on = '请选择实际日期'
  else if (!validDate(occurredOn)) errors.occurred_on = '请选择有效的实际日期'
  if (draft.notes.length > 4000) errors.notes = '备注最多 4000 个字符'
  if (!draft.payer_member_id) errors.payer_member_id = '请选择付款人'
  const participants =
    draft.split_mode === 'personal'
      ? [draft.payer_member_id]
      : [...new Set(draft.participant_member_ids)]
  if (participants.length < 1) errors.participant_member_ids = '至少选择一位参与人'
  // 关联原支出只对退款有意义
  const refunded = draft.kind === 'refund' ? draft.refunded_entry_id || null : null
  if (Object.keys(errors).length) throw new DraftError(errors)
  const values: LedgerValues = {
    kind: draft.kind,
    amount: amount!,
    currency_code: money.currency,
    category_id: draft.category_id,
    occurred_on: occurredOn,
    notes: draft.notes,
    refunded_entry_id: refunded,
    payer_member_id: draft.payer_member_id,
    split_mode: draft.split_mode,
    participant_member_ids: participants,
  }
  return values
}

const patchFields = [
  'amount',
  'category_id',
  'occurred_on',
  'notes',
  'refunded_entry_id',
  'payer_member_id',
  'split_mode',
] as const

function sameIds(left: readonly string[], right: readonly string[]) {
  return left.length === right.length && left.every((id, i) => id === right[i])
}

/** kind 不可改，不进 PATCH；金额改动时按契约附带币种；refunded_entry_id 显式 null 表示解除关联；参与人整体替换。 */
export function changedLedgerFields(values: LedgerValues, baseline: LedgerEntry): LedgerPatch {
  const patch: Record<string, unknown> = {}
  for (const key of patchFields) {
    const next = values[key] ?? null
    const prev = baseline[key] ?? null
    if (next !== prev) patch[key] = next
  }
  if ('amount' in patch) patch.currency_code = values.currency_code
  const participants = values.participant_member_ids ?? []
  if (
    !sameIds(
      participants,
      baseline.splits.map((s) => s.member_id),
    )
  ) {
    patch.participant_member_ids = participants
  }
  return patch as LedgerPatch
}

/**
 * 表单预览：与服务端相同的分摊算法（最小单位向下取整，余数按参与人顺序补齐）。
 * 返回 member_id → 份额；金额非法或参与人为空时返回 null。
 */
export function splitPreview(
  amount: string,
  mode: SplitMode,
  participants: readonly TripMember[],
  minorUnits: number,
): Map<string, string> | null {
  if (mode === 'personal') participants = participants.filter((m) => m.is_self)
  if (!participants.length) return null
  let canonical: string
  try {
    canonical = canonicalizeAmount(amount.trim(), minorUnits)
  } catch {
    return null
  }
  const total = BigInt(canonical.replace('.', ''))
  const n = BigInt(participants.length)
  let shares: bigint[]
  if (mode === 'ratio') {
    const weights = participants.map((m) => BigInt(Math.round(Number(m.share_percent) * 100)))
    const sum = weights.reduce((acc, w) => acc + w, 0n)
    if (sum === 0n) return null
    shares = weights.map((w) => (total * w) / sum)
  } else {
    shares = participants.map(() => total / n)
  }
  let remainder = total - shares.reduce((acc, s) => acc + s, 0n)
  for (let i = 0; remainder > 0n && i < shares.length; i++) {
    shares[i] += 1n
    remainder -= 1n
  }
  const format = (units: bigint) => {
    if (minorUnits === 0) return units.toString()
    const padded = units.toString().padStart(minorUnits + 1, '0')
    return `${padded.slice(0, -minorUnits)}.${padded.slice(-minorUnits)}`
  }
  return new Map(participants.map((m, i) => [m.id, format(shares[i]!)]))
}
