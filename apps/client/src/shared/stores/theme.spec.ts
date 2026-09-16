import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { THEME_PREFERENCE_KEY, useThemeStore } from './theme'

const store = vi.hoisted(() => new Map<string, string>())
vi.mock('@/platform', () => ({
  platform: {
    kind: 'web',
    isNative: false,
    preferences: {
      get: async (key: string) => store.get(key) ?? null,
      set: async (key: string, value: string) => void store.set(key, value),
      remove: async (key: string) => void store.delete(key),
    },
  },
}))

beforeEach(() => {
  setActivePinia(createPinia())
  store.clear()
  document.documentElement.removeAttribute('data-theme')
})

describe('useThemeStore', () => {
  it('访客只用默认主题且不改写主人的偏好', async () => {
    store.set(THEME_PREFERENCE_KEY, 'organic')
    const theme = useThemeStore()
    await theme.restore({ defaultOnly: true })
    expect(theme.current).toBe('glass')
    expect(document.documentElement.dataset.theme).toBe('glass')
    expect(store.get(THEME_PREFERENCE_KEY)).toBe('organic')
  })
  it('没有持久化值时恢复为默认主题', async () => {
    const theme = useThemeStore()
    await theme.restore()
    expect(theme.current).toBe('glass')
    expect(theme.ready).toBe(true)
    expect(document.documentElement.dataset.theme).toBe('glass')
  })

  it('持久化值合法时恢复该主题，非法时回退默认', async () => {
    store.set(THEME_PREFERENCE_KEY, 'organic')
    const theme = useThemeStore()
    await theme.restore()
    expect(theme.current).toBe('organic')

    store.set(THEME_PREFERENCE_KEY, 'neon')
    setActivePinia(createPinia())
    const again = useThemeStore()
    await again.restore()
    expect(again.current).toBe('glass')
  })

  it('apply 切换并持久化，重复应用同一主题不重复写入', async () => {
    const theme = useThemeStore()
    await theme.restore()
    await theme.apply('organic')
    expect(theme.current).toBe('organic')
    expect(theme.definition.name).toBe('有机自然')
    expect(store.get(THEME_PREFERENCE_KEY)).toBe('organic')
    expect(document.documentElement.dataset.theme).toBe('organic')

    store.delete(THEME_PREFERENCE_KEY)
    await theme.apply('organic')
    expect(store.has(THEME_PREFERENCE_KEY)).toBe(false)
  })
})
