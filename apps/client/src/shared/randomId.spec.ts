import { afterEach, describe, expect, it, vi } from 'vitest'

import { randomId } from './randomId'

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

/** 去掉 crypto.randomUUID，模拟非安全上下文（纯 http 自托管）里的浏览器。 */
function withoutRandomUUID() {
  // jsdom 里 randomUUID 挂在 crypto 实例自己身上，所以直接遮蔽实例属性而不是原型。
  const own = Object.getOwnPropertyDescriptor(crypto, 'randomUUID')
  const proto = own ? null : Object.getOwnPropertyDescriptor(Crypto.prototype, 'randomUUID')
  Object.defineProperty(crypto, 'randomUUID', { configurable: true, value: undefined })
  return () => {
    delete (crypto as { randomUUID?: unknown }).randomUUID
    if (own) Object.defineProperty(crypto, 'randomUUID', own)
    else if (proto) Object.defineProperty(Crypto.prototype, 'randomUUID', proto)
  }
}

describe('randomId', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('有 crypto.randomUUID 时直接用它', () => {
    const spy = vi.spyOn(crypto, 'randomUUID')
    const id = randomId()
    expect(spy).toHaveBeenCalledTimes(1)
    expect(id).toMatch(UUID_V4)
  })

  // 回归：http + 非 localhost 时 crypto.randomUUID 不存在，直接调用会抛 TypeError，
  // 注册、登录、创建旅行等写操作在发请求前就失败，页面只显示「网络错误」。
  it('非安全上下文里退回 getRandomValues，仍然是合法的 v4 UUID', () => {
    const restore = withoutRandomUUID()
    try {
      expect(() => crypto.randomUUID()).toThrow(TypeError)
      const ids = new Set([randomId(), randomId(), randomId()])
      expect(ids.size).toBe(3)
      for (const id of ids) expect(id).toMatch(UUID_V4)
    } finally {
      restore()
    }
  })

  it('两种来源都不存在时抛出，而不是悄悄退化成弱随机', () => {
    const restore = withoutRandomUUID()
    const getRandomValues = vi.spyOn(crypto, 'getRandomValues').mockImplementation(() => {
      throw new Error('no getRandomValues')
    })
    try {
      expect(() => randomId()).toThrow('no getRandomValues')
    } finally {
      getRandomValues.mockRestore()
      restore()
    }
  })
})
