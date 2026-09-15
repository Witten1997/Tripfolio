/** 访客分享页前缀；启动时据此决定不挂载任何壳。 */
export const SHARE_PATH_PREFIX = '/s/'

/** 判断浏览器路径是否为分享页；base 为 Vite 的 BASE_URL（默认 '/'）。 */
export function isSharePath(pathname: string, base: string = import.meta.env.BASE_URL): boolean {
  const root = base.endsWith('/') ? base.slice(0, -1) : base
  const prefix = root + SHARE_PATH_PREFIX
  return pathname.startsWith(prefix) && pathname.length > prefix.length
}
