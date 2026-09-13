import { describe, expect, it } from 'vitest'

import { buildRoutes } from './routes'

describe('buildRoutes', () => {
  it.each(['desktop', 'mobile'] as const)('%s 壳提供公开的主题中心路由', (shell) => {
    const route = buildRoutes(shell).find((r) => r.name === 'themes')
    expect(route?.path).toBe('/themes')
    expect(route?.meta?.public).toBe(true)
    expect(route?.meta?.guestOnly).toBeUndefined()
  })
})
