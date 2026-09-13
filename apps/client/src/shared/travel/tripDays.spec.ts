import { describe, expect, it } from 'vitest'

import type { ItineraryItem } from '@/shared/api/itinerary'
import { dayTitle, groupByDay, todayIn, tripDays } from '@/shared/travel/tripDays'

function item(overrides: Partial<ItineraryItem>): ItineraryItem {
  return {
    id: crypto.randomUUID(),
    trip_id: 't',
    title: 'x',
    kind: 'other',
    scheduled_on: '2026-10-01',
    sort_order: 0,
    planned_start_local: null,
    planned_end_local: null,
    planned_duration_minutes: null,
    place_name: '',
    address: '',
    latitude: null,
    longitude: null,
    estimated_amount: null,
    currency_code: null,
    notes: '',
    status: 'pending',
    actual_start_local: null,
    actual_end_local: null,
    actual_notes: '',
    version: '1',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    deleted_at: null,
    ...overrides,
  }
}

describe('tripDays', () => {
  it('标题格式为“月.日 周X”，不带前导零', () => {
    expect(dayTitle('2026-09-20')).toBe('9.20 周日')
    expect(dayTitle('2026-10-01')).toBe('10.1 周四')
  })

  it('按起止日期生成每一天（含两端）', () => {
    const days = tripDays('2026-10-01', '2026-10-03')
    expect(days.map((d) => d.date)).toEqual(['2026-10-01', '2026-10-02', '2026-10-03'])
    expect(days.every((d) => !d.outside)).toBe(true)
  })

  it('旅行日期外的项目归入额外显示的天并标记 outside，按日期排序', () => {
    const before = item({ scheduled_on: '2026-09-30' })
    const after = item({ scheduled_on: '2026-10-05' })
    const inside = item({ scheduled_on: '2026-10-02', sort_order: 1 })
    const inside0 = item({ scheduled_on: '2026-10-02', sort_order: 0 })
    const days = groupByDay('2026-10-01', '2026-10-03', [after, inside, before, inside0])
    expect(days.map((d) => d.date)).toEqual([
      '2026-09-30',
      '2026-10-01',
      '2026-10-02',
      '2026-10-03',
      '2026-10-05',
    ])
    expect(days[0]!.outside).toBe(true)
    expect(days[4]!.outside).toBe(true)
    expect(days[2]!.items.map((i) => i.id)).toEqual([inside0.id, inside.id])
    expect(days[1]!.items).toEqual([])
  })

  it('同日同 sort_order 时按 id 稳定排序', () => {
    const a = item({ id: 'a', scheduled_on: '2026-10-01', sort_order: 0 })
    const b = item({ id: 'b', scheduled_on: '2026-10-01', sort_order: 0 })
    const days = groupByDay('2026-10-01', '2026-10-01', [b, a])
    expect(days[0]!.items.map((i) => i.id)).toEqual(['a', 'b'])
  })

  it('todayIn 按旅行时区取日期', () => {
    const at = new Date('2026-09-13T16:30:00Z')
    expect(todayIn('Asia/Tokyo', at)).toBe('2026-09-14')
    expect(todayIn('America/Los_Angeles', at)).toBe('2026-09-13')
    expect(todayIn('Not/AZone', at)).toBe('2026-09-13')
  })
})
