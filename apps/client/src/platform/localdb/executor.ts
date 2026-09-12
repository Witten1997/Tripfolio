/**
 * 本地数据库的最小 SQL 执行接口。业务层（迁移、仓储、自检）只依赖这里的类型；
 * 具体实现有两个：Capacitor 插件（安卓，后续鸿蒙）和 Node 内置 sqlite（仅单元测试）。
 */

export type SqlParam = string | number | bigint | null | Uint8Array

export type SqlRow = Record<string, unknown>

export interface SqlRunResult {
  /** 本条语句影响的行数 */
  changes: number
  /** INSERT 后的 rowid；不适用时为 null */
  lastInsertRowId: number | null
}

export interface SqlExecutor {
  /** 执行一条或多条不带参数的语句（DDL、PRAGMA）。 */
  exec(sql: string): Promise<void>
  /** 执行一条带参数的写语句。 */
  run(sql: string, params?: SqlParam[]): Promise<SqlRunResult>
  /** 执行一条查询并返回全部行。 */
  query<T extends SqlRow = SqlRow>(sql: string, params?: SqlParam[]): Promise<T[]>
}

export interface SqlConnection extends SqlExecutor {
  /** 数据库逻辑名（不含平台后缀）。 */
  readonly name: string
  /**
   * 在一个事务中执行 fn。fn 正常返回则提交，抛出则回滚并原样抛出。
   * 不支持嵌套：在事务内再次调用会抛错。
   */
  transaction<T>(fn: (tx: SqlExecutor) => Promise<T>): Promise<T>
  close(): Promise<void>
}

/** 按名称打开、检查、删除数据库；每个账号一个数据库文件。 */
export interface SqlConnectionOpener {
  open(name: string): Promise<SqlConnection>
  exists(name: string): Promise<boolean>
  /** 删除数据库文件；已打开的连接先关闭。不存在时静默返回。 */
  delete(name: string): Promise<void>
}

export class SqlTransactionError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'SqlTransactionError'
  }
}
