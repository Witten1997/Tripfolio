// @vitest-environment node
import { EventEmitter } from 'node:events'

import type { ProxyOptions } from 'vite'
import { describe, expect, it } from 'vitest'

import { amapProxies } from './amap-proxy'

describe('高德开发代理', () => {
  it('三类请求只发往固定官方主机，并覆盖客户端伪造的安全密钥', () => {
    const proxies = amapProxies('server-secret')
    expect(Object.values(proxies).map((proxy) => proxy.target)).toEqual([
      'https://webapi.amap.com',
      'https://fmap01.amap.com',
      'https://restapi.amap.com',
    ])
    for (const [prefix, proxy] of Object.entries(proxies)) {
      const path = proxy.rewrite!(`${prefix}/test?key=public-js-key&jscode=untrusted`)
      const url = new URL(path, String(proxy.target))
      expect(url.pathname).not.toContain('_AMapService')
      expect(url.searchParams.get('key')).toBe('public-js-key')
      expect(url.searchParams.getAll('jscode')).toEqual(['server-secret'])
    }
  })

  it('网络故障进入 Vite 日志前清除请求查询和错误中的安全密钥', () => {
    const options = amapProxies('server-secret')['/_AMapService']!
    const server = new EventEmitter()
    options.configure!(server as Parameters<NonNullable<ProxyOptions['configure']>>[0], options)
    const error = new Error('网络中断 server-secret')
    const request = { url: '/v3/geocode/regeo?key=public-js-key&jscode=server-secret' }
    server.emit('error', error, request)
    expect(request.url).toBe('/v3/geocode/regeo')
    expect(error.message).not.toContain('server-secret')
    expect(error.stack).not.toContain('server-secret')
  })
})
