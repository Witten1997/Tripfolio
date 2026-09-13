import { describe, expect, it } from 'vitest'

import { SWITCHING_CLASS, THEME_ATTRIBUTE, applyThemeToDocument } from './dom'
import { findTheme } from './registry'

const nextFrame = () => new Promise((resolve) => setTimeout(resolve, 50))

describe('applyThemeToDocument', () => {
  it('设置 data-theme、color-scheme、Vant 暗色类与 theme-color', async () => {
    const glass = findTheme('glass')
    applyThemeToDocument(document, glass)
    const root = document.documentElement
    expect(root.getAttribute(THEME_ATTRIBUTE)).toBe('glass')
    expect(root.style.colorScheme).toBe(glass.colorScheme)
    expect(root.classList.contains('van-theme-dark')).toBe(glass.colorScheme === 'dark')
    expect(root.classList.contains(SWITCHING_CLASS)).toBe(true)
    expect(document.querySelector('meta[name="theme-color"]')?.getAttribute('content')).toBe(
      glass.preview.canvas,
    )
    await nextFrame()
    expect(root.classList.contains(SWITCHING_CLASS)).toBe(false)
  })

  it('暗色主题加 Vant 暗色类，切回亮色主题时移除，且不重复创建 meta', () => {
    const dark = { ...findTheme('glass'), colorScheme: 'dark' as const }
    applyThemeToDocument(document, dark)
    expect(document.documentElement.classList.contains('van-theme-dark')).toBe(true)
    applyThemeToDocument(document, findTheme('organic'))
    const root = document.documentElement
    expect(root.getAttribute(THEME_ATTRIBUTE)).toBe('organic')
    expect(root.style.colorScheme).toBe('light')
    expect(root.classList.contains('van-theme-dark')).toBe(false)
    expect(document.querySelectorAll('meta[name="theme-color"]')).toHaveLength(1)
  })
})
