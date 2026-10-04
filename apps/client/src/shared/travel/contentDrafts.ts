import type { Photo, PhotoPatch } from '@/shared/api/photos'
import type { Reservation, ReservationPatch } from '@/shared/api/reservations'
import type { Document, DocumentPatch } from '@/shared/api/documents'
import { validDate } from '@/shared/travel/itineraryDraft'
import { DraftError } from '@/shared/travel/tripDraft'

export type PhotoDraft = Required<PhotoPatch>
export type ReservationDraft = Required<ReservationPatch>
export type DocumentDraft = Required<DocumentPatch>
export const reservationLabels: Record<Reservation['kind'], string> = {
  transport: '交通',
  lodging: '住宿',
  attraction: '景点',
  other: '其他',
}
export const contentFieldLabels: Record<string, string> = {
  asset_id: '文件',
  taken_at_local: '拍摄时刻',
  recorded_on: '分组日期',
  caption: '照片说明',
  sort_order: '排序',
  place_name: '地点',
  address: '地址',
  latitude: '纬度',
  longitude: '经度',
  kind: '类型',
  title: '名称',
  booking_reference: '预订编号',
  transport_number: '航班 / 车次',
  provider_name: '提供方',
  start_local: '开始时刻',
  end_local: '结束时刻',
  origin: '出发地',
  destination: '到达地',
  contact_name: '联系人',
  contact_phone: '联系电话',
  notes: '备注',
  reservation_id: '关联预订',
}
export function emptyPhotoDraft(recordedOn: string): PhotoDraft {
  return {
    asset_id: '',
    taken_at_local: null,
    recorded_on: recordedOn,
    caption: '',
    sort_order: 0,
    place_name: '',
    address: '',
    latitude: null,
    longitude: null,
  }
}
export function emptyReservationDraft(): ReservationDraft {
  return {
    kind: 'transport',
    title: '',
    booking_reference: '',
    transport_number: null,
    provider_name: null,
    start_local: null,
    end_local: null,
    origin: null,
    destination: null,
    address: '',
    contact_name: null,
    contact_phone: null,
    notes: '',
  }
}
export function emptyDocumentDraft(): DocumentDraft {
  return { title: '', notes: '', asset_id: '', reservation_id: null }
}
export function pickDraft<D extends object>(empty: D, resource: object): D {
  return Object.fromEntries(Object.keys(empty).map((key) => [key, Reflect.get(resource, key)])) as D
}
export function changedContent<D extends object>(values: D, baseline: object): Partial<D> {
  return Object.fromEntries(
    Object.entries(values).filter(
      ([key, value]) => JSON.stringify(value) !== JSON.stringify(Reflect.get(baseline, key)),
    ),
  ) as Partial<D>
}
function localTime(value: string | null, field: string, errors: Record<string, string>) {
  if (!value) return null
  const match = /^(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2}):(\d{2})$/.exec(value)
  if (
    !match ||
    !validDate(match[1]!) ||
    Number(match[2]) > 23 ||
    Number(match[3]) > 59 ||
    Number(match[4]) > 59
  )
    errors[field] = '请输入有效的日期和时刻'
  return value
}
export function validatePhoto(draft: PhotoDraft): PhotoDraft {
  const errors: Record<string, string> = {}
  if (!draft.asset_id) errors.asset_id = '请添加照片'
  if (!validDate(draft.recorded_on)) errors.recorded_on = '请选择分组日期'
  const taken = localTime(draft.taken_at_local, 'taken_at_local', errors)
  if (Object.keys(errors).length) throw new DraftError(errors)
  return { ...draft, taken_at_local: taken }
}
export function validateReservation(draft: ReservationDraft): ReservationDraft {
  const errors: Record<string, string> = {}
  if (!draft.title.trim()) errors.title = '请填写预订名称'
  const start = localTime(draft.start_local, 'start_local', errors)
  const end = localTime(draft.end_local, 'end_local', errors)
  if (start && end && end < start) errors.end_local = '结束时刻不能早于开始时刻'
  if (Object.keys(errors).length) throw new DraftError(errors)
  return {
    ...draft,
    title: draft.title.trim(),
    start_local: start,
    end_local: end,
    transport_number: draft.kind === 'transport' ? draft.transport_number || null : null,
    origin: draft.kind === 'transport' ? draft.origin || null : null,
    destination: draft.kind === 'transport' ? draft.destination || null : null,
  }
}
export function changedReservation(
  values: ReservationDraft,
  baseline: Reservation,
): ReservationPatch {
  const patch = changedContent(values, baseline)
  if (patch.kind && patch.kind !== 'transport')
    Object.assign(patch, { transport_number: null, origin: null, destination: null })
  return patch
}
export function validateDocument(draft: DocumentDraft): DocumentDraft {
  const errors: Record<string, string> = {}
  if (!draft.title.trim()) errors.title = '请填写资料名称'
  if (!draft.asset_id) errors.asset_id = '请添加图片或 PDF'
  if (Object.keys(errors).length) throw new DraftError(errors)
  return { ...draft, title: draft.title.trim(), reservation_id: draft.reservation_id || null }
}
export const photoDraftFrom = (v: Photo) => pickDraft(emptyPhotoDraft(v.recorded_on), v)
export const reservationDraftFrom = (v: Reservation) => pickDraft(emptyReservationDraft(), v)
export const documentDraftFrom = (v: Document) => pickDraft(emptyDocumentDraft(), v)
