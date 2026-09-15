import { describe, expect, it } from 'vitest'

import type { CategoryTotals, DailyTotals } from '@/shared/api/statistics'
import {
  categoryAmountRows,
  dailyBars,
  formatMoney,
  formatShare,
  pieSlices,
  sumMoney,
} from '@/shared/travel/statisticsView'

function cat(overrides: Partial<CategoryTotals> & { category_id: string }): CategoryTotals {
  return {
    name: overrides.category_id,
    icon: null,
    expense_amount: '0.00',
    refund_amount: '0.00',
    net_amount: '0.00',
    share: null,
    trip_category_net_amount: '0.00',
    ...overrides,
  }
}

describe('categoryAmountRows', () => {
  it('只展示当前筛选范围有记录的分类，不用整趟净额判断是否有记录', () => {
    const input = [
      cat({ category_id: 'empty' }),
      cat({ category_id: 'outside-range', trip_category_net_amount: '300.00' }),
      cat({ category_id: 'dining', expense_amount: '60.00', net_amount: '60.00' }),
      cat({ category_id: 'transport', expense_amount: '200.00', net_amount: '200.00' }),
    ]
    expect(categoryAmountRows(input).map((row) => row.category_id)).toEqual(['transport', 'dining'])
    expect(input.map((row) => row.category_id)).toEqual([
      'empty',
      'outside-range',
      'dining',
      'transport',
    ])
  })

  it('保留全额退款的零净额分类、只有退款及已删除但被引用的分类', () => {
    const refunded = cat({
      category_id: 'refunded',
      expense_amount: '100.00',
      refund_amount: '100.00',
      net_amount: '0.00',
    })
    const refundOnly = cat({
      category_id: 'refund-only',
      refund_amount: '50.00',
      net_amount: '-50.00',
    })
    const deleted = cat({
      category_id: 'deleted',
      name: '已删除分类',
      expense_amount: '0.0001',
      net_amount: '0.0001',
    })
    expect(categoryAmountRows([refundOnly, refunded, deleted])).toEqual([
      deleted,
      refunded,
      refundOnly,
    ])
  })

  it('没有记录时返回空列表', () => {
    expect(categoryAmountRows([])).toEqual([])
    expect(categoryAmountRows([cat({ category_id: 'empty' })])).toEqual([])
  })
})

describe('formatMoney', () => {
  it('按千分位分组并保留服务端小数位，负号在最前', () => {
    expect(formatMoney('1234.50')).toBe('1,234.50')
    expect(formatMoney('999')).toBe('999')
    expect(formatMoney('1234567.89')).toBe('1,234,567.89')
    expect(formatMoney('-1234.50')).toBe('-1,234.50')
    expect(formatMoney('0.00')).toBe('0.00')
  })
})

describe('sumMoney', () => {
  it('按定点整数精确相加，不引入浮点误差', () => {
    expect(sumMoney(['0.10', '0.20'])).toBe('0.30')
    expect(sumMoney(['1234.56', '1.44'])).toBe('1236.00')
    expect(sumMoney(['100', '23'])).toBe('123')
    expect(sumMoney(['5.00', '-8.50'])).toBe('-3.50')
    expect(sumMoney([])).toBe('0')
  })
})

describe('pieSlices', () => {
  it('只取净额为正的分类，按净额降序，配色下标从 1 开始', () => {
    const slices = pieSlices([
      cat({ category_id: 'a', name: '交通', net_amount: '100.00', share: 0.25 }),
      cat({ category_id: 'b', name: '美食', net_amount: '300.00', share: 0.75 }),
      cat({ category_id: 'c', name: '购物', net_amount: '0.00' }),
      cat({ category_id: 'd', name: '退款多', net_amount: '-50.00' }),
    ])
    expect(slices.map((s) => s.name)).toEqual(['美食', '交通'])
    expect(slices.map((s) => s.colorIndex)).toEqual([1, 2])
    expect(slices[0]!.value).toBe(300)
    expect(slices[0]!.amount).toBe('300.00')
  })

  it('超过上限时末尾分类折叠成中性“其他分类”切片，金额精确求和', () => {
    const many = Array.from({ length: 8 }, (_, i) =>
      cat({
        category_id: `c${i}`,
        name: `分类${i}`,
        net_amount: `${(8 - i) * 10}.10`,
        share: 0.1,
      }),
    )
    const slices = pieSlices(many, 6)
    expect(slices).toHaveLength(6)
    expect(slices.slice(0, 5).map((s) => s.colorIndex)).toEqual([1, 2, 3, 4, 5])
    const other = slices[5]!
    expect(other.key).toBe('__other__')
    expect(other.colorIndex).toBe(0)
    expect(other.name).toBe('其他 3 个分类')
    // 折叠了 30.10 + 20.10 + 10.10
    expect(other.amount).toBe('60.30')
    expect(other.share).toBeCloseTo(0.3)
  })

  it('折叠项里只要有一个没有占比，整体占比为 null', () => {
    const many = Array.from({ length: 7 }, (_, i) =>
      cat({ category_id: `c${i}`, net_amount: `${7 - i}.00`, share: i === 6 ? null : 0.1 }),
    )
    expect(pieSlices(many, 6)[5]!.share).toBeNull()
  })

  it('没有正净额时返回空，交由页面展示金额明细', () => {
    expect(pieSlices([cat({ category_id: 'a', net_amount: '-1.00' })])).toEqual([])
  })
})

describe('dailyBars', () => {
  it('把服务端的日期降序改为升序绘图，净额可为负', () => {
    const daily: DailyTotals[] = [
      { date: '2026-10-03', expense_amount: '0.00', refund_amount: '50.00', net_amount: '-50.00' },
      { date: '2026-10-01', expense_amount: '80.00', refund_amount: '0.00', net_amount: '80.00' },
    ]
    expect(dailyBars(daily)).toEqual([
      { date: '2026-10-01', net: 80, amount: '80.00' },
      { date: '2026-10-03', net: -50, amount: '-50.00' },
    ])
  })
})

describe('formatShare', () => {
  it('占比转百分比，缺失时为 null', () => {
    expect(formatShare(0.2367)).toBe('23.7%')
    expect(formatShare(null)).toBeNull()
    expect(formatShare(undefined)).toBeNull()
  })
})
