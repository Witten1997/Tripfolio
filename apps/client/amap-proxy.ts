import type { ProxyOptions } from 'vite'

/** 仅在 Vite 服务端运行，jscode 不进入 import.meta.env 或浏览器包。 */
export function amapProxies(jscode: string): Record<string, ProxyOptions> {
  const proxy = (target: string): ProxyOptions => ({
    target,
    changeOrigin: true,
    configure(server) {
      // Vite 默认错误日志会打印改写后的 req.url；在它的监听器运行前移除查询凭证。
      server.on('error', (error, request) => {
        if (request.url) request.url = request.url.split('?')[0]
        if (jscode) {
          error.message = error.message.replaceAll(jscode, '[redacted]')
          if (error.stack) error.stack = error.stack.replaceAll(jscode, '[redacted]')
        }
      })
    },
    rewrite(path) {
      const url = new URL(path, target)
      url.pathname = url.pathname.replace(/^\/_AMapService/, '')
      url.searchParams.set('jscode', jscode)
      return url.pathname + url.search
    },
  })
  return {
    '/_AMapService/v4/map/styles': proxy('https://webapi.amap.com'),
    '/_AMapService/v3/vectormap': proxy('https://fmap01.amap.com'),
    '/_AMapService': proxy('https://restapi.amap.com'),
  }
}
