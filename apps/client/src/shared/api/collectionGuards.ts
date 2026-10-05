export type CollectionKind =
  'categories' | 'members' | 'itinerary_day' | 'packing_order' | 'todo_order' | 'photo_day'

export interface CollectionGuard {
  readonly kind: CollectionKind
  readonly scope_id: string
  readonly revision: string
}

const kinds: readonly string[] = [
  'categories',
  'members',
  'itinerary_day',
  'packing_order',
  'todo_order',
  'photo_day',
]
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const zeroUUID = '00000000-0000-0000-0000-000000000000'

function validScope(guard: CollectionGuard): boolean {
  const parts = guard.scope_id.split('/')
  const id = parts[0]!
  if (id.length !== 36 || !uuid.test(id) || id === zeroUUID) return false
  if (guard.kind !== 'photo_day' && guard.kind !== 'itinerary_day') return parts.length === 1
  if (parts.length !== 2) return false
  const day = parts[1]!
  if (day.length !== 10 || !/^[0-9]{4}-[0-9]{2}-[0-9]{2}$/.test(day) || day.startsWith('0000'))
    return false
  const parsed = new Date(`${day}T00:00:00Z`)
  return !Number.isNaN(parsed.valueOf()) && parsed.toISOString().slice(0, 10) === day
}

/** Capture revisions returned with the complete confirmed data being edited.
 * This does not download or refresh anything. The caller must not use paginated,
 * optimistic or separately fetched revisions to assert completeness.
 */
export class CollectionBaseline {
  readonly guards: readonly CollectionGuard[]
  readonly headers: Readonly<{ 'X-Collection-Guards': string }>

  constructor(source: { complete: boolean; guards: readonly CollectionGuard[] }) {
    if (!source.complete) throw new Error('集合资料不完整，请先完成读取。')
    const seen = new Set<string>()
    const guards = source.guards.map((guard) => {
      if (
        !kinds.includes(guard.kind) ||
        !validScope(guard) ||
        guard.revision.length !== 71 ||
        !/^sha256:[0-9a-f]{64}$/.test(guard.revision)
      ) {
        throw new Error('集合基线无效。')
      }
      const key = `${guard.kind}/${guard.scope_id}`
      if (seen.has(key)) throw new Error('集合基线重复。')
      seen.add(key)
      return Object.freeze({ kind: guard.kind, scope_id: guard.scope_id, revision: guard.revision })
    })
    guards.sort((a, b) => {
      const left = `${a.kind}/${a.scope_id}`
      const right = `${b.kind}/${b.scope_id}`
      return left < right ? -1 : left > right ? 1 : 0
    })
    const encoded = JSON.stringify(guards)
    // Validated values and keys contain only ASCII, so length equals UTF-8 bytes.
    if (encoded.length > 16 * 1024) throw new Error('集合基线请求超过16KiB。')
    this.guards = Object.freeze(guards)
    this.headers = Object.freeze({ 'X-Collection-Guards': encoded })
    Object.freeze(this)
  }

  /** Include guards in createWriteIntent.key; changed baselines require a new ID.
   * Callers keep this baseline and their input on failure. Explicit reconciliation
   * creates a new baseline; no automatic retry against a freshly fetched revision.
   */
  intent<T>(request: T): { request: T; guards: readonly CollectionGuard[] } {
    return { request, guards: this.guards }
  }
}
