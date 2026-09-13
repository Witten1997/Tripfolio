import { describe, expect, it } from 'vitest'

/** 页面、壳与组件的样式只能引用 --tf-* 令牌；违反时在这里失败，而不是在视觉审查时才发现。 */
const files = import.meta.glob<string>('../../{desktop,mobile}/**/*.{vue,css}', {
  query: '?raw',
  import: 'default',
  eager: true,
})

const STYLE_BLOCK = /<style[^>]*>([\s\S]*?)<\/style>/g
const FORBIDDEN: Array<[RegExp, string]> = [
  [/#(?:[0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})\b/i, '字面十六进制颜色'],
  [/\brgba?\(/i, '字面 rgb 颜色'],
  [/var\(--el-/, 'Element Plus 变量'],
  [/var\(--van-/, 'Vant 变量'],
]

function styleSources(path: string, source: string): string[] {
  if (path.endsWith('.css')) return [source]
  return [...source.matchAll(STYLE_BLOCK)].map((m) => m[1]!)
}

describe('样式契约', () => {
  it('至少扫描到页面文件', () => {
    expect(Object.keys(files).length).toBeGreaterThan(5)
  })

  it.each(Object.keys(files))('%s 的样式只引用 --tf-* 令牌', (path) => {
    const source = files[path]!
    for (const css of styleSources(path, source)) {
      for (const [pattern, label] of FORBIDDEN) {
        expect(pattern.test(css), `${label}：${pattern.exec(css)?.[0]}`).toBe(false)
      }
    }
    // 模板内联颜色属性（如 Vant NoticeBar 的 color/background）同样禁止
    expect(/\b(?:color|background)="#/.test(source), '模板内联字面颜色').toBe(false)
  })
})
