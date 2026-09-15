import { describe, expect, it } from 'vitest'

import { installNoIndex } from './robots'

describe('installNoIndex', () => {
  it('注入 noindex 并可移除，不重复注入', () => {
    const doc = document.implementation.createHTMLDocument('t')
    const remove = installNoIndex(doc)
    const second = installNoIndex(doc)
    expect(doc.head.querySelectorAll('meta[name="robots"]')).toHaveLength(1)
    expect(doc.head.querySelector('meta[name="robots"]')?.getAttribute('content')).toBe(
      'noindex, nofollow',
    )
    second()
    remove()
    expect(doc.head.querySelector('meta[name="robots"]')).toBeNull()
  })
})
