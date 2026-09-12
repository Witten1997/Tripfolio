import { describe, expect, it } from 'vitest'

import { MOBILE_MAX_WIDTH, pickShell } from './pickShell'

describe('pickShell', () => {
  it('原生环境一律使用移动壳', () => {
    expect(pickShell({ isNative: true, viewportWidth: 1920 })).toBe('mobile')
  })

  it('窄视口使用移动壳，边界值包含在内', () => {
    expect(pickShell({ isNative: false, viewportWidth: MOBILE_MAX_WIDTH })).toBe('mobile')
    expect(pickShell({ isNative: false, viewportWidth: 375 })).toBe('mobile')
  })

  it('宽视口使用桌面壳', () => {
    expect(pickShell({ isNative: false, viewportWidth: MOBILE_MAX_WIDTH + 1 })).toBe('desktop')
    expect(pickShell({ isNative: false, viewportWidth: 1440 })).toBe('desktop')
  })
})
