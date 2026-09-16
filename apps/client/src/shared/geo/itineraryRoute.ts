import type { GeoCoordinate, GeoRoute, TravelMode } from '@/shared/api/geo'
import type { ItineraryItem } from '@/shared/api/itinerary'
import type { ItineraryKind } from '@/shared/travel/itineraryKinds'

export interface MapPoint extends GeoCoordinate {
  id: string
  title: string
  number: number
  kind?: ItineraryKind
}

export type RoutableItem = Pick<
  ItineraryItem,
  'id' | 'scheduled_on' | 'sort_order' | 'title' | 'place_name' | 'latitude' | 'longitude'
>

export interface Waypoint<T extends RoutableItem = ItineraryItem> extends MapPoint {
  item: T
  sourceIndex: number
}

export interface RouteLeg<T extends RoutableItem = ItineraryItem> {
  id: string
  from: Waypoint<T>
  to: Waypoint<T>
  missingBetween: number
  crossDay: boolean
}

export interface MapPath {
  id: string
  points: GeoCoordinate[]
  kind: 'road' | 'illustrative'
  mode?: TravelMode
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

export function sortedItinerary<T extends RoutableItem>(items: T[]): T[] {
  return [...items].sort(
    (a, b) =>
      a.scheduled_on.localeCompare(b.scheduled_on) ||
      a.sort_order - b.sort_order ||
      a.id.localeCompare(b.id),
  )
}

export function itineraryWaypoints<T extends RoutableItem>(items: T[]): Waypoint<T>[] {
  return sortedItinerary(items).flatMap((item, sourceIndex) =>
    isCoordinate(item)
      ? [
          {
            id: item.id,
            title: item.place_name || item.title,
            latitude: item.latitude,
            longitude: item.longitude,
            number: sourceIndex + 1,
            kind: 'kind' in item ? (item.kind as ItineraryKind) : undefined,
            sourceIndex,
            item,
          },
        ]
      : [],
  )
}

export function itineraryLegs<T extends RoutableItem>(points: Waypoint<T>[]): RouteLeg<T>[] {
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

export function directDistanceMeters(from: GeoCoordinate, to: GeoCoordinate): number {
  const earthRadius = 6_371_008.8
  const radians = Math.PI / 180
  const fromLatitude = from.latitude * radians
  const toLatitude = to.latitude * radians
  const latitudeDelta = (to.latitude - from.latitude) * radians
  const longitudeDelta = (to.longitude - from.longitude) * radians
  const haversine =
    Math.sin(latitudeDelta / 2) ** 2 +
    Math.cos(fromLatitude) * Math.cos(toLatitude) * Math.sin(longitudeDelta / 2) ** 2
  return Math.round(earthRadius * 2 * Math.atan2(Math.sqrt(haversine), Math.sqrt(1 - haversine)))
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
export function routeMapPaths<T extends RoutableItem>(
  leg: RouteLeg<T>,
  route?: GeoRoute | Pick<GeoRoute, 'mode' | 'path'>,
): MapPath[] {
  if (!route) return [{ id: leg.id, points: [leg.from, leg.to], kind: 'illustrative' }]
  const paths: MapPath[] = [{ id: leg.id, points: route.path, kind: 'road', mode: route.mode }]
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
