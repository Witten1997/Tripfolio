import { describe, expect, it } from 'vitest'

import { isSharePath } from './path'

describe('isSharePath', () => {
  it('识别带令牌的分享路径', () => {
    expect(isSharePath('/s/t000000000000000000001', '/')).toBe(true)
    expect(isSharePath('/app/s/abc', '/app/')).toBe(true)
  })
  it('拒绝空令牌、其他页面与相似前缀', () => {
    expect(isSharePath('/s/', '/')).toBe(false)
    expect(isSharePath('/trips/1', '/')).toBe(false)
    expect(isSharePath('/settings', '/')).toBe(false)
  })
})
