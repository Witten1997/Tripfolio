import { describe, expect, it } from 'vitest'

import { parseThemeTokens, REQUIRED_TOKENS } from './tokens'

const files = import.meta.glob<string>('../../styles/**/*.css', {
  query: '?raw',
  import: 'default',
  eager: true,
})

/** base.css 自己定义的跨主题常量，桥接与页面也可以引用。 */
const BASE_TOKENS = ['font-body', 'font-display', 'control-size']

describe('桥接与基础样式', () => {
  it('按钮和显式筛选组共享控制高度，行李圆形勾选保留命中区和焦点', () => {
    const css = files['../../styles/bridge/element-plus.css']!
    expect(css).toMatch(
      /\.tf-filter-controls \.el-select__wrapper,[\s\S]*?\.tf-filter-controls \.el-radio-button__inner\s*\{[^}]*min-height:\s*var\(--tf-control-size\)/,
    )
    expect(css).toMatch(
      /\.tf-round-check\.el-checkbox\s*\{[^}]*min-height:\s*var\(--tf-control-size\)/,
    )
    expect(css).toMatch(/\.tf-round-check \.el-checkbox__inner\s*\{[^}]*border-radius:\s*50%/)
    expect(css).toContain('.el-checkbox__original:focus-visible + .el-checkbox__inner')
  })
  it.each(BASE_TOKENS)('基础层定义跨主题常量 --tf-%s', (name) => {
    expect(parseThemeTokens(files['../../styles/base.css']!).has(name)).toBe(true)
  })
  it('存在两份桥接与 base.css', () => {
    expect(Object.keys(files).sort()).toEqual([
      '../../styles/base.css',
      '../../styles/bridge/element-plus.css',
      '../../styles/bridge/vant.css',
    ])
  })
  it.each(Object.keys(files))('%s 只引用契约内的 --tf-* 令牌', (path) => {
    const allowed = new Set<string>([...REQUIRED_TOKENS, ...BASE_TOKENS])
    const used = [...files[path]!.matchAll(/var\(--tf-([a-z0-9-]+)/g)].map((m) => m[1]!)
    expect(used.length).toBeGreaterThan(0)
    for (const name of used) expect(allowed.has(name), `--tf-${name}`).toBe(true)
  })
})
