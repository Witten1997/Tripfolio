import { beforeEach, describe, expect, it, vi } from 'vitest'
const { fetcher } = vi.hoisted(() => ({ fetcher: vi.fn() }))
vi.mock('./client', async () => ({
  api: (await import('openapi-fetch')).default({
    baseUrl: 'https://tripfolio.test/api/v1',
    fetch: fetcher,
  }),
  registerRefreshHandler: vi.fn(),
}))
import { createPhoto, updatePhoto } from './photos'
import { CollectionBaseline } from './collectionGuards'

const trip = '11111111-1111-4111-8111-111111111111'
const id = '22222222-2222-4222-8222-222222222222'
const day = '2026-10-06'
const guard = {
  kind: 'photo_day' as const,
  scope_id: `${trip}/${day}`,
  revision: `sha256:${'a'.repeat(64)}`,
}
const baseline = new CollectionBaseline({ complete: true, guards: [guard] })
const receipt = {
  warnings: [],
  scope_revisions: [guard],
  replayed: true,
  data: { id, version: '99' },
}
beforeEach(() =>
  fetcher.mockReset().mockImplementation(async () => Response.json({ data: receipt })),
)

describe('照片写请求', () => {
  it.each([{ recorded_on: day }, { taken_at_local: null }, { sort_order: 0 }])(
    '同值敏感字段仍发送冻结基线 %j',
    async (patch) => {
      const outcome = await updatePhoto(
        trip,
        id,
        '9007199254740993',
        patch,
        'original-key',
        baseline,
      )
      const request = fetcher.mock.calls[0]![0] as Request
      expect(request.method).toBe('PATCH')
      expect(request.headers.get('If-Match')).toBe('"9007199254740993"')
      expect(request.headers.get('Idempotency-Key')).toBe('original-key')
      expect(JSON.parse(request.headers.get('X-Collection-Guards')!)).toEqual([guard])
      expect(await request.json()).toEqual(patch)
      expect(outcome.result.scope_revisions).toEqual([guard])
      expect(outcome.resource?.version).toBe('99')
    },
  )
  it('说明独立修改不附集合头，显式无关基线本地拒绝', async () => {
    await updatePhoto(trip, id, '1', { caption: '说明' }, 'key')
    expect((fetcher.mock.calls[0]![0] as Request).headers.has('X-Collection-Guards')).toBe(false)
    await expect(updatePhoto(trip, id, '1', { caption: '说明' }, 'key', baseline)).rejects.toThrow(
      '不匹配',
    )
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it('拒绝其他旅行或集合的基线', async () => {
    for (const value of [
      { ...guard, scope_id: `${id}/${day}` },
      { ...guard, kind: 'members' as const, scope_id: trip },
    ]) {
      await expect(
        updatePhoto(
          trip,
          id,
          '1',
          { sort_order: 0 },
          'key',
          new CollectionBaseline({ complete: true, guards: [value] }),
        ),
      ).rejects.toThrow('不匹配')
    }
    expect(fetcher).not.toHaveBeenCalled()
  })
  it('创建正文区分省略顺序和显式零，均不附guard', async () => {
    for (const extra of [{}, { sort_order: 0 }]) {
      const body = { id, asset_id: id, recorded_on: day, ...extra }
      await createPhoto(trip, body, 'key')
      const request = fetcher.mock.lastCall![0] as Request
      expect(await request.json()).toEqual(body)
      expect(request.headers.has('X-Collection-Guards')).toBe(false)
    }
  })
  it.each([412, 428])('原样透传%s且不读取新基线或自动重试', async (status) => {
    fetcher.mockResolvedValue(
      Response.json(
        { status, code: status === 412 ? 'COLLECTION_CONFLICT' : 'COLLECTION_BASE_REQUIRED' },
        { status },
      ),
    )
    await expect(
      updatePhoto(trip, id, '1', { sort_order: 0 }, 'key', baseline),
    ).rejects.toMatchObject({ problem: { status } })
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it('重放无资源仍保留原集合事实', async () => {
    fetcher.mockResolvedValue(Response.json({ data: { ...receipt, data: null } }))
    const result = await updatePhoto(trip, id, '1', { sort_order: 0 }, 'key', baseline)
    expect(result.resource).toBeNull()
    expect(result.result.scope_revisions).toEqual([guard])
  })
})
