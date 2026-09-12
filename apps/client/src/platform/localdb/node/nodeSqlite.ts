/**
 * Node 内置 sqlite（node:sqlite）实现的执行器，只用于单元测试：
 * 让迁移、仓储与自检场景在本机验证，与真机上的 Capacitor 实现共用同一套业务代码。
 * 不会被打进前端产物：只有 *.spec.ts 导入它。
 */
import { existsSync, unlinkSync } from 'node:fs'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'

import {
  SqlTransactionError,
  type SqlConnection,
  type SqlConnectionOpener,
  type SqlExecutor,
  type SqlParam,
  type SqlRow,
  type SqlRunResult,
} from '../executor'

class NodeSqlConnection implements SqlConnection {
  private inTransaction = false

  constructor(
    readonly name: string,
    private readonly db: DatabaseSync,
  ) {}

  async exec(sql: string): Promise<void> {
    this.db.exec(sql)
  }

  async run(sql: string, params: SqlParam[] = []): Promise<SqlRunResult> {
    const result = this.db.prepare(sql).run(...params)
    return {
      changes: Number(result.changes),
      lastInsertRowId: result.lastInsertRowid == null ? null : Number(result.lastInsertRowid),
    }
  }

  async query<T extends SqlRow = SqlRow>(sql: string, params: SqlParam[] = []): Promise<T[]> {
    return this.db.prepare(sql).all(...params) as T[]
  }

  async transaction<T>(fn: (tx: SqlExecutor) => Promise<T>): Promise<T> {
    if (this.inTransaction) throw new SqlTransactionError('不支持嵌套事务')
    this.inTransaction = true
    this.db.exec('BEGIN IMMEDIATE')
    try {
      const result = await fn(this)
      this.db.exec('COMMIT')
      return result
    } catch (error) {
      this.db.exec('ROLLBACK')
      throw error
    } finally {
      this.inTransaction = false
    }
  }

  async close(): Promise<void> {
    this.db.close()
  }
}

/** 在 dir 下按名称创建文件数据库；exists/delete 直接操作文件。 */
export function createNodeSqlOpener(dir: string): SqlConnectionOpener {
  const fileFor = (name: string) => join(dir, `${name}.sqlite`)
  return {
    async open(name) {
      const db = new DatabaseSync(fileFor(name), { enableForeignKeyConstraints: true })
      return new NodeSqlConnection(name, db)
    },
    async exists(name) {
      return existsSync(fileFor(name))
    },
    async delete(name) {
      for (const suffix of ['', '-journal', '-wal', '-shm']) {
        const path = fileFor(name) + suffix
        if (existsSync(path)) unlinkSync(path)
      }
    },
  }
}
