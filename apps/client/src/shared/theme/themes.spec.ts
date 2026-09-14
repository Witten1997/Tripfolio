import { describe, expect, it } from 'vitest'

import { DEFAULT_THEME, findTheme, isThemeId, themeIds, themes } from './registry'
import {
  CHART_ADJACENT_DELTA_MIN,
  CHART_TOKENS,
  COLOR_TOKENS,
  REQUIRED_TOKENS,
  composite,
  contrastRatio,
  deltaEOk,
  isBluePurple,
  parseColor,
  parseThemeTokens,
} from './tokens'

const files = import.meta.glob<string>('../../themes/*/tokens.css', {
  query: '?raw',
  import: 'default',
  eager: true,
})
const decors = import.meta.glob<string>('../../themes/*/decor.css', {
  query: '?raw',
  import: 'default',
  eager: true,
})
/** 装饰层允许字面颜色，但同样不得出现蓝紫色。 */
const LITERAL_COLOR = /#[0-9a-f]{6}|rgb\([^)]*\)/gi

function tokensOf(id: string) {
  const css = files[`../../themes/${id}/tokens.css`]
  if (css === undefined) throw new Error(`缺少 src/themes/${id}/tokens.css`)
  return { css, tokens: parseThemeTokens(css) }
}

describe('主题注册表', () => {
  it('每个 themeIds 都有定义，默认主题存在', () => {
    expect(themes.map((t) => t.id)).toEqual([...themeIds])
    expect(findTheme(DEFAULT_THEME).id).toBe(DEFAULT_THEME)
    expect(isThemeId('glass')).toBe(true)
    expect(isThemeId('neon')).toBe(false)
    expect(isThemeId(null)).toBe(false)
  })
  it('预览色可解析且不是蓝紫色', () => {
    for (const theme of themes) {
      for (const value of Object.values(theme.preview)) {
        const color = parseColor(value)
        expect(color).not.toBeNull()
        expect(isBluePurple(color!), `${theme.id} ${value}`).toBe(false)
      }
    }
  })
})

describe.each(themeIds)('主题 %s 的令牌文件', (id) => {
  it('只在自己的 data-theme 选择器下赋值', () => {
    const { css } = tokensOf(id)
    expect(css).toContain(`[data-theme='${id}']`)
    for (const other of themeIds) if (other !== id) expect(css).not.toContain(`'${other}'`)
  })
  it('定义的令牌集合与 REQUIRED_TOKENS 完全一致', () => {
    const { tokens } = tokensOf(id)
    expect([...tokens.keys()].sort()).toEqual([...REQUIRED_TOKENS].sort())
  })
  it('颜色令牌格式合法', () => {
    const { tokens } = tokensOf(id)
    for (const name of COLOR_TOKENS) {
      expect(parseColor(tokens.get(name)!), `--tf-${name}`).not.toBeNull()
    }
  })
  it('颜色令牌与装饰层都不出现蓝紫色', () => {
    const { tokens } = tokensOf(id)
    for (const name of COLOR_TOKENS) {
      expect(isBluePurple(parseColor(tokens.get(name)!)!), `--tf-${name}`).toBe(false)
    }
    const decor = decors[`../../themes/${id}/decor.css`] ?? ''
    for (const [literal] of decor.matchAll(LITERAL_COLOR)) {
      const color = parseColor(literal)
      if (color) expect(isBluePurple(color), `decor ${literal}`).toBe(false)
    }
  })
  it('阴影类令牌不写 none', () => {
    const { tokens } = tokensOf(id)
    for (const name of ['shadow-1', 'shadow-2', 'shadow-3', 'surface-highlight']) {
      expect(tokens.get(name), `--tf-${name}`).not.toBe('none')
    }
  })
  it('文字与强调色对比度达标', () => {
    const { tokens } = tokensOf(id)
    const color = (name: string) => parseColor(tokens.get(name)!)!
    const canvas = color('canvas')
    const surface = composite(color('surface'), canvas)
    for (const bg of [canvas, surface]) {
      expect(contrastRatio(color('text-1'), bg)).toBeGreaterThanOrEqual(4.5)
      expect(contrastRatio(color('text-2'), bg)).toBeGreaterThanOrEqual(4.5)
      expect(contrastRatio(color('text-3'), bg)).toBeGreaterThanOrEqual(3)
    }
    expect(contrastRatio(color('accent-contrast'), color('accent'))).toBeGreaterThanOrEqual(4.5)
  })
  // 图表按 chart-1..6 的固定顺序取色，相邻切片必须能分辨；
  // 完整的色觉障碍校验见 dataviz 技能的 validate_palette，这里守住正常视力硬门槛。
  it('图表色板相邻色可分辨', () => {
    const { tokens } = tokensOf(id)
    const palette = CHART_TOKENS.map((name) => parseColor(tokens.get(name)!)!)
    for (let i = 1; i < palette.length; i++) {
      const delta = deltaEOk(palette[i - 1]!, palette[i]!)
      expect(
        delta,
        `--tf-${CHART_TOKENS[i - 1]} 与 --tf-${CHART_TOKENS[i]} ΔE ${delta.toFixed(1)}`,
      ).toBeGreaterThanOrEqual(CHART_ADJACENT_DELTA_MIN)
    }
  })
})
