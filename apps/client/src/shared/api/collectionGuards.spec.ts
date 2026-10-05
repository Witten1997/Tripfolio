import { describe, expect, it } from 'vitest'

import { CollectionBaseline, type CollectionGuard } from './collectionGuards'
import { createWriteIntent } from './writes'

const trip = '22222222-2222-4222-8222-222222222222'
const guard: CollectionGuard = {
  kind: 'members',
  scope_id: trip,
  revision: `sha256:${'a'.repeat(64)}`,
}

describe('CollectionBaseline', () => {
  it('captures an immutable copy without rewriting input or refreshing on failure', () => {
    const source = [{ ...guard }]
    const baseline = new CollectionBaseline({ complete: true, guards: source })
    source[0]!.revision = `sha256:${'b'.repeat(64)}`
    source.length = 0
    const input = { members: [{ name: '原始输入' }] }
    const intent = createWriteIntent()
    const operation = intent.key(baseline.intent(input))
    expect(baseline.guards).toEqual([guard])
    expect(Object.isFrozen(baseline)).toBe(true)
    expect(Object.isFrozen(baseline.guards[0])).toBe(true)
    expect(JSON.parse(baseline.headers['X-Collection-Guards'])).toEqual([guard])
    // A failed send has no mutation/reset path. Retrying preserves the original intent.
    expect(intent.key(baseline.intent(input))).toBe(operation)
    expect(input).toEqual({ members: [{ name: '原始输入' }] })
    const reconciled = new CollectionBaseline({
      complete: true,
      guards: [{ ...guard, revision: `sha256:${'b'.repeat(64)}` }],
    })
    expect(intent.key(reconciled.intent(input))).not.toBe(operation)
  })

  it('normalizes scope order for stable request fingerprints without reordering business input', () => {
    const other: CollectionGuard = { ...guard, kind: 'todo_order' }
    const left = new CollectionBaseline({ complete: true, guards: [other, guard] })
    const right = new CollectionBaseline({ complete: true, guards: [guard, other] })
    expect(left.headers).toEqual(right.headers)
    const request = { ordered_ids: ['b', 'a'] }
    expect(left.intent(request).request).toBe(request)
    expect(request.ordered_ids).toEqual(['b', 'a'])
  })

  it('rejects incomplete, malformed, duplicate and oversized baselines', () => {
    expect(() => new CollectionBaseline({ complete: false, guards: [guard] })).toThrow('不完整')
    expect(() => new CollectionBaseline({ complete: true, guards: [guard, guard] })).toThrow('重复')
    for (const invalid of [
      { ...guard, kind: 'unknown' as CollectionGuard['kind'] },
      { ...guard, scope_id: '00000000-0000-0000-0000-000000000000' },
      { ...guard, scope_id: `${trip}/2026-10-05` },
      { ...guard, revision: `sha256:${'A'.repeat(64)}` },
      { ...guard, revision: `${guard.revision}\n` },
      { ...guard, kind: 'photo_day' as const, scope_id: `${trip}/2026-02-29` },
      { ...guard, kind: 'photo_day' as const, scope_id: `${trip}/0000-01-01` },
    ])
      expect(() => new CollectionBaseline({ complete: true, guards: [invalid] })).toThrow('无效')
    const many = Array.from({ length: 200 }, (_, index) => ({
      ...guard,
      scope_id: `${index.toString(16).padStart(8, '0')}-2222-4222-8222-222222222222`,
    }))
    expect(() => new CollectionBaseline({ complete: true, guards: many })).toThrow('16KiB')
  })

  it('accepts leap days and keeps an explicit empty array', () => {
    const baseline = new CollectionBaseline({
      complete: true,
      guards: [{ ...guard, kind: 'photo_day', scope_id: `${trip}/2028-02-29` }],
    })
    expect(baseline.guards).toHaveLength(1)
    expect(
      new CollectionBaseline({ complete: true, guards: [] }).headers['X-Collection-Guards'],
    ).toBe('[]')
  })
})
