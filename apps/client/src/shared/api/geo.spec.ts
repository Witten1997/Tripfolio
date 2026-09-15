import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/shared/api/client'
import { calculateRoute, GeoRouteError, parseRetryAfter } from '@/shared/api/geo'

vi.mock('@/shared/api/client', () => ({ api: { GET: vi.fn() }, registerRefreshHandler: vi.fn() }))
beforeEach(() => vi.mocked(api.GET).mockReset())

describe('算路错误重试语义', () => {
  it('保留响应 Retry-After，支持秒数和 HTTP 日期', async () => {
    expect(parseRetryAfter('3')).toBe(3000)
    expect(
      parseRetryAfter('Tue, 15 Sep 2026 00:00:03 GMT', Date.parse('2026-09-15T00:00:00Z')),
    ).toBe(3000)
    expect(parseRetryAfter(null)).toBeNull()
    expect(parseRetryAfter('invalid')).toBeNull()
    const error = {
      type: 'about:blank',
      code: 'DEPENDENCY_UNAVAILABLE',
      status: 503,
      title: '稍后重试',
      request_id: 'test',
    }
    vi.mocked(api.GET).mockResolvedValue({
      error,
      response: new Response('', { status: 503, headers: { 'Retry-After': '2' } }),
    } as never)
    const point = { latitude: 39, longitude: 116 }
    await expect(calculateRoute(point, point, 'driving')).rejects.toMatchObject({
      code: 'DEPENDENCY_UNAVAILABLE',
      retryAfterMs: 2000,
    })
    expect(new GeoRouteError(error, null).retryAfterMs).toBeNull()
  })
})
