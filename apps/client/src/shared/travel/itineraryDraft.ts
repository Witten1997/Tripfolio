import type { ItineraryCreate, ItineraryItem, ItineraryPatch } from '@/shared/api/itinerary'
import type { GeoPlace } from '@/shared/api/geo'
import { isCoordinate } from '@/shared/geo/itineraryRoute'
import { canonicalizeAmount } from '@/shared/money'
import { DraftError } from '@/shared/travel/tripDraft'

export type PlannedMode = 'none' | 'end' | 'duration'

/** 表单草稿：计划开始只编辑时间，其余当地时间以“YYYY-MM-DD HH:mm”编辑。 */
export interface ItineraryDraft {
  title: string
  kind: ItineraryItem['kind']
  scheduled_on: string
  planned_start: string
  planned_mode: PlannedMode
  planned_end: string
  planned_duration_minutes: string
  place_name: string
  address: string
  latitude: number | null
  longitude: number | null
  estimated_amount: string
  notes: string
  status: ItineraryItem['status']
  actual_start: string
  actual_end: string
  actual_notes: string
}

/** 校验后的字段集合：契约创建正文去掉 id；currency_code 仅在金额非空时携带。 */
export type ItineraryValues = Omit<ItineraryCreate, 'id'>

export const itineraryFieldLabels: Record<string, string> = {
  title: '标题',
  kind: '类型',
  scheduled_on: '所属日期',
  planned_start_local: '计划开始',
  planned_end_local: '计划结束',
  planned_duration_minutes: '停留时长',
  place_name: '地点',
  address: '地址',
  latitude: '纬度',
  longitude: '经度',
  estimated_amount: '预计费用',
  notes: '备注',
  status: '状态',
  actual_start_local: '实际开始',
  actual_end_local: '实际结束',
  actual_notes: '实际记录',
}

export function emptyItineraryDraft(scheduledOn = ''): ItineraryDraft {
  return {
    title: '',
    kind: 'attraction',
    scheduled_on: scheduledOn,
    planned_start: '',
    planned_mode: 'none',
    planned_end: '',
    planned_duration_minutes: '',
    place_name: '',
    address: '',
    latitude: null,
    longitude: null,
    estimated_amount: '',
    notes: '',
    status: 'pending',
    actual_start: '',
    actual_end: '',
    actual_notes: '',
  }
}

function toEditable(local: string | null): string {
  return local ? local.slice(0, 16).replace('T', ' ') : ''
}

function toLocal(field: string, editable: string, errors: Record<string, string>): string | null {
  const value = editable.trim()
  if (!value) return null
  const match = /^(\d{4}-\d{2}-\d{2})[ T](\d{2}:\d{2})(?::\d{2})?$/.exec(value)
  if (!match || !validDate(match[1]!)) {
    errors[field] = '时间格式须为 YYYY-MM-DD HH:mm'
    return null
  }
  const [hh, mm] = match[2]!.split(':').map(Number)
  if (hh! > 23 || mm! > 59) {
    errors[field] = '时间格式须为 YYYY-MM-DD HH:mm'
    return null
  }
  return `${match[1]}T${match[2]}:00`
}

export function validDate(value: string) {
  if (!/^[0-9]{4}-[0-9]{2}-[0-9]{2}$/.test(value) || value.startsWith('0000')) return false
  const date = new Date(`${value}T00:00:00Z`)
  return Number.isFinite(date.valueOf()) && date.toISOString().slice(0, 10) === value
}

export function itineraryDraftFrom(item: ItineraryItem): ItineraryDraft {
  return {
    title: item.title,
    kind: item.kind,
    scheduled_on: item.scheduled_on,
    planned_start: toEditable(item.planned_start_local),
    planned_mode:
      item.planned_end_local !== null
        ? 'end'
        : item.planned_duration_minutes !== null
          ? 'duration'
          : 'none',
    planned_end: toEditable(item.planned_end_local),
    planned_duration_minutes:
      item.planned_duration_minutes === null ? '' : String(item.planned_duration_minutes),
    place_name: item.place_name,
    address: item.address,
    latitude: item.latitude,
    longitude: item.longitude,
    estimated_amount: item.estimated_amount ?? '',
    notes: item.notes,
    status: item.status,
    actual_start: toEditable(item.actual_start_local),
    actual_end: toEditable(item.actual_end_local),
    actual_notes: item.actual_notes,
  }
}

export interface TripMoney {
  currency: string
  minorUnits: number
}

/** 标题与位置作为一个快照更新，换选 POI 不遗留上一次的标题。 */
export function applyItineraryPlace(draft: ItineraryDraft, place: GeoPlace) {
  Object.assign(draft, {
    title: place.name.trim() || '地图选点',
    place_name: place.name.trim() || '地图选点',
    address: place.address,
    latitude: place.latitude,
    longitude: place.longitude,
  })
}

export function clearItineraryPlace(draft: ItineraryDraft) {
  Object.assign(draft, { title: '', place_name: '', address: '', latitude: null, longitude: null })
}

