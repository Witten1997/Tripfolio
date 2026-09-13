import type { ThemeDefinition } from './registry'

export const THEME_ATTRIBUTE = 'data-theme'
/** 切换瞬间加在 <html> 上，base.css 借此抑制所有过渡一帧。 */
export const SWITCHING_CLASS = 'tf-theme-switching'
const VANT_DARK_CLASS = 'van-theme-dark'

/** 把主题应用到文档：纯 DOM 操作，不含加载与持久化。 */
export function applyThemeToDocument(doc: Document, theme: ThemeDefinition): void {
  const root = doc.documentElement
  root.classList.add(SWITCHING_CLASS)
  root.setAttribute(THEME_ATTRIBUTE, theme.id)
  root.style.colorScheme = theme.colorScheme
  root.classList.toggle(VANT_DARK_CLASS, theme.colorScheme === 'dark')

  let meta = doc.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
  if (!meta) {
    meta = doc.createElement('meta')
    meta.name = 'theme-color'
    doc.head.appendChild(meta)
  }
  meta.content = theme.preview.canvas

  // 强制回流让抑制类生效，下一帧再移除
  void root.offsetWidth
  const view = doc.defaultView
  const schedule =
    view?.requestAnimationFrame?.bind(view) ??
    ((callback: FrameRequestCallback) => setTimeout(() => callback(0), 16))
  schedule(() => root.classList.remove(SWITCHING_CLASS))
}
