import type { components } from '@tripfolio/contracts/openapi/v1'

import { CollectionBaseline } from './collectionGuards'

type BaselineResourceMap = {
  categories: components['schemas']['ExpenseCategory']
  itinerary_day: components['schemas']['ItineraryItem']
  photo_day: components['schemas']['Photo']
}
export type ReadableCollectionKind = keyof BaselineResourceMap
export interface CollectionScope<K extends ReadableCollectionKind> {
  readonly kind: K
  readonly scope_id: string
}
export interface CollectionPageRequest<
  K extends ReadableCollectionKind,
> extends CollectionScope<K> {
  readonly limit: number
  readonly cursor?: string
}
export interface CollectionPage<K extends ReadableCollectionKind> extends CollectionScope<K> {
  readonly sync_epoch: string
  readonly revision: string
  readonly expires_at: string
  readonly items: readonly BaselineResourceMap[K][]
  readonly next_cursor: string | null
}
type DeepReadonly<T> = T extends object ? { readonly [P in keyof T]: DeepReadonly<T[P]> } : T
export interface CompleteCollection<K extends ReadableCollectionKind> extends CollectionScope<K> {
  readonly complete: true
  readonly sync_epoch: string
  readonly revision: string
  readonly expires_at: string
  readonly items: readonly DeepReadonly<BaselineResourceMap[K]>[]
  readonly baseline: CollectionBaseline
}
export type CollectionPageReader<K extends ReadableCollectionKind> = (
  request: CollectionPageRequest<K>,
  signal?: AbortSignal,
) => Promise<unknown>

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const revisionPattern = /^sha256:[0-9a-f]{64}$/
const kinds: readonly string[] = ['categories', 'itinerary_day', 'photo_day']
const invalid = () => new Error('集合资料不完整或已变化，请重新读取并核对。')

function isUUID(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.length === 36 &&
    uuidPattern.test(value) &&
    value !== '00000000-0000-0000-0000-000000000000'
  )
}
function isDay(value: unknown): value is string {
  if (
    typeof value !== 'string' ||
    value.length !== 10 ||
    !/^[0-9]{4}-[0-9]{2}-[0-9]{2}$/.test(value) ||
    value.startsWith('0000')
  )
    return false
  const parsed = new Date(`${value}T00:00:00Z`)
  return Number.isFinite(parsed.valueOf()) && parsed.toISOString().slice(0, 10) === value
}
function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

// Read descriptors rather than evaluating getters or calling toJSON. The reader's
// response remains independent even if its caller mutates it while hashing awaits.
function snapshot(value: unknown, ancestors = new Set<object>(), depth = 0): unknown {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return value
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value !== 'object' || depth > 64 || ancestors.has(value)) throw invalid()
  const proto = Object.getPrototypeOf(value)
  if (
    Array.isArray(value) ? proto !== Array.prototype : proto !== Object.prototype && proto !== null
  )
    throw invalid()
  ancestors.add(value)
  const descriptors = Object.getOwnPropertyDescriptors(value)
  const keys = Reflect.ownKeys(descriptors)
  let result: unknown
  if (Array.isArray(value)) {
    if (keys.length !== value.length + 1) throw invalid()
    const out: unknown[] = []
    for (let i = 0; i < value.length; i++) {
      const d = descriptors[String(i)]
      if (!d || !('value' in d) || !d.enumerable) throw invalid()
      out.push(snapshot(d.value, ancestors, depth + 1))
    }
    result = Object.freeze(out)
  } else {
    const out: Record<string, unknown> = Object.create(null)
    for (const key of keys) {
      if (typeof key !== 'string') throw invalid()
      const d = descriptors[key]!
      if (!('value' in d) || !d.enumerable) throw invalid()
      out[key] = snapshot(d.value, ancestors, depth + 1)
    }
    result = Object.freeze(out)
  }
  ancestors.delete(value)
  return result
}

function validScope(value: unknown): value is CollectionScope<ReadableCollectionKind> {
  if (
    !record(value) ||
    typeof value.kind !== 'string' ||
    !kinds.includes(value.kind) ||
    typeof value.scope_id !== 'string'
  )
    return false
  const parts = value.scope_id.split('/')
  return (
    isUUID(parts[0]) &&
    (value.kind === 'categories' ? parts.length === 1 : parts.length === 2 && isDay(parts[1]))
  )
}
function validVersion(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.length <= 19 &&
    /^[1-9][0-9]*$/.test(value) &&
    BigInt(value).toString() === value &&
    BigInt(value) <= 9223372036854775807n
  )
}
function timestamp(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    /^\d{4}-\d{2}-\d{2}T.*Z$/.test(value) &&
    Number.isFinite(Date.parse(value))
  )
}
function expiration(value: unknown): value is string {
  return (
    timestamp(value) &&
    value.length === 20 &&
    new Date(value).toISOString() === `${value.slice(0, -1)}.000Z`
  )
}
const text = (v: unknown) => typeof v === 'string'
const nullableText = (v: unknown) => v === null || text(v)
const int32 = (v: unknown) =>
  typeof v === 'number' && Number.isInteger(v) && v >= -2147483648 && v <= 2147483647
const coordinate = (v: unknown, max: number) =>
  v === null || (typeof v === 'number' && Number.isFinite(v) && Math.abs(v) <= max)

