import {
  itineraryLegs,
  itineraryWaypoints,
  routeMapPaths,
  type MapPath,
  type RouteLeg,
  type Waypoint,
} from '@/shared/geo/itineraryRoute'

import type { PublicItineraryItem, PublicLeg, PublicRoutes } from './api'

/** 一段相邻路段及其算路结果；route 为空表示这一段没有算出来。 */
export interface SharedLeg extends RouteLeg<PublicItineraryItem> {
  route: PublicLeg | null
}

export interface SharedMap {
  points: Waypoint<PublicItineraryItem>[]
  legs: SharedLeg[]
  /** 按出发站索引：行程与地图两个视图都在「这一站」之后展示到下一站的距离。 */
  byOrigin: Map<string, SharedLeg>
  paths: MapPath[]
  readyCount: number
  failedCount: number
  distanceMeters: number
  durationSeconds: number
}

/** 把服务端整趟路段套到本地相邻关系上：算出的画道路，缺失的画示意连线；汇总只含成功路段。 */
export function buildSharedMap(
  items: PublicItineraryItem[],
  routes: PublicRoutes | null,
): SharedMap {
  const points = itineraryWaypoints(items)
  const byPair = new Map(
    (routes?.legs ?? []).map((leg) => [`${leg.from_item_id}>${leg.to_item_id}`, leg]),
  )
  const legs: SharedLeg[] = itineraryLegs(points).map((leg) => ({
    ...leg,
    route: byPair.get(leg.id) ?? null,
  }))
  const out: SharedMap = {
    points,
    legs,
    byOrigin: new Map(legs.map((leg) => [leg.from.id, leg])),
    paths: [],
    readyCount: 0,
    failedCount: 0,
    distanceMeters: 0,
    durationSeconds: 0,
  }
  for (const leg of legs) {
    if (!leg.route) {
      out.failedCount++
      out.paths.push(...routeMapPaths(leg))
      continue
    }
    out.readyCount++
    out.distanceMeters += leg.route.distance_meters
    out.durationSeconds += leg.route.duration_seconds
    out.paths.push(...routeMapPaths(leg, { path: leg.route.path, mode: routes?.mode ?? 'driving' }))
  }
  return out
}
