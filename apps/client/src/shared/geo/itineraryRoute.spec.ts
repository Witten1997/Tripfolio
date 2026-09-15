import { describe, expect, it } from 'vitest'

import type { ItineraryItem } from '@/shared/api/itinerary'
import {
  formatDistance,
  formatDuration,
  isCoordinate,
  itineraryLegs,
  itineraryWaypoints,
  routeKey,
  routeMapPaths,
} from '@/shared/geo/itineraryRoute'
import {
  changedItineraryFields,
  emptyItineraryDraft,
  itineraryDraftFrom,
  validateItineraryDraft,
} from '@/shared/travel/itineraryDraft'

function item(
  id: string,
  sort: number,
  date = '2026-10-01',
  latitude: number | null = 39.9,
): ItineraryItem {
  return {
    ...validateItineraryDraft(
      {
        ...emptyItineraryDraft(date),
        title: id,
        latitude,
        longitude: latitude === null ? null : 116.4,
      },
      { currency: 'CNY', minorUnits: 2 },
    ),
    id,
    trip_id: 'trip',
    sort_order: sort,
    currency_code: null,
    version: '1',
    created_at: '',
    updated_at: '',
    deleted_at: null,
  } as ItineraryItem
}

describe('行程地点与路线', () => {
  it('按日期、顺序、ID 编号；未定位项目留出位置，不自动改行程顺序', () => {
    const points = itineraryWaypoints([
      item('c', 0, '2026-10-02'),
      item('b', 3),
      item('missing', 2, '2026-10-01', null),
      item('a', 0),
    ])
    expect(points.map((p) => [p.id, p.number])).toEqual([
      ['a', 1],
      ['b', 3],
      ['c', 4],
    ])
    const legs = itineraryLegs(points)
    expect(legs[0]).toMatchObject({ id: 'a>b', missingBetween: 1, crossDay: false })
    expect(legs[1]).toMatchObject({ id: 'b>c', crossDay: true })
  })

  it('坐标支持零值，拒绝空值、非有限值和越界', () => {
    expect(isCoordinate({ latitude: 0, longitude: 0 })).toBe(true)
    for (const value of [null, undefined, NaN, Infinity, 91, '39.9'])
      expect(isCoordinate({ latitude: value, longitude: 116 })).toBe(false)
  })

  it('新增与编辑保存经纬度，清除定位显式写回两个 null', () => {
    const baseline = item('a', 0)
    const draft = itineraryDraftFrom(baseline)
    expect(draft.latitude).toBe(39.9)
    draft.latitude = 39.918058
    draft.longitude = 116.397026
    const values = validateItineraryDraft(draft, { currency: 'CNY', minorUnits: 2 })
    expect(changedItineraryFields(values, baseline)).toEqual({
      latitude: 39.918058,
      longitude: 116.397026,
    })
    draft.latitude = draft.longitude = null
    expect(
      changedItineraryFields(
        validateItineraryDraft(draft, { currency: 'CNY', minorUnits: 2 }),
        baseline,
      ),
    ).toEqual({ latitude: null, longitude: null })
    draft.latitude = 39
    expect(() => validateItineraryDraft(draft, { currency: 'CNY', minorUnits: 2 })).toThrow(
      '经纬度',
    )
  })

  it('方向与交通模式参与缓存键，不把反方向距离复用', () => {
    const a = { latitude: 39, longitude: 116 }
    const b = { latitude: 40, longitude: 117 }
    expect(routeKey(a, b, 'driving')).not.toBe(routeKey(b, a, 'driving'))
    expect(routeKey(a, b, 'walking')).not.toBe(routeKey(a, b, 'driving'))
  })

  it('未知路线只画示意线，成功路线保留原道路点，POI 接续另画虚线', () => {
    const leg = itineraryLegs(
      itineraryWaypoints([item('a', 0), item('b', 1, '2026-10-01', 40)]),
    )[0]!
    expect(routeMapPaths(leg)[0]?.kind).toBe('illustrative')
    const path = [
      { latitude: 39.91, longitude: 116.4 },
      { latitude: 39.98, longitude: 116.41 },
    ]
    const paths = routeMapPaths(leg, {
      mode: 'walking',
      provider: 'amap',
      path,
      distance_meters: 1000,
      duration_seconds: 800,
    })
    expect(paths[0]?.points).toBe(path)
    expect(paths.map((p) => p.kind)).toEqual(['road', 'illustrative', 'illustrative'])
  })

  it('米与秒正确转为可读单位，零距离不显示为缺失', () => {
    expect(formatDistance(0)).toBe('0 米')
    expect(formatDistance(1108)).toBe('1.1 公里')
    expect(formatDuration(886)).toBe('15 分钟')
    expect(formatDuration(3601)).toBe('1 小时 1 分钟')
  })
})
