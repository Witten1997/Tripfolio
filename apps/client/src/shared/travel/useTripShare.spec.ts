import { describe, expect, it, vi } from 'vitest'

import type { TripShare } from '@/shared/api/shares'

import { useTripShare, type TripShareDeps } from './useTripShare'

const sample = (token: string): TripShare => ({
  id: '019ed632-51c0-7000-8000-000000000001',
  token,
  url: `https://trip.example.com/s/${token}`,
  created_at: '2026-09-15T08:00:00Z',
  rotated_at: null,
  last_viewed_at: null,
  view_count: 0,
})

function fakeDeps(overrides: Partial<TripShareDeps> = {}) {
  const copied: string[] = []
  const deps: TripShareDeps = {
    get: vi.fn(async () => null),
    enable: vi.fn(async () => sample('t000000000000000000001')),
    rotate: vi.fn(async () => sample('t000000000000000000002')),
    disable: vi.fn(async () => {}),
    copy: vi.fn(async (text: string) => {
      copied.push(text)
    }),
    ...overrides,
  }
  return { deps, copied }
}

describe('useTripShare', () => {
  it('未开启时为 off，生成后为 on 并可复制链接', async () => {
    const { deps, copied } = fakeDeps()
    const s = useTripShare('trip-1', deps)
    await s.load()
    expect(s.status.value).toBe('off')
    await s.enable()
    expect(s.status.value).toBe('on')
    expect(s.share.value?.url).toBe('https://trip.example.com/s/t000000000000000000001')
    await s.copy()
    expect(copied).toEqual(['https://trip.example.com/s/t000000000000000000001'])
    expect(s.copied.value).toBe(true)
  })

  it('重新生成换令牌并清除已复制状态，关闭后回到 off', async () => {
    const { deps } = fakeDeps({ get: vi.fn(async () => sample('t000000000000000000001')) })
    const s = useTripShare('trip-1', deps)
    await s.load()
    expect(s.status.value).toBe('on')
    await s.copy()
    await s.rotate()
    expect(s.share.value?.token).toBe('t000000000000000000002')
    expect(s.copied.value).toBe(false)
    await s.disable()
    expect(s.status.value).toBe('off')
    expect(s.share.value).toBeNull()
    expect(deps.disable).toHaveBeenCalledWith('trip-1')
  })

  it('读取失败进入 error 并给出文案；复制失败提示手动复制', async () => {
    const { deps } = fakeDeps({
      get: vi.fn(async () => {
        throw new TypeError('offline')
      }),
      copy: vi.fn(async () => {
        throw new Error('denied')
      }),
    })
    const s = useTripShare('trip-1', deps)
    await s.load()
    expect(s.status.value).toBe('error')
    expect(s.error.value).toBe('无法读取分享状态，请稍后重试')
    s.share.value = sample('t000000000000000000001')
    await s.copy()
    expect(s.error.value).toBe('复制失败，请手动选中链接复制')
  })

  it('动作进行中忽略重复触发', async () => {
    let resolveEnable!: (v: TripShare) => void
    const enable = vi.fn(() => new Promise<TripShare>((resolve) => (resolveEnable = resolve)))
    const { deps } = fakeDeps({ enable })
    const s = useTripShare('trip-1', deps)
    const first = s.enable()
    void s.enable()
    expect(enable).toHaveBeenCalledTimes(1)
    resolveEnable(sample('t000000000000000000001'))
    await first
    expect(s.busy.value).toBe(false)
  })
})
