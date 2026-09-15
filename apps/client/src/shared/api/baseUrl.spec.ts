import { describe, expect, it } from 'vitest'

import { resolveApiBaseUrl } from './baseUrl'

describe('resolveApiBaseUrl', () => {
  it('空串与纯空白一律视为未配置，回退到同源 /api/v1', () => {
    // Docker 构建默认传空串：这里是那次事故的回归点。
    expect(resolveApiBaseUrl(undefined, '')).toBe('/api/v1')
    expect(resolveApiBaseUrl('', '')).toBe('/api/v1')
    expect(resolveApiBaseUrl('   ', '  ')).toBe('/api/v1')
    expect(resolveApiBaseUrl()).toBe('/api/v1')
  })

  it('显式参数优先于构建期变量，二者都会去掉首尾空白', () => {
    expect(resolveApiBaseUrl('http://api.test/v1', 'https://api.example.com/api/v1')).toBe(
      'http://api.test/v1',
    )
    expect(resolveApiBaseUrl(undefined, ' https://api.example.com/api/v1 ')).toBe(
      'https://api.example.com/api/v1',
    )
    expect(resolveApiBaseUrl('', 'https://api.example.com/api/v1')).toBe(
      'https://api.example.com/api/v1',
    )
  })

  it('源码里不允许对 VITE_API_BASE_URL 使用 ?? 兜底', async () => {
    // 样式契约同款守卫：新增客户端时若写回 `?? import.meta.env.VITE_API_BASE_URL`，
    // 空串会再次变成有效值，容器里整站接口失效。
    const sources = import.meta.glob('/src/**/*.{ts,vue}', {
      query: '?raw',
      import: 'default',
      eager: true,
    })
    const offenders = Object.entries(sources)
      .filter(([path]) => !path.endsWith('.spec.ts'))
      .filter(([, text]) => /\?\?\s*import\.meta\.env\.VITE_API_BASE_URL/.test(text as string))
      .map(([path]) => path)
    expect(offenders).toEqual([])
  })
})
