import type { ClipboardWriter } from '../types'

/** 把文本放进临时 textarea 并用 execCommand 复制；非安全上下文里没有 navigator.clipboard 时的退路。 */
function copyWithExecCommand(text: string): boolean {
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '-1000px'
  area.style.opacity = '0'
  document.body.append(area)
  const selection = document.getSelection()
  const previous = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null
  area.select()
  try {
    return document.execCommand('copy')
  } finally {
    area.remove()
    if (selection && previous) {
      selection.removeAllRanges()
      selection.addRange(previous)
    }
  }
}

/**
 * 网页与 WebView 共用；需要系统级剪贴板能力时再换 @capacitor/clipboard 实现。
 *
 * `navigator.clipboard` 只在**安全上下文**（HTTPS 或 localhost）里存在，纯 http 自托管部署里是
 * `undefined`，直接调用会让「复制分享链接」抛 `TypeError`。因此缺它时退回 `document.execCommand`：
 * 它虽已废弃但所有浏览器仍在实现，且不要求安全上下文。两者都不可用时抛出，由调用方提示手动复制。
 */
export const webClipboard: ClipboardWriter = {
  async writeText(text) {
    if (typeof navigator.clipboard?.writeText === 'function') {
      await navigator.clipboard.writeText(text)
      return
    }
    if (!copyWithExecCommand(text)) throw new Error('浏览器未提供剪贴板能力')
  },
}
