import { describe, expect, it } from 'vitest'

import {
  COLOR_TOKENS,
  REQUIRED_TOKENS,
  composite,
  contrastRatio,
  isBluePurple,
  parseColor,
  parseThemeTokens,
} from './tokens'

describe('parseThemeTokens', () => {
  it('提取 --tf-* 声明并忽略注释与非 tf 变量', () => {
    const css = `
      /* 注释里的 --tf-fake: #000; 不算 */
      [data-theme='x'] {
        --tf-accent: #4f7a4a;
        --el-color-primary: red;
        --tf-radius-organic: 62% 38% 46% 54% / 50% 42% 58% 50%;
        --tf-backdrop: radial-gradient(circle at 20% 10%, #fff 0, transparent 60%), #f4efe6;
      }
    `
    const tokens = parseThemeTokens(css)
    expect([...tokens.keys()]).toEqual(['accent', 'radius-organic', 'backdrop'])
    expect(tokens.get('radius-organic')).toBe('62% 38% 46% 54% / 50% 42% 58% 50%')
  })
})

describe('parseColor', () => {
  it('支持 #rrggbb 与 rgb(r g b / a)', () => {
    expect(parseColor('#4f7a4a')).toEqual({ r: 79, g: 122, b: 74, a: 1 })
    expect(parseColor('rgb(43 42 36 / 0.45)')).toEqual({ r: 43, g: 42, b: 36, a: 0.45 })
    expect(parseColor('rgb(255 255 255 / 16%)')).toEqual({ r: 255, g: 255, b: 255, a: 0.16 })
  })
  it('拒绝其它写法', () => {
    expect(parseColor('#fff')).toBeNull()
    expect(parseColor('rgba(0,0,0,0.5)')).toBeNull()
    expect(parseColor('white')).toBeNull()
  })
})

describe('contrastRatio', () => {
  it('黑白为 21，同色为 1', () => {
    const black = parseColor('#000000')!
    const white = parseColor('#ffffff')!
    expect(contrastRatio(black, white)).toBeCloseTo(21, 1)
    expect(contrastRatio(white, white)).toBe(1)
  })
  it('半透明前景先与背景合成', () => {
    const fg = parseColor('rgb(255 255 255 / 0.5)')!
    const bg = parseColor('#000000')!
    expect(composite(fg, bg)).toEqual({ r: 128, g: 128, b: 128, a: 1 })
    expect(contrastRatio(fg, bg)).toBeGreaterThan(4)
    expect(contrastRatio(fg, bg)).toBeLessThan(21)
  })
})

describe('令牌清单', () => {
  it('颜色令牌都在必需清单内且无重复', () => {
    expect(new Set(REQUIRED_TOKENS).size).toBe(REQUIRED_TOKENS.length)
    for (const name of COLOR_TOKENS) expect(REQUIRED_TOKENS).toContain(name)
  })
})

describe('isBluePurple', () => {
  it('蓝色与紫色违规，青绿、暖色与中性灰不违规', () => {
    expect(isBluePurple(parseColor('#8db4ff')!)).toBe(true)
    expect(isBluePurple(parseColor('#7048e8')!)).toBe(true)
    expect(isBluePurple(parseColor('#1f7a6d')!)).toBe(false)
    expect(isBluePurple(parseColor('#c4553d')!)).toBe(false)
    expect(isBluePurple(parseColor('#5d5a50')!)).toBe(false)
    expect(isBluePurple(parseColor('rgb(30 40 35 / 0.14)')!)).toBe(false)
  })
})
