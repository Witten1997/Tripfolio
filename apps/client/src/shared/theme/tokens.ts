/**
 * 主题令牌契约：页面与组件只允许引用 --tf-* 变量，每套主题必须给清单中的每个令牌赋值。
 * 清单与 docs/architecture/2026-09-13-前端主题体系设计.md 第 3 节保持一致。
 */
export const REQUIRED_TOKENS = [
  // 强调色与语义色
  'accent',
  'accent-contrast',
  'accent-soft',
  'success',
  'success-soft',
  'warning',
  'warning-soft',
  'danger',
  'danger-soft',
  'info',
  'info-soft',
  // 表面与文字
  'canvas',
  'surface',
  'surface-raised',
  'surface-sunken',
  'text-1',
  'text-2',
  'text-3',
  'text-disabled',
  'line',
  'line-soft',
  'overlay',
  // 形状与表面效果
  'radius-control',
  'radius-card',
  'radius-panel',
  'radius-organic',
  'surface-blur',
  'surface-border',
  'surface-highlight',
  // 光影与背景
  'shadow-1',
  'shadow-2',
  'shadow-3',
  'backdrop',
  // 动效
  'ease',
  'duration-fast',
  'duration',
  // 图表色板（切片 4 接入 ECharts 时读取）
  'chart-1',
  'chart-2',
  'chart-3',
  'chart-4',
  'chart-5',
  'chart-6',
] as const

export type TokenName = (typeof REQUIRED_TOKENS)[number]

/** 必须是可解析颜色的令牌（用于格式与对比度校验）。 */
export const COLOR_TOKENS: readonly TokenName[] = [
  'accent',
  'accent-contrast',
  'accent-soft',
  'success',
  'success-soft',
  'warning',
  'warning-soft',
  'danger',
  'danger-soft',
  'info',
  'info-soft',
  'canvas',
  'surface',
  'surface-raised',
  'surface-sunken',
  'text-1',
  'text-2',
  'text-3',
  'text-disabled',
  'line',
  'line-soft',
  'overlay',
  'chart-1',
  'chart-2',
  'chart-3',
  'chart-4',
  'chart-5',
  'chart-6',
]

export interface Rgba {
  r: number
  g: number
  b: number
  a: number
}

const COMMENT = /\/\*[\s\S]*?\*\//g
const DECLARATION = /--tf-([a-z0-9-]+)\s*:\s*([^;]+);/g
const HEX = /^#([0-9a-f]{6})$/i
const RGB = /^rgb\(\s*(\d{1,3})\s+(\d{1,3})\s+(\d{1,3})\s*(?:\/\s*([0-9.]+%?)\s*)?\)$/i

/** 从一份主题 CSS 中提取 --tf-* 声明，键不含前缀。 */
export function parseThemeTokens(css: string): Map<string, string> {
  const tokens = new Map<string, string>()
  for (const match of css.replace(COMMENT, '').matchAll(DECLARATION)) {
    tokens.set(match[1]!, match[2]!.trim())
  }
  return tokens
}

/** 只接受 #rrggbb 与 rgb(r g b / a) 两种写法（设计文档第 3 节）。 */
export function parseColor(value: string): Rgba | null {
  const text = value.trim()
  const hex = HEX.exec(text)
  if (hex) {
    const n = Number.parseInt(hex[1]!, 16)
    return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255, a: 1 }
  }
  const rgb = RGB.exec(text)
  if (!rgb) return null
  const alphaText = rgb[4]
  const a =
    alphaText === undefined
      ? 1
      : alphaText.endsWith('%')
        ? Number(alphaText.slice(0, -1)) / 100
        : Number(alphaText)
  return { r: Number(rgb[1]), g: Number(rgb[2]), b: Number(rgb[3]), a }
}

/** 把带透明度的前景色合成到背景上（背景视为不透明）。 */
export function composite(fg: Rgba, bg: Rgba): Rgba {
  const mix = (f: number, b: number) => Math.round(f * fg.a + b * (1 - fg.a))
  return { r: mix(fg.r, bg.r), g: mix(fg.g, bg.g), b: mix(fg.b, bg.b), a: 1 }
}

function channel(value: number): number {
  const s = value / 255
  return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
}

function luminance(color: Rgba): number {
  return 0.2126 * channel(color.r) + 0.7152 * channel(color.g) + 0.0722 * channel(color.b)
}

/** WCAG 对比度；前景若半透明先合成。 */
export function contrastRatio(fg: Rgba, bg: Rgba): number {
  const l1 = luminance(composite(fg, bg))
  const l2 = luminance(bg)
  const [hi, lo] = l1 > l2 ? [l1, l2] : [l2, l1]
  return (hi + 0.05) / (lo + 0.05)
}

/** HSL 色相（0–360）与饱和度（0–1）。 */
export function hueSaturation(color: Rgba): { hue: number; saturation: number } {
  const r = color.r / 255
  const g = color.g / 255
  const b = color.b / 255
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const delta = max - min
  if (delta === 0) return { hue: 0, saturation: 0 }
  const lightness = (max + min) / 2
  const saturation = delta / (1 - Math.abs(2 * lightness - 1))
  let hue: number
  if (max === r) hue = ((g - b) / delta) % 6
  else if (max === g) hue = (b - r) / delta + 2
  else hue = (r - g) / delta + 4
  hue *= 60
  if (hue < 0) hue += 360
  return { hue, saturation }
}

/** 全站配色禁止蓝紫色（用户 2026-09-13 决定）：色相 190–310 且饱和度高于 0.1 视为违规。 */
export function isBluePurple(color: Rgba): boolean {
  const { hue, saturation } = hueSaturation(color)
  return saturation > 0.1 && hue >= 190 && hue <= 310
}
