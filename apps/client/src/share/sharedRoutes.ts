import {
  itineraryLegs,
  itineraryWaypoints,
  routeMapPaths,
  type MapPath,
  type RouteLeg,
  type Waypoint,
} from '@/shared/geo/itineraryRoute'

import type { PublicItineraryItem, PublicRoutes } from './api'

export interface SharedMap {
  points: Waypoint<PublicItineraryItem>[]
  legs: RouteLeg<PublicItineraryItem>[]
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
  const legs = itineraryLegs(points)
  const byPair = new Map(
    (routes?.legs ?? []).map((leg) => [`${leg.from_item_id}>${leg.to_item_id}`, leg]),
  )
  const out: SharedMap = {
    points,
    legs,
    paths: [],
    readyCount: 0,
    failedCount: 0,
    distanceMeters: 0,
    durationSeconds: 0,
  }
  for (const leg of legs) {
    const found = byPair.get(leg.id)
    if (!found) {
      out.failedCount++
      out.paths.push(...routeMapPaths(leg))
      continue
    }
    out.readyCount++
    out.distanceMeters += found.distance_meters
    out.durationSeconds += found.duration_seconds
    out.paths.push(...routeMapPaths(leg, { path: found.path }))
  }
  return out
}
