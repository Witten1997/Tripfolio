import { describe, expect, it } from 'vitest'

import type { PackingItem } from '@/shared/api/packing'
import { packingGroups, packingProgress } from '@/shared/travel/packingView'
import { packingStatusOrder, packingStatusLabels } from '@/shared/api/packing'

const noFilters = { category: '', status: '' } as const
function item(overrides: Partial<PackingItem>): PackingItem {
  return {
    id: crypto.randomUUID(),
    trip_id: 'trip',
    name: '测试物品',
    category: 'other',
    quantity: 1,
    notes: '',
    status: 'pending',
    version: '1',
    created_at: '2026-09-14T00:00:00Z',
    updated_at: '2026-09-14T00:00:00Z',
    deleted_at: null,
    ...overrides,
  }
}

describe('packingGroups', () => {
  it('界面只有两态，旧已装包与已备齐归入同一筛选和准备进度', () => {
    const input = [
      item({ status: 'packed' }),
      item({ status: 'ready' }),
      item({ status: 'pending' }),
    ]
    expect(packingStatusOrder).toEqual(['pending', 'ready'])
    expect(packingStatusLabels.packed).toBe('已准备')
    expect(
      packingGroups(input, { category: '', status: 'ready' }).flatMap((group) => group.items),
    ).toEqual(input.slice(0, 2))
    expect(packingProgress(input)).toEqual({ total: 3, ready: 2, percent: 67 })
    expect(packingProgress([])).toEqual({ total: 0, ready: 0, percent: 0 })
  })
  it('只显示有物品的大类并保持预设顺序，不改变原列表顺序', () => {
    const charger = item({ name: '充电器', category: 'electronics' })
    const passport = item({ name: '护照', category: 'documents' })
    const phone = item({ name: '手机', category: 'electronics' })
    const input = [charger, passport, phone]
    const groups = packingGroups(input, noFilters)
    expect(groups.map((group) => group.category)).toEqual(['documents', 'electronics'])
    expect(groups[0]!.items).toEqual([passport])
    expect(groups[1]!.items).toEqual([charger, phone])
    expect(input).toEqual([charger, passport, phone])
  })

  it('分类及状态筛选后隐藏空组，没有匹配时留给页面展示筛选空态', () => {
    const passport = item({ category: 'documents', status: 'packed' })
    const charger = item({ category: 'electronics', status: 'pending' })
    const input = [passport, charger]
    expect(
      packingGroups(input, { category: '', status: 'pending' }).map((g) => g.category),
    ).toEqual(['electronics'])
    expect(packingGroups(input, { category: 'documents', status: '' })[0]!.items).toEqual([
      passport,
    ])
    expect(packingGroups(input, { category: 'documents', status: 'pending' })).toEqual([])
    expect(packingGroups(input, { category: 'food', status: '' })).toEqual([])
    expect(packingGroups([], noFilters)).toEqual([])
  })

  it('添加到新大类时出现卡片，移走或删除该大类最后一件物品后卡片消失', () => {
    const passport = item({ category: 'documents' })
    const medicine = item({ category: 'medicine' })
    expect(packingGroups([passport, medicine], noFilters).map((g) => g.category)).toEqual([
      'documents',
      'medicine',
    ])
    expect(packingGroups([passport], noFilters).map((g) => g.category)).toEqual(['documents'])
    expect(
      packingGroups([{ ...passport, category: 'other' }], noFilters).map((g) => g.category),
    ).toEqual(['other'])
  })
})
