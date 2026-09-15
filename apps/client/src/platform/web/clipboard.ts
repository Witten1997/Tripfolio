import type { ClipboardWriter } from '../types'

/** 网页与 WebView 共用；需要系统级剪贴板能力时再换 @capacitor/clipboard 实现。 */
export const webClipboard: ClipboardWriter = {
  async writeText(text) {
    await navigator.clipboard.writeText(text)
  },
}
