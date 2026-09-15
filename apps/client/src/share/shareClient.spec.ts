import { describe, expect, it, vi } from 'vitest'

import { createShareClient } from './shareClient'

function problem(status: number, code: string) {
  return new Response(
    JSON.stringify({ type: 'about:blank', title: code, status, code, request_id: 'r' }),
    {
      status,
      headers: { 'Content-Type': 'application/problem+json' },
    },
  )
}

describe('createShareClient', () => {
  it('只注入 Share 令牌，不带 Bearer', async () => {
    const fetch = vi.fn(async (req: Request) => {
      expect(req.headers.get('Authorization')).toBe('Share t000000000000000000001')
      return new Response(
        JSON.stringify({
          data: {
            name: '东京',
            destination: '',
            start_date: '2026-10-01',
            end_date: '2026-10-07',
            timezone: 'Asia/Tokyo',
          },
        }),
        {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        },
      )
    })
    const client = createShareClient('t000000000000000000001', {
      baseUrl: 'http://api.test/v1',
      fetch,
    })
    const { data } = await client.GET('/public/trip')
    expect(data?.data.name).toBe('东京')
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it.each([
    [401, 'AUTH_REQUIRED'],
    [404, 'SHARE_NOT_FOUND'],
  ] as const)('%s 原样返回，不刷新、不重放', async (status, code) => {
    const fetch = vi.fn(async () => problem(status, code))
    const client = createShareClient('t000000000000000000009', {
      baseUrl: 'http://api.test/v1',
      fetch,
    })
    const { error, response } = await client.GET('/public/trip')
    expect(response.status).toBe(status)
    expect(error?.code).toBe(code)
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})
