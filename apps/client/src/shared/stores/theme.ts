import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { platform } from '@/platform'
import { applyThemeToDocument } from '@/shared/theme/dom'
import { DEFAULT_THEME, findTheme, isThemeId, type ThemeId } from '@/shared/theme/registry'

export const THEME_PREFERENCE_KEY = 'tripfolio.theme'

/**
 * 主题是本地偏好，不进入账号数据。restore 在应用挂载前调用一次；
 * apply 由主题中心调用，加载主题 CSS、应用到文档并持久化。
 */
export const useThemeStore = defineStore('theme', () => {
  const current = ref<ThemeId>(DEFAULT_THEME)
  const ready = ref(false)
  const definition = computed(() => findTheme(current.value))

  async function activate(id: ThemeId) {
    const theme = findTheme(id)
    await theme.load()
    applyThemeToDocument(document, theme)
    current.value = id
  }

  async function restore() {
    let saved: string | null = null
    try {
      saved = await platform.preferences.get(THEME_PREFERENCE_KEY)
    } catch {
      saved = null
    }
    await activate(isThemeId(saved) ? saved : DEFAULT_THEME)
    ready.value = true
  }

  async function apply(id: ThemeId) {
    if (ready.value && id === current.value) return
    await activate(id)
    await platform.preferences.set(THEME_PREFERENCE_KEY, id)
  }

  return { current, ready, definition, restore, apply }
})
