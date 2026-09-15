import { describe, expect, it } from 'vitest'

import baseStyles from '@/styles/base.css?raw'
import elementStyles from '@/styles/bridge/element-plus.css?raw'

/** 页面、壳与组件的样式只能引用 --tf-* 令牌；违反时在这里失败，而不是在视觉审查时才发现。 */
const files = import.meta.glob<string>('../../{desktop,mobile,share}/**/*.{vue,css}', {
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

  it('图标与同组文字按钮共享控制尺寸，不使用互相冲突的固定高度', () => {
    const iconStyles = files['../../desktop/components/IconAction.vue']!
    expect(baseStyles).toContain('--tf-control-size: 40px;')
    expect(iconStyles).toContain('width: var(--tf-control-size);')
    expect(iconStyles).toContain('height: var(--tf-control-size);')
    expect(iconStyles).not.toMatch(/(?:width|height): (?:40|44)px;/)
    expect(elementStyles).toMatch(
      /\.tf-actions > \.el-button,\s*\.tf-actions > a > \.el-button \{[^}]*min-height: var\(--tf-control-size\);/,
    )
    expect(elementStyles).toContain('white-space: normal;')
  })

  it('窄屏及触屏统一使用 44px 操作区域，字号增加时文字按钮允许长高', () => {
    expect(baseStyles).toMatch(
      /@media \(max-width: 767px\), \(pointer: coarse\) \{\s*:root \{\s*--tf-control-size: 44px;/,
    )
    expect(elementStyles).toMatch(
      /\.tf-actions > \.el-button,\s*\.tf-actions > a > \.el-button \{\s*height: auto;/,
    )
  })

  it.each(['../../desktop/DesktopShell.vue', '../../mobile/MobileShell.vue'])(
    '%s 为复用的业务页提供组件样式、中文配置与旅行隔离',
    (path) => {
      const source = files[path]!
      const vendor = source.indexOf("import 'element-plus/dist/index.css'")
      const bridge = source.indexOf("import '@/styles/bridge/element-plus.css'")
      expect(vendor).toBeGreaterThanOrEqual(0)
      expect(bridge).toBeGreaterThan(vendor)
      expect(source).toContain('<ElConfigProvider :locale="zhCn">')
      expect(source).toContain(':key="String($route.params.tripId ?? \'\')"')
    },
  )

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
