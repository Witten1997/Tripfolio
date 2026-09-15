/**
 * 解析接口前缀：显式参数 → 构建期变量 → 同源 `/api/v1`。
 *
 * 必须用 `||` 而不是 `??`：Docker 构建默认把 `VITE_API_BASE_URL` 传成**空串**（`ARG ...=` + `${VAR:-}`），
 * `??` 会接受空串，baseUrl 变成空值，请求落到 `/metadata` 这类根路径，
 * 被单二进制的 SPA 回退返回 index.html——接口全部失效且只在容器里复现。
 */
export function resolveApiBaseUrl(explicit?: string, fromEnv?: string): string {
  return explicit?.trim() || fromEnv?.trim() || '/api/v1'
}
