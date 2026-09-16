/**
 * 主题注册表：新增主题只需新建 src/themes/<id>/ 三个文件并在此登记一条。
 * load 用动态 import，让每套主题的 CSS 单独成 chunk，首屏只加载当前主题。
 */
export const themeIds = ['organic', 'glass'] as const

export type ThemeId = (typeof themeIds)[number]

export interface ThemeDefinition {
  id: ThemeId
  name: string
  description: string
  /** 决定 color-scheme 与 Vant 的暗色类。 */
  colorScheme: 'light' | 'dark'
  /** 主题中心的色块与 <meta name="theme-color">。 */
  preview: { canvas: string; surface: string; accent: string }
  load: () => Promise<unknown>
}

export const DEFAULT_THEME: ThemeId = 'glass'

export const themes: readonly ThemeDefinition[] = [
  {
    id: 'organic',
    name: '有机自然',
    description: '流动的形状、自然的色调与柔和的曲线。',
    colorScheme: 'light',
    preview: { canvas: '#f4efe6', surface: '#fffdf8', accent: '#4f7a4a' },
    load: () => import('@/themes/organic/index.css'),
  },
  {
    id: 'glass',
    name: '玻璃态',
    description: '半透明的白色玻璃、模糊的背景与通透的光感。',
    colorScheme: 'light',
    preview: { canvas: '#e6ece6', surface: '#ffffff', accent: '#1f7a6d' },
    load: () => import('@/themes/glass/index.css'),
  },
]

export function isThemeId(value: unknown): value is ThemeId {
  return typeof value === 'string' && (themeIds as readonly string[]).includes(value)
}

export function findTheme(id: ThemeId): ThemeDefinition {
  const theme = themes.find((t) => t.id === id)
  if (!theme) throw new Error(`未注册的主题：${id}`)
  return theme
}
