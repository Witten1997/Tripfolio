/**
 * 生成 UUID v4。
 *
 * 浏览器只在**安全上下文**（HTTPS 或 localhost）里提供 `crypto.randomUUID`，而自托管的纯 http
 * 部署（内网 IP、NAS 端口映射，如 `http://trip.example.com:61118`）不是安全上下文，该函数是
 * `undefined`。直接调用会抛 `TypeError`，且异常发生在 fetch 之前——注册、登录、创建旅行等写操作
 * 请求根本没发出去，页面只显示「网络错误，请稍后再试」，服务端日志里也找不到这条请求。
 *
 * 因此这里在它缺失时退回 `crypto.getRandomValues`：同样不受安全上下文限制，同样是 CSPRNG
 * （不使用 `Math.random`），按 RFC 4122 自行拼出 v4。
 */
export function randomId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40 // 版本 4
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80 // 变体 10xx
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}
