import type { SqlConnection } from './executor'
import { SCHEMA_V1 } from './schema'

export interface LocalMigration {
  /** 从 1 开始连续递增 */
  version: number
  name: string
  /** 依次执行的语句；整组在一个事务内应用 */
  statements: readonly string[]
}

export const LOCAL_MIGRATIONS: readonly LocalMigration[] = [
  { version: 1, name: 'initial', statements: SCHEMA_V1 },
]

export const LOCAL_SCHEMA_VERSION = LOCAL_MIGRATIONS[LOCAL_MIGRATIONS.length - 1]!.version

export interface MigrationResult {
  /** 本次应用的版本号 */
  applied: number[]
  /** 迁移后的版本 */
  version: number
}

export class LocalMigrationError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'LocalMigrationError'
  }
}

/** 校验迁移列表从 1 开始连续递增，避免漏号导致的版本错位。 */
export function assertMigrationsContiguous(migrations: readonly LocalMigration[]): void {
  migrations.forEach((m, index) => {
    if (m.version !== index + 1) {
      throw new LocalMigrationError(
        `迁移版本必须从 1 连续递增，第 ${index + 1} 项却是 ${m.version}`,
      )
    }
    if (m.statements.length === 0) {
      throw new LocalMigrationError(`迁移 ${m.version} 没有语句`)
    }
  })
}

/**
 * 把连接迁移到最新版本。每个版本在自己的事务内应用并登记，中途失败只回滚该版本；
 * 数据库版本高于程序已知版本时拒绝打开，防止旧程序改写新结构。
 */
export async function migrateLocalDatabase(
  conn: SqlConnection,
  migrations: readonly LocalMigration[] = LOCAL_MIGRATIONS,
): Promise<MigrationResult> {
  assertMigrationsContiguous(migrations)
  await conn.exec(`CREATE TABLE IF NOT EXISTS local_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at TEXT NOT NULL
  )`)

  const rows = await conn.query<{ version: number }>(
    'SELECT version FROM local_migrations ORDER BY version',
  )
  const appliedVersions = rows.map((r) => Number(r.version))
  const current = appliedVersions.length === 0 ? 0 : appliedVersions[appliedVersions.length - 1]!
  if (current > migrations.length) {
    throw new LocalMigrationError(
      `本地数据库版本 ${current} 高于程序支持的 ${migrations.length}，请升级应用`,
    )
  }
  appliedVersions.forEach((v, index) => {
    if (v !== index + 1) {
      throw new LocalMigrationError(`本地迁移记录不连续：${appliedVersions.join(',')}`)
    }
  })

  const applied: number[] = []
  for (const migration of migrations.slice(current)) {
    await conn.transaction(async (tx) => {
      for (const statement of migration.statements) {
        await tx.exec(statement)
      }
      await tx.run('INSERT INTO local_migrations (version, name, applied_at) VALUES (?, ?, ?)', [
        migration.version,
        migration.name,
        new Date().toISOString(),
      ])
    })
    applied.push(migration.version)
  }
  return { applied, version: migrations.length }
}

/** 读取已应用的最高版本；尚未初始化时为 0。 */
export async function currentLocalSchemaVersion(conn: SqlConnection): Promise<number> {
  const tables = await conn.query<{ n: number }>(
    "SELECT count(*) AS n FROM sqlite_master WHERE type = 'table' AND name = 'local_migrations'",
  )
  if (Number(tables[0]?.n ?? 0) === 0) return 0
  const rows = await conn.query<{ v: number | null }>(
    'SELECT max(version) AS v FROM local_migrations',
  )
  return Number(rows[0]?.v ?? 0)
}
