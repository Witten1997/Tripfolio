import type { CategoryTotals, DailyTotals } from '@/shared/api/statistics'

/**
 * 金额只用于展示，不参与结算：按千分位分组，保留服务端已规范化的小数位。
 * 传入的是契约 Money/SignedMoney 字符串（可能带负号）。
 */
export function formatMoney(amount: string): string {
  const negative = amount.startsWith('-')
  const unsigned = negative ? amount.slice(1) : amount
  const [integer = '0', fraction] = unsigned.split('.')
  const grouped = integer.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${negative ? '-' : ''}${grouped}${fraction ? `.${fraction}` : ''}`
}

/** 图表数值：金额字符串转数字仅用于绘图比例，展示金额仍用 formatMoney。 */
export function toNumber(amount: string): number {
  const value = Number(amount)
  return Number.isFinite(value) ? value : 0
}

/** 精确求和同币种金额：按最大小数位当定点整数相加，避免浮点误差进入展示金额。 */
export function sumMoney(amounts: readonly string[]): string {
  if (!amounts.length) return '0'
  const scale = Math.max(...amounts.map((a) => a.split('.')[1]?.length ?? 0))
  const total = amounts.reduce((sum, amount) => {
    const negative = amount.startsWith('-')
    const [integer = '0', fraction = ''] = (negative ? amount.slice(1) : amount).split('.')
    const scaled = BigInt(integer + fraction.padEnd(scale, '0'))
    return sum + (negative ? -scaled : scaled)
  }, 0n)
  if (scale === 0) return total.toString()
  const negative = total < 0n
  const digits = (negative ? -total : total).toString().padStart(scale + 1, '0')
  return `${negative ? '-' : ''}${digits.slice(0, -scale)}.${digits.slice(-scale)}`
}

export interface PieSlice {
  key: string
  name: string
  /** 净额（正数）用于饼图比例。 */
  value: number
  /** 展示用净额字符串。 */
  amount: string
  share: number | null
  /** 1–6 指向 --tf-chart-N；0 表示折叠出来的“其他分类”，用中性色。 */
  colorIndex: number
}

export const MAX_PIE_SLICES = 6

/**
 * 饼图切片：只取净额为正的分类，按净额降序；超过 MAX_PIE_SLICES 时，
 * 末尾若干类折叠成“其他分类”中性切片，避免循环用色或产生看不清的碎片。
 * 每个切片都直接标注分类名，身份不靠颜色单独承载。
 */
export function pieSlices(byCategory: readonly CategoryTotals[], max = MAX_PIE_SLICES): PieSlice[] {
  const positive = byCategory
    .filter((c) => toNumber(c.net_amount) > 0)
    .sort((a, b) => toNumber(b.net_amount) - toNumber(a.net_amount))
  if (positive.length <= max) {
    return positive.map((c, index) => ({
      key: c.category_id,
      name: c.name,
      value: toNumber(c.net_amount),
      amount: c.net_amount,
      share: c.share ?? null,
      colorIndex: index + 1,
    }))
  }
  const colored = positive.slice(0, max - 1)
  const folded = positive.slice(max - 1)
  const slices: PieSlice[] = colored.map((c, index) => ({
    key: c.category_id,
    name: c.name,
    value: toNumber(c.net_amount),
    amount: c.net_amount,
    share: c.share ?? null,
    colorIndex: index + 1,
  }))
  const foldedAmount = sumMoney(folded.map((c) => c.net_amount))
  const foldedShare = folded.every((c) => c.share !== null)
    ? folded.reduce((sum, c) => sum + (c.share ?? 0), 0)
    : null
  slices.push({
    key: '__other__',
    name: `其他 ${folded.length} 个分类`,
    value: toNumber(foldedAmount),
    amount: foldedAmount,
    share: foldedShare,
    colorIndex: 0,
  })
  return slices
}

export interface DailyBar {
  date: string
  net: number
  amount: string
}

/** 每日柱状：服务端按日期降序返回，绘图改为升序（时间轴从左到右）。 */
export function dailyBars(daily: readonly DailyTotals[]): DailyBar[] {
  return [...daily]
    .sort((a, b) => (a.date < b.date ? -1 : a.date > b.date ? 1 : 0))
    .map((d) => ({ date: d.date, net: toNumber(d.net_amount), amount: d.net_amount }))
}

/** 占比展示为百分比字符串；无占比（ratio_available=false）返回 null。 */
export function formatShare(share: number | null | undefined): string | null {
  if (share === null || share === undefined) return null
  return `${(share * 100).toFixed(1)}%`
}
