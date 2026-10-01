import type { DashboardPlace, DashboardTrip } from '@/shared/api/dashboard'
import { sumMoney } from '@/shared/travel/statisticsView'

export type SpendingScope = 'personal' | 'whole'

export function uniquePlaces(places: readonly DashboardPlace[]): DashboardPlace[] {
  const seen = new Set<string>()
  return places.filter((place) => {
    const key = place.poi_id
      ? `poi:${place.poi_id}`
      : `${place.name.trim()}:${place.latitude?.toFixed(6)},${place.longitude?.toFixed(6)}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

export function visitedCities(places: readonly DashboardPlace[]) {
  const grouped = new Map<string, DashboardPlace[]>()
  for (const place of places) {
    if (place.excluded || place.kind === 'transport' || !place.region) continue
    const code = place.region.city_code
    const group = grouped.get(code) ?? []
    group.push(place)
    grouped.set(code, group)
  }
  return [...grouped]
    .map(([code, records]) => {
      const region = records[0]!.region!
      const point = records.find((p) => p.latitude !== null && p.longitude !== null)
      return {
        code,
        name: region.city_name,
        province: region.province_name,
        provinceCode: region.province_code,
        records,
        places: uniquePlaces(records),
        visits: new Set(records.map((p) => p.trip_id)).size,
        latitude: point?.latitude ?? null,
        longitude: point?.longitude ?? null,
      }
    })
    .sort((a, b) => b.visits - a.visits || a.name.localeCompare(b.name, 'zh-CN'))
}

export type VisitedCity = ReturnType<typeof visitedCities>[number]

export function tripNet(trip: DashboardTrip, scope: SpendingScope): string {
  return sumMoney(trip.categories.map((category) => category[scope].net))
}

export function spendingCategories(trips: readonly DashboardTrip[], scope: SpendingScope) {
  const grouped = new Map<
    string,
    { id: string; name: string; net: string[]; expense: string[]; refund: string[] }
  >()
  for (const trip of trips) {
    for (const category of trip.categories) {
      const group = grouped.get(category.id) ?? {
        id: category.id,
        name: category.name,
        net: [],
        expense: [],
        refund: [],
      }
      for (const key of ['net', 'expense', 'refund'] as const) group[key].push(category[scope][key])
      grouped.set(category.id, group)
    }
  }
  return [...grouped.values()]
    .map((group) => ({
      id: group.id,
      name: group.name,
      net: sumMoney(group.net),
      expense: sumMoney(group.expense),
      refund: sumMoney(group.refund),
    }))
    .filter((row) => Number(row.expense) !== 0 || Number(row.refund) !== 0)
    .sort((a, b) => Number(b.net) - Number(a.net))
}

/** 与服务端保持相同口径：合并同币种旅行覆盖的自然日，作为日均花销分母。 */
export function coveredDays(trips: readonly DashboardTrip[]): number {
  const ranges = trips
    .map((trip) => [Date.parse(trip.start_date), Date.parse(trip.end_date) + 86400000] as const)
    .sort((a, b) => a[0] - b[0])
  let total = 0,
    end = -Infinity
  for (const [start, next] of ranges) {
    total += Math.max(0, next - Math.max(start, end)) / 86400000
    end = Math.max(end, next)
  }
  return total
}

/** 定点整数除法，包含负净支出的四舍五入；浮点数只进入图表比例。 */
export function averageMoney(amount: string, divisor: number, units: number): string {
  if (!divisor) return '—'
  const negative = amount.startsWith('-')
  const [integer = '0', fraction = ''] = (negative ? amount.slice(1) : amount).split('.')
  const scale = Math.max(units, fraction.length)
  const value = BigInt(integer + fraction.padEnd(scale, '0'))
  const denominator = BigInt(divisor) * 10n ** BigInt(scale - units)
  const rounded = (value * 2n + denominator) / (2n * denominator)
  const digits = rounded.toString().padStart(units + 1, '0')
  const formatted = units ? `${digits.slice(0, -units)}.${digits.slice(-units)}` : digits
  return `${negative && rounded !== 0n ? '-' : ''}${formatted}`
}
