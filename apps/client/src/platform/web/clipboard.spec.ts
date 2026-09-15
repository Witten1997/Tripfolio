import { afterEach, describe, expect, it, vi } from 'vitest'

import { webClipboard } from './clipboard'

/** 去掉 navigator.clipboard，模拟非安全上下文（纯 http 自托管）里的浏览器。 */
function withoutAsyncClipboard() {
  const original = Object.getOwnPropertyDescriptor(Navigator.prototype, 'clipboard')
  Object.defineProperty(Navigator.prototype, 'clipboard', { configurable: true, value: undefined })
  return () => {
    if (original) Object.defineProperty(Navigator.prototype, 'clipboard', original)
    else delete (Navigator.prototype as { clipboard?: unknown }).clipboard
  }
}

describe('webClipboard', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('安全上下文里用 navigator.clipboard', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(Navigator.prototype, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    await webClipboard.writeText('https://trip.example.com/s/token')
    expect(writeText).toHaveBeenCalledWith('https://trip.example.com/s/token')
  })

  // 回归：非安全上下文没有 navigator.clipboard，直接调用会让「复制分享链接」抛 TypeError。
  it('非安全上下文里退回 execCommand，并把文本放进临时 textarea', async () => {
    const restore = withoutAsyncClipboard()
    const execCommand = vi.fn((command: string) => {
      const area = document.querySelector('textarea')
      expect(command).toBe('copy')
      expect(area?.value).toBe('https://trip.example.com/s/token')
      return true
    })
    Object.defineProperty(document, 'execCommand', { configurable: true, value: execCommand })
    try {
      await expect(
        webClipboard.writeText('https://trip.example.com/s/token'),
      ).resolves.toBeUndefined()
      expect(execCommand).toHaveBeenCalledTimes(1)
      expect(document.querySelector('textarea')).toBeNull()
    } finally {
      restore()
    }
  })

  it('execCommand 也失败时抛出，由调用方提示手动复制', async () => {
    const restore = withoutAsyncClipboard()
    Object.defineProperty(document, 'execCommand', { configurable: true, value: () => false })
    try {
      await expect(webClipboard.writeText('x')).rejects.toThrow('剪贴板')
    } finally {
      restore()
    }
  })
})