function validResource(
  kind: ReadableCollectionKind,
  scopeID: string,
  value: unknown,
): value is Record<string, unknown> & { id: string; version: string } {
  if (
    !record(value) ||
    !isUUID(value.id) ||
    !validVersion(value.version) ||
    value.deleted_at !== null ||
    !timestamp(value.created_at) ||
    !timestamp(value.updated_at) ||
    !int32(value.sort_order)
  )
    return false
  if (kind === 'categories')
    return text(value.name) && nullableText(value.icon) && typeof value.is_preset === 'boolean'
  const [trip, day] = scopeID.split('/')
  if (
    value.trip_id !== trip ||
    !text(value.place_name) ||
    !text(value.address) ||
    !coordinate(value.latitude, 90) ||
    !coordinate(value.longitude, 180)
  )
    return false
  if (kind === 'photo_day')
    return (
      value.recorded_on === day &&
      isUUID(value.asset_id) &&
      nullableText(value.taken_at_local) &&
      text(value.caption)
    )
  return (
    value.scheduled_on === day &&
    text(value.title) &&
    text(value.poi_id) &&
    typeof value.footprint_excluded === 'boolean' &&
    typeof value.kind === 'string' &&
    ['attraction', 'transport', 'lodging', 'dining', 'other'].includes(value.kind) &&
    typeof value.status === 'string' &&
    ['pending', 'completed', 'skipped'].includes(value.status) &&
    [
      'planned_start_local',
      'planned_end_local',
      'actual_start_local',
      'actual_end_local',
      'estimated_amount',
      'currency_code',
    ].every((key) => nullableText(value[key])) &&
    (value.planned_duration_minutes === null || int32(value.planned_duration_minutes)) &&
    text(value.notes) &&
    text(value.actual_notes)
  )
}

export async function loadCollectionBaseline<K extends ReadableCollectionKind>(
  scope: CollectionScope<K>,
  readPage: CollectionPageReader<K>,
  options?: { readonly limit?: number; readonly signal?: AbortSignal },
): Promise<CompleteCollection<K>> {
  const captured = snapshot(scope)
  const limit = options?.limit ?? 50
  const signal = options?.signal
  if (
    !validScope(captured) ||
    !Number.isInteger(limit) ||
    limit < 1 ||
    limit > 100 ||
    typeof readPage !== 'function'
  )
    throw invalid()
  const crypto = globalThis.crypto
  if (!crypto?.subtle) throw new Error('当前环境无法校验集合资料，请在安全连接中重试。')
  const { kind, scope_id: scopeID } = captured as CollectionScope<K>
  let cursor: string | undefined
  let first: { epoch: string; revision: string; expires: string } | undefined
  let previous = ''
  const cursors = new Set<string>()
  const items: unknown[] = []
  const lines: string[] = []
  do {
    signal?.throwIfAborted()
    const request = Object.freeze({ kind, scope_id: scopeID, limit, ...(cursor ? { cursor } : {}) })
    const page = snapshot(await readPage(request, signal))
    signal?.throwIfAborted()
    if (
      !record(page) ||
      page.kind !== kind ||
      page.scope_id !== scopeID ||
      !isUUID(page.sync_epoch) ||
      typeof page.revision !== 'string' ||
      page.revision.length !== 71 ||
      !revisionPattern.test(page.revision) ||
      !expiration(page.expires_at) ||
      Date.parse(page.expires_at) <= Date.now() ||
      !Array.isArray(page.items) ||
      page.items.length > limit ||
      !(
        page.next_cursor === null ||
        (typeof page.next_cursor === 'string' &&
          page.next_cursor.length > 0 &&
          page.next_cursor.length <= 4096)
      )
    )
      throw invalid()
    if (!first)
      first = { epoch: page.sync_epoch, revision: page.revision, expires: page.expires_at }
    if (
      page.sync_epoch !== first.epoch ||
      page.revision !== first.revision ||
      page.expires_at !== first.expires
    )
      throw invalid()
    for (const item of page.items) {
      if (!validResource(kind, scopeID, item) || item.id <= previous) throw invalid()
      previous = item.id
      items.push(item)
      lines.push(`${item.id}:${item.version}\n`)
    }
    if (page.next_cursor === null) break
    if (page.items.length !== limit || cursors.has(page.next_cursor)) throw invalid()
    cursors.add(page.next_cursor)
    cursor = page.next_cursor
  } while (true)
  const source = `${first.epoch}\n${kind}\n${scopeID}\n${lines.join('')}`
  const digest = new Uint8Array(
    await crypto.subtle.digest('SHA-256', new TextEncoder().encode(source)),
  )
  const revision = `sha256:${Array.from(digest, (b) => b.toString(16).padStart(2, '0')).join('')}`
  signal?.throwIfAborted()
  if (revision !== first.revision || Date.parse(first.expires) <= Date.now()) throw invalid()
  const baseline = new CollectionBaseline({
    complete: true,
    guards: [{ kind, scope_id: scopeID, revision }],
  })
  return Object.freeze({
    kind,
    scope_id: scopeID,
    complete: true,
    sync_epoch: first.epoch,
    revision,
    expires_at: first.expires,
    items: Object.freeze(items),
    baseline,
  }) as CompleteCollection<K>
}
