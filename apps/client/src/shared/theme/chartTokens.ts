import { onScopeDispose, ref, watch, type Ref } from 'vue'

import { useThemeStore } from '@/shared/stores/theme'

/**
 * ECharts 的颜色只能是 JS 字符串，不能写 var(--tf-*)。
 * 这里在运行时从文档读取主题令牌，保证图表配色仍然只来自 --tf-* 契约，
 * 并在切换主题后重新读取。
 */
export interface ChartTokens {
  /** --tf-chart-1..6，按固定顺序分配，不循环取用。 */
  series: string[]
  /** 折叠出来的“其他分类”用中性色，与分类色板区分。 */
  neutral: string
  text: string
  textMuted: string
  line: string
  surface: string
  accent: string
  danger: string
}

const FALLBACK: ChartTokens = {
  series: ['#2f6b34', '#d19a2f', '#12998d', '#d1673c', '#9c3f5e', '#8f9a3a'],
  neutral: '#7d7868',
  text: '#2b2a24',
  textMuted: '#7d7868',
  line: '#e8e1d3',
  surface: '#fffdf8',
  accent: '#2f6b34',
  danger: '#c4553d',
}

function readToken(styles: CSSStyleDeclaration, name: string, fallback: string): string {
  const value = styles.getPropertyValue(`--tf-${name}`).trim()
  return value || fallback
}

export function readChartTokens(doc: Document = document): ChartTokens {
  const styles = doc.defaultView?.getComputedStyle(doc.documentElement)
  if (!styles) return FALLBACK
  return {
    series: FALLBACK.series.map((fallback, index) =>
      readToken(styles, `chart-${index + 1}`, fallback),
    ),
    neutral: readToken(styles, 'text-3', FALLBACK.neutral),
    text: readToken(styles, 'text-1', FALLBACK.text),
    textMuted: readToken(styles, 'text-3', FALLBACK.textMuted),
    line: readToken(styles, 'line-soft', FALLBACK.line),
    surface: readToken(styles, 'surface-raised', FALLBACK.surface),
    accent: readToken(styles, 'accent', FALLBACK.accent),
    danger: readToken(styles, 'danger', FALLBACK.danger),
  }
}

/** 随主题切换刷新的图表令牌。 */
export function useChartTokens(): Ref<ChartTokens> {
  const theme = useThemeStore()
  const tokens = ref<ChartTokens>(readChartTokens())
  const refresh = () => {
    tokens.value = readChartTokens()
  }
  // 主题 CSS 在 activate 里已加载并应用，current 变化后读到的就是新令牌
  const stop = watch(() => theme.current, refresh)
  onScopeDispose(stop)
  return tokens
}