/** 只预填独立账目的备注与日期，不建立关联，也不转换预计费用。 */
export function itineraryExpensePreset(draft: ItineraryDraft) {
  const notes = draft.place_name.trim() || draft.title.trim()
  const errors: Record<string, string> = {}
  if (!notes) errors.place_name = '请先选择地点，再记录该地点花费'
  if (!validDate(draft.scheduled_on)) errors.scheduled_on = '请先选择有效的所属日期'
  if (Object.keys(errors).length) throw new DraftError(errors)
  return { notes, occurred_on: draft.scheduled_on }
}

export function validateItineraryDraft(
  draft: ItineraryDraft,
  money: TripMoney,
  requirePlace = false,
): ItineraryValues {
  const errors: Record<string, string> = {}
  const title = draft.title.trim()
  if (!title || title.length > 200)
    errors.title = requirePlace ? '请选择有效地点，行程名称会自动填写' : '请输入 1–200 个字符的标题'
  if (!validDate(draft.scheduled_on)) errors.scheduled_on = '请选择有效的所属日期'
  const plannedStart = toLocal('planned_start_local', draft.planned_start, errors)
  let plannedEnd: string | null = null
  let duration: number | null = null
  if (draft.planned_mode === 'end') {
    plannedEnd = toLocal('planned_end_local', draft.planned_end, errors)
    if (plannedStart && plannedEnd && plannedEnd < plannedStart) {
      errors.planned_end_local = '计划结束时间不能早于开始时间'
    }
  } else if (draft.planned_mode === 'duration') {
    const raw = draft.planned_duration_minutes.trim()
    if (raw) {
      duration = Number(raw)
      if (!Number.isInteger(duration) || duration < 1) {
        errors.planned_duration_minutes = '停留时长须为正整数分钟'
        duration = null
      }
    }
  }
  const actualStart = toLocal('actual_start_local', draft.actual_start, errors)
  const actualEnd = toLocal('actual_end_local', draft.actual_end, errors)
  if (actualStart && actualEnd && actualEnd < actualStart) {
    errors.actual_end_local = '实际结束时间不能早于开始时间'
  }
  const placeName = draft.place_name.trim()
  const address = draft.address.trim()
  if (requirePlace && (!isCoordinate(draft) || !placeName)) {
    errors.place_name = '请选择一个高德地点或在地图上选点'
  }
  if (placeName.length > 200) errors.place_name = '地点最多 200 个字符'
  if (address.length > 500) errors.address = '地址最多 500 个字符'
  if ((draft.latitude === null) !== (draft.longitude === null))
    errors.latitude = '经纬度须同时填写或同时清空'
  if (
    draft.latitude !== null &&
    (!Number.isFinite(draft.latitude) || Math.abs(draft.latitude) > 90)
  )
    errors.latitude = '纬度须在 −90 至 90 之间'
  if (
    draft.longitude !== null &&
    (!Number.isFinite(draft.longitude) || Math.abs(draft.longitude) > 180)
  )
    errors.longitude = '经度须在 −180 至 180 之间'
  if (draft.notes.length > 10000) errors.notes = '备注最多 10000 个字符'
  if (draft.actual_notes.length > 10000) errors.actual_notes = '实际记录最多 10000 个字符'
  let amount: string | null = null
  if (draft.estimated_amount.trim() !== '') {
    try {
      amount = canonicalizeAmount(draft.estimated_amount.trim(), money.minorUnits)
    } catch (cause) {
      errors.estimated_amount = cause instanceof Error ? cause.message : '金额格式不正确'
    }
  }
  if (Object.keys(errors).length) throw new DraftError(errors)
  const values: ItineraryValues = {
    title,
    kind: draft.kind,
    scheduled_on: draft.scheduled_on,
    planned_start_local: plannedStart,
    planned_end_local: plannedEnd,
    planned_duration_minutes: duration,
    place_name: placeName,
    address,
    latitude: draft.latitude,
    longitude: draft.longitude,
    estimated_amount: amount,
    notes: draft.notes,
    status: draft.status,
    actual_start_local: actualStart,
    actual_end_local: actualEnd,
    actual_notes: draft.actual_notes,
  }
  if (amount !== null) values.currency_code = money.currency
  return values
}

const patchFields = [
  'title',
  'kind',
  'planned_start_local',
  'planned_end_local',
  'planned_duration_minutes',
  'place_name',
  'address',
  'latitude',
  'longitude',
  'estimated_amount',
  'notes',
  'status',
  'actual_start_local',
  'actual_end_local',
  'actual_notes',
] as const

/** scheduled_on 只能由重排接口修改，不进 PATCH；金额改为非空时按契约附带币种。 */
export function changedItineraryFields(
  values: ItineraryValues,
  baseline: ItineraryItem,
): ItineraryPatch {
  const patch: Record<string, unknown> = {}
  for (const key of patchFields) {
    if ((values[key] ?? null) !== baseline[key]) patch[key] = values[key] ?? null
  }
  if ('estimated_amount' in patch && values.estimated_amount !== null) {
    patch.currency_code = values.currency_code
  }
  return patch as ItineraryPatch
}
