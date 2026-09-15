import { describe, expect, it } from 'vitest'

import { buildRoutes } from './routes'

describe('buildRoutes', () => {
  it.each(['desktop', 'mobile'] as const)('%s 壳提供公开的主题中心路由', (shell) => {
    const route = buildRoutes(shell).find((r) => r.name === 'themes')
    expect(route?.path).toBe('/themes')
    expect(route?.meta?.public).toBe(true)
    expect(route?.meta?.guestOnly).toBeUndefined()
  })

  it('旅行详情按页签提供子路由，默认进入行程', () => {
    const detail = buildRoutes('desktop').find((r) => r.name === 'trip-detail')
    expect(detail?.path).toBe('/trips/:tripId')
    expect(detail?.meta?.public).toBeUndefined()
    const names = detail?.children?.map((c) => c.name)
    expect(names).toEqual([
      'trip-itinerary',
      'trip-ledger',
      'trip-map',
      'trip-packing',
      'trip-album',
      'trip-todos',
    ])
    expect(detail?.redirect).toEqual({ name: 'trip-itinerary' })
  })
})

it.each(['desktop', 'mobile'] as const)('%s 壳提供不要求登录的访客分享页', (shell) => {
  const route = buildRoutes(shell).find((r) => r.name === 'share')
  expect(route?.path).toBe('/s/:token')
  expect(route?.meta).toEqual({ public: true, share: true })
})
