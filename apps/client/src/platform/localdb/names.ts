const ACCOUNT_ID_PATTERN = /^[A-Za-z0-9-]{1,64}$/

/**
 * 每个账号一个本地数据库，名称 `tripfolio-<账号ID>`。
 * 安卓上插件会把文件存为 `tripfolio-<账号ID>SQLite.db`；退出登录不删除文件，
 * 以保留尚未同步的内容；“清理本地缓存”与账号注销才删除。
 */
export function localDatabaseName(accountId: string): string {
  if (!ACCOUNT_ID_PATTERN.test(accountId)) {
    throw new Error(`非法的账号标识：${accountId}`)
  }
  return `tripfolio-${accountId}`
}
