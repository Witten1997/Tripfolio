import {
  CapacitorSQLite,
  SQLiteConnection,
  type SQLiteDBConnection,
} from '@capacitor-community/sqlite'

import {
  SqlTransactionError,
  type SqlConnection,
  type SqlConnectionOpener,
  type SqlExecutor,
  type SqlParam,
  type SqlRow,
  type SqlRunResult,
} from '../localdb/executor'

/**
 * @capacitor-community/sqlite 实现的本地数据库（安卓；鸿蒙化版本待验证）。
 * 要点（依据插件 8.1.1 的安卓源码）：
 * - 插件打开数据库时已启用外键约束；
 * - 我们自己管理事务：begin/commit/rollbackTransaction；事务内的 execute/run 必须传 transaction=false，
 *   否则插件会再开一个事务并报 "Already in transaction"；
 * - createConnection 对已存在的连接名会抛错，因此先 isConnection 再 retrieveConnection；
 * - deleteDatabase/isDBExists 需要先有连接，文件是否存在用 isDatabase 判断。
 */

const sqlite = new SQLiteConnection(CapacitorSQLite)
const NO_ENCRYPTION = 'no-encryption'
const PLUGIN_DB_VERSION = 1

class CapacitorSqlConnection implements SqlConnection {
  private inTransaction = false

  constructor(
    readonly name: string,
    private readonly db: SQLiteDBConnection,
  ) {}

  async exec(sql: string): Promise<void> {
    await this.db.execute(sql, false)
  }

  async run(sql: string, params: SqlParam[] = []): Promise<SqlRunResult> {
    const result = await this.db.run(sql, params, false)
    const lastId = result.changes?.lastId
    return {
      changes: result.changes?.changes ?? 0,
      lastInsertRowId: lastId == null || lastId < 0 ? null : lastId,
    }
  }

  async query<T extends SqlRow = SqlRow>(sql: string, params: SqlParam[] = []): Promise<T[]> {
    const result = await this.db.query(sql, params)
    return (result.values ?? []) as T[]
  }

  async transaction<T>(fn: (tx: SqlExecutor) => Promise<T>): Promise<T> {
    if (this.inTransaction) throw new SqlTransactionError('不支持嵌套事务')
    this.inTransaction = true
    await this.db.beginTransaction()
    try {
      const result = await fn(this)
      await this.db.commitTransaction()
      return result
    } catch (error) {
      await this.db.rollbackTransaction().catch(() => undefined)
      throw error
    } finally {
      this.inTransaction = false
    }
  }

  async close(): Promise<void> {
    // closeConnection 会先关闭已打开的数据库，再从原生与 JS 两侧的连接字典移除
    await sqlite.closeConnection(this.name, false)
  }
}

async function acquire(name: string): Promise<SQLiteDBConnection> {
  // 页面刷新或热重载后 JS 侧字典可能丢失，先与原生侧对齐
  await sqlite.checkConnectionsConsistency().catch(() => undefined)
  const existing = (await sqlite.isConnection(name, false)).result === true
  const db = existing
    ? await sqlite.retrieveConnection(name, false)
    : await sqlite.createConnection(name, false, NO_ENCRYPTION, PLUGIN_DB_VERSION, false)
  await db.open()
  return db
}

export const capacitorLocalDatabase: SqlConnectionOpener = {
  async open(name) {
    const db = await acquire(name)
    return new CapacitorSqlConnection(name, db)
  },

  async exists(name) {
    const result = await CapacitorSQLite.isDatabase({ database: name })
    return result.result === true
  },

  async delete(name) {
    if (!(await this.exists(name))) return
    const db = await acquire(name)
    try {
      await db.delete()
    } finally {
      await sqlite.closeConnection(name, false).catch(() => undefined)
    }
  },
}
