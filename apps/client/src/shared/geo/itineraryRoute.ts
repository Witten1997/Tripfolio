import type { GeoCoordinate, GeoRoute, TravelMode } from '@/shared/api/geo'
import type { ItineraryItem } from '@/shared/api/itinerary'

export interface MapPoint extends GeoCoordinate {
  id: string
  title: string
  number: number
}

export interface Waypoint extends MapPoint {
  item: ItineraryItem
  sourceIndex: number
}

export interface RouteLeg {
  id: string
  from: Waypoint
  to: Waypoint
  missingBetween: number
  crossDay: boolean
}

export interface MapPath {
  id: string
  points: GeoCoordinate[]
  kind: 'road' | 'illustrative'
}

export function isCoordinate(value: {
  latitude: unknown
  longitude: unknown
}): value is GeoCoordinate {
  return (
    typeof value.latitude === 'number' &&
    Number.isFinite(value.latitude) &&
    typeof value.longitude === 'number' &&
    Number.isFinite(value.longitude) &&
    Math.abs(value.latitude) <= 90 &&
    Math.abs(value.longitude) <= 180
  )
}

export function sortedItinerary(items: ItineraryItem[]): ItineraryItem[] {
  return [...items].sort(
    (a, b) =>
      a.scheduled_on.localeCompare(b.scheduled_on) ||
      a.sort_order - b.sort_order ||
      a.id.localeCompare(b.id),
  )
}

export function itineraryWaypoints(items: ItineraryItem[]): Waypoint[] {
  return sortedItinerary(items).flatMap((item, sourceIndex) =>
    isCoordinate(item)
      ? [
          {
            id: item.id,
            title: item.place_name || item.title,
            latitude: item.latitude,
            longitude: item.longitude,
            number: sourceIndex + 1,
            sourceIndex,
            item,
          },
        ]
      : [],
  )
}

export function itineraryLegs(points: Waypoint[]): RouteLeg[] {
  return points.slice(1).map((to, index) => {
    const from = points[index]!
    return {
      id: `${from.id}>${to.id}`,
      from,
      to,
      missingBetween: to.sourceIndex - from.sourceIndex - 1,
      crossDay: from.item.scheduled_on !== to.item.scheduled_on,
    }
  })
}

export function routeKey(from: GeoCoordinate, to: GeoCoordinate, mode: TravelMode): string {
  return [
    mode,
    from.longitude.toFixed(6),
    from.latitude.toFixed(6),
    to.longitude.toFixed(6),
    to.latitude.toFixed(6),
  ].join(':')
}

export function formatDistance(meters: number): string {
  return meters < 1000 ? `${Math.round(meters)} 米` : `${(meters / 1000).toFixed(1)} 公里`
}

export function formatDuration(seconds: number): string {
  if (seconds === 0) return '0 分钟'
  if (seconds < 60) return '不到 1 分钟'
  const minutes = Math.ceil(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟`
  const rest = minutes % 60
  return `${Math.floor(minutes / 60)} 小时${rest ? ` ${rest} 分钟` : ''}`
}

/** 高德可能把 POI 中心吸附到道路。端点接续用虚线，不能冒充实际道路。 */
export function routeMapPaths(leg: RouteLeg, route?: GeoRoute): MapPath[] {
  if (!route) return [{ id: leg.id, points: [leg.from, leg.to], kind: 'illustrative' }]
  const paths: MapPath[] = [{ id: leg.id, points: route.path, kind: 'road' }]
  const start = route.path[0]
  const end = route.path.at(-1)
  if (start && !samePoint(start, leg.from))
    paths.push({ id: `${leg.id}:start`, points: [leg.from, start], kind: 'illustrative' })
  if (end && !samePoint(end, leg.to))
    paths.push({ id: `${leg.id}:end`, points: [end, leg.to], kind: 'illustrative' })
  return paths
}

function samePoint(a: GeoCoordinate, b: GeoCoordinate): boolean {
  return (
    Math.abs(a.latitude - b.latitude) < 0.000001 && Math.abs(a.longitude - b.longitude) < 0.000001
  )
}
