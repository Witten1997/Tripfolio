import { describe, expect, it } from 'vitest'

import type { PublicItineraryItem, PublicRoutes } from './api'
import { buildSharedMap } from './sharedRoutes'

function item(
  id: string,
  on: string,
  order: number,
  lat?: number,
  lng?: number,
): PublicItineraryItem {
  return {
    id,
    scheduled_on: on,
    sort_order: order,
    title: id,
    kind: 'attraction',
    place_name: '',
    address: '',
    latitude: lat ?? null,
    longitude: lng ?? null,
    planned_start_local: null,
    planned_end_local: null,
    planned_duration_minutes: null,
  }
}

const items = [
  item('A', '2026-10-01', 0, 35.1, 139.1),
  item('B', '2026-10-01', 1),
  item('C', '2026-10-02', 0, 35.2, 139.2),
  item('D', '2026-10-02', 1, 35.3, 139.3),
]

describe('buildSharedMap', () => {
  it('跳过无坐标项目，服务端算出的路段画道路、缺失的画示意连线，汇总只含成功路段', () => {
    const routes: PublicRoutes = {
      mode: 'driving',
      failed_leg_count: 1,
      unlocated_item_count: 1,
      legs: [
        {
          from_item_id: 'A',
          to_item_id: 'C',
          distance_meters: 4200,
          duration_seconds: 900,
          path: [
            { latitude: 35.1, longitude: 139.1 },
            { latitude: 35.2, longitude: 139.2 },
          ],
        },
      ],
    }
    const map = buildSharedMap(items, routes)
    expect(map.points.map((p) => p.id)).toEqual(['A', 'C', 'D'])
    expect(map.legs.map((l) => l.id)).toEqual(['A>C', 'C>D'])
    expect(map.readyCount).toBe(1)
    expect(map.failedCount).toBe(1)
    expect(map.distanceMeters).toBe(4200)
    expect(map.durationSeconds).toBe(900)
    expect(map.paths.some((p) => p.id === 'A>C' && p.kind === 'road')).toBe(true)
    expect(map.paths.some((p) => p.id === 'C>D' && p.kind === 'illustrative')).toBe(true)
  })

  it('没有路线结果时全部为示意连线', () => {
    const map = buildSharedMap(items, null)
    expect(map.readyCount).toBe(0)
    expect(map.failedCount).toBe(2)
    expect(map.paths.every((p) => p.kind === 'illustrative')).toBe(true)
  })

  it('没有定位项目时没有点位与路段', () => {
    const map = buildSharedMap([item('B', '2026-10-01', 0)], null)
    expect(map.points).toHaveLength(0)
    expect(map.legs).toHaveLength(0)
  })
})
