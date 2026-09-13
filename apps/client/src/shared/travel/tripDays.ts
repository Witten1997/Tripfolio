import type { ItineraryItem } from '@/shared/api/itinerary'

export interface TripDay {
  date: string
  title: string
  /** 不在旅行起止日期范围内（旅行改期后遗留的项目）。 */
  outside: boolean
  items: ItineraryItem[]
}

const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

/** 需求 TR-01：标题为“月.日 周几”，例如 9.20 周日。 */
export function dayTitle(date: string): string {
  const [, month, day] = date.split('-').map(Number) as [number, number, number]
  const weekday = new Date(`${date}T00:00:00Z`).getUTCDay()
  return `${month}.${day} ${WEEKDAYS[weekday]}`
}

function addDays(date: string, n: number): string {
  const at = new Date(`${date}T00:00:00Z`)
  at.setUTCDate(at.getUTCDate() + n)
  return at.toISOString().slice(0, 10)
}

/** 旅行起止之间的每一天（含两端）。 */
export function tripDays(start: string, end: string): TripDay[] {
  const days: TripDay[] = []
  for (let date = start; date <= end; date = addDays(date, 1)) {
    days.push({ date, title: dayTitle(date), outside: false, items: [] })
    if (days.length > 400) break
  }
  return days
}

function compareItems(a: ItineraryItem, b: ItineraryItem) {
  return a.sort_order - b.sort_order || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
}

/** 按旅行日期铺满每一天；日期外的项目按其日期插入额外的天并标记 outside。服务端不存天实体。 */
export function groupByDay(start: string, end: string, items: ItineraryItem[]): TripDay[] {
  const byDate = new Map<string, TripDay>()
  for (const day of tripDays(start, end)) byDate.set(day.date, day)
  for (const item of items) {
    let day = byDate.get(item.scheduled_on)
    if (!day) {
      day = {
        date: item.scheduled_on,
        title: dayTitle(item.scheduled_on),
        outside: true,
        items: [],
      }
      byDate.set(item.scheduled_on, day)
    }
    day.items.push(item)
  }
  return [...byDate.values()]
    .map((day) => ({ ...day, items: [...day.items].sort(compareItems) }))
    .sort((a, b) => (a.date < b.date ? -1 : a.date > b.date ? 1 : 0))
}

/** 旅行时区下的今天；时区无效时退回本机日期，与服务端 PhaseOf 的兜底一致。 */
export function todayIn(timezone: string, at = new Date()): string {
  try {
    const parts = new Intl.DateTimeFormat('en-CA', {
      timeZone: timezone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).formatToParts(at)
    const get = (type: string) => parts.find((p) => p.type === type)?.value ?? ''
    return `${get('year')}-${get('month')}-${get('day')}`
  } catch {
    return at.toISOString().slice(0, 10)
  }
}
