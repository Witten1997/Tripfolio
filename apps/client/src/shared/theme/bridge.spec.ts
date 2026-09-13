import { describe, expect, it } from 'vitest'

import { REQUIRED_TOKENS } from './tokens'

const files = import.meta.glob<string>('../../styles/**/*.css', {
  query: '?raw',
  import: 'default',
  eager: true,
})

/** base.css 自己定义的跨主题常量，桥接与页面也可以引用。 */
const BASE_TOKENS = ['font-body', 'font-display']

describe('桥接与基础样式', () => {
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
