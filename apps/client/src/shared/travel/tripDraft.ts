import type { Trip, TripPatch } from '@/shared/api/trips'
import { canonicalizeAmount } from '@/shared/money'
import type { Metadata } from '@/shared/stores/metadata'

export const tripFieldLabels = {
  name: '旅行名称',
  start_date: '开始日期',
  end_date: '结束日期',
  destination: '目的地',
  notes: '备注',
  timezone: '旅行时区',
  currency_code: '记账币种',
  budget_amount: '总预算',
}

export type TripField = keyof typeof tripFieldLabels
export type TripValues = Pick<Trip, TripField>
export type TripDraft = Omit<TripValues, 'budget_amount'> & { budget_amount: string }

export function emptyTripDraft(timezone = 'Asia/Shanghai', currency = 'CNY'): TripDraft {
  return {
    name: '',
    start_date: '',
    end_date: '',
    destination: '',
    notes: '',
    timezone,
    currency_code: currency,
    budget_amount: '',
  }
}

export function draftFromTrip(trip: Trip): TripDraft {
  return {
    name: trip.name,
    start_date: trip.start_date,
    end_date: trip.end_date,
    destination: trip.destination,
    notes: trip.notes,
    timezone: trip.timezone,
    currency_code: trip.currency_code,
    budget_amount: trip.budget_amount ?? '',
  }
}

export class DraftError extends Error {
  constructor(readonly fields: Record<string, string>) {
    super(Object.values(fields)[0] ?? '请检查填写内容')
  }
}

function validDate(value: string) {
  if (!/^[0-9]{4}-[0-9]{2}-[0-9]{2}$/.test(value) || value.startsWith('0000')) return false
  const date = new Date(`${value}T00:00:00Z`)
  return Number.isFinite(date.valueOf()) && date.toISOString().slice(0, 10) === value
}

/** 不用浮点数处理预算；缺省与零分别输出 null 与规范金额字符串。 */
export function validateTripDraft(draft: TripDraft, metadata: Metadata | null): TripValues {
  const errors: Record<string, string> = {}
  const name = draft.name.trim()
  const destination = draft.destination.trim()
  const timezone = draft.timezone.trim()
  if (!name || name.length > 120) errors.name = '请输入 1–120 个字符的旅行名称'
  if (destination.length > 300) errors.destination = '目的地最多 300 个字符'
  if (draft.notes.length > 10000) errors.notes = '备注最多 10000 个字符'
  if (!validDate(draft.start_date)) errors.start_date = '请选择有效的开始日期'
  if (!validDate(draft.end_date)) errors.end_date = '请选择有效的结束日期'
  if (!errors.start_date && !errors.end_date && draft.end_date < draft.start_date) {
    errors.end_date = '结束日期不能早于开始日期'
  }
  try {
    if (!timezone || timezone.length > 64) throw new Error()
    new Intl.DateTimeFormat('zh-CN', { timeZone: timezone }).format()
  } catch {
    errors.timezone = '请输入有效的 IANA 时区，例如 Asia/Shanghai'
  }

  const currency = metadata?.currencies.find((item) => item.code === draft.currency_code)
  let budget: string | null = null
  if (!currency) {
    errors.currency_code = '请先加载币种信息并选择支持的币种'
  } else if (draft.budget_amount !== '') {
    try {
      budget = canonicalizeAmount(draft.budget_amount, currency.minor_units)
    } catch (cause) {
      errors.budget_amount = cause instanceof Error ? cause.message : '预算金额格式不正确'
    }
  }
  if (Object.keys(errors).length) throw new DraftError(errors)
  return {
    ...draft,
    name,
    destination,
    timezone,
    budget_amount: budget,
  }
}

/** 固定字段白名单，保留显式 null；绝不把列表资源整体回传。 */
export function changedTripFields(values: TripValues, baseline: Trip): TripPatch {
  return Object.fromEntries(
    (Object.keys(tripFieldLabels) as TripField[])
      .filter((key) => values[key] !== baseline[key])
      .map((key) => [key, values[key]]),
  )
}
