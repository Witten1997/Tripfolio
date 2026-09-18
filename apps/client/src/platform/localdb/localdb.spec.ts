// @vitest-environment node
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { LocalDatabase } from './database'
import type { SqlConnectionOpener } from './executor'
import {
  LOCAL_MIGRATIONS,
  LOCAL_SCHEMA_VERSION,
  LocalMigrationError,
  assertMigrationsContiguous,
  currentLocalSchemaVersion,
  migrateLocalDatabase,
} from './migrations'
import { localDatabaseName } from './names'
import { createNodeSqlOpener } from './node/nodeSqlite'
import { bumpPersistenceCounter, runLocalDbSelfCheck } from './selfcheck'

let dir: string
let opener: SqlConnectionOpener

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), 'tripfolio-localdb-'))
  opener = createNodeSqlOpener(dir)
})

afterEach(() => {
  rmSync(dir, { recursive: true, force: true })
})

describe('本地数据库自检场景', () => {
  it('全部步骤通过', async () => {
    const report = await runLocalDbSelfCheck(opener)
    const failed = report.steps.filter((s) => !s.ok).map((s) => `${s.name}: ${s.detail}`)
    expect(failed, failed.join('\n')).toEqual([])
    expect(report.ok).toBe(true)
    expect(report.steps).toHaveLength(7)
  })

  it('自检结束后不留下账号 A、B 的数据库', async () => {
    await runLocalDbSelfCheck(opener)
    expect(await opener.exists(localDatabaseName('selfcheck-a'))).toBe(false)
    expect(await opener.exists(localDatabaseName('selfcheck-b'))).toBe(false)
  })

  it('重启保留计数跨连接递增', async () => {
    expect(await bumpPersistenceCounter(opener)).toBe(1)
    expect(await bumpPersistenceCounter(opener)).toBe(2)
  })
})

describe('迁移', () => {
  it('迁移列表从 1 连续递增', () => {
    expect(() => assertMigrationsContiguous(LOCAL_MIGRATIONS)).not.toThrow()
    expect(() =>
      assertMigrationsContiguous([{ version: 2, name: 'x', statements: ['SELECT 1'] }]),
    ).toThrow(LocalMigrationError)
  })

  it('重复迁移是空操作', async () => {
    const conn = await opener.open('m1')
    expect(await currentLocalSchemaVersion(conn)).toBe(0)
    const first = await migrateLocalDatabase(conn)
    expect(first.applied).toEqual(LOCAL_MIGRATIONS.map((m) => m.version))
    const second = await migrateLocalDatabase(conn)
    expect(second.applied).toEqual([])
    expect(second.version).toBe(LOCAL_SCHEMA_VERSION)
    await conn.close()
  })

  it('已有数据库能升级到新增的版本', async () => {
    const conn = await opener.open('m2')
    await migrateLocalDatabase(conn)
    const next = {
      version: LOCAL_SCHEMA_VERSION + 1,
      name: 'add-notes-table',
      statements: ['CREATE TABLE notes (id TEXT PRIMARY KEY, body TEXT NOT NULL)'],
    }
    const result = await migrateLocalDatabase(conn, [...LOCAL_MIGRATIONS, next])
    expect(result.applied).toEqual([next.version])
    const tables = await conn.query<{ name: string }>(
      "SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'notes'",
    )
    expect(tables).toHaveLength(1)
    await conn.close()
  })

  it('迁移中途失败整体回滚且不登记版本', async () => {
    const conn = await opener.open('m3')
    const broken = {
      version: 1,
      name: 'broken',
      statements: [
        'CREATE TABLE ok_table (id TEXT PRIMARY KEY)',
        'CREATE TABLE ok_table (id TEXT)',
      ],
    }
    await expect(migrateLocalDatabase(conn, [broken])).rejects.toThrow()
    expect(await currentLocalSchemaVersion(conn)).toBe(0)
    const tables = await conn.query<{ name: string }>(
      "SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'ok_table'",
    )
    expect(tables).toHaveLength(0)
    await conn.close()
  })

  it('数据库版本高于程序时拒绝打开', async () => {
    const conn = await opener.open('m4')
    const future = {
      version: LOCAL_SCHEMA_VERSION + 1,
      name: 'future',
      statements: ['CREATE TABLE future (id TEXT)'],
    }
    await migrateLocalDatabase(conn, [...LOCAL_MIGRATIONS, future])
    await expect(migrateLocalDatabase(conn, LOCAL_MIGRATIONS)).rejects.toThrow(LocalMigrationError)
    await conn.close()
  })
})

describe('LocalDatabase', () => {
  it('按账号命名并校验账号标识', async () => {
    expect(localDatabaseName('019ed632-51c0-7000-8000-000000000001')).toBe(
      'tripfolio-019ed632-51c0-7000-8000-000000000001',
    )
    expect(() => localDatabaseName('../etc')).toThrow()
    const db = await LocalDatabase.open(opener, 'acc-1')
    expect(db.name).toBe('tripfolio-acc-1')
    await db.close()
  })

  it('旧版本的同步数据不会覆盖新版本', async () => {
    const db = await LocalDatabase.open(opener, 'acc-2')
    const trip = {
      id: 't1',
      name: '新名称',
      start_date: '2026-10-01',
      end_date: '2026-10-02',
      currency_code: 'CNY',
      timezone: 'Asia/Shanghai',
      budget_amount: null,
      version: 3,
      updated_at: '2026-09-12T00:00:00Z',
      deleted_at: null,
    }
    await db.upsertTrip(trip)
    await db.upsertTrip({ ...trip, name: '旧名称', version: 2 })
    const rows = await db.transaction((tx) =>
      tx.query<{ name: string; version: number }>('SELECT name, version FROM trips WHERE id = ?', [
        't1',
      ]),
    )
    expect(rows[0]).toMatchObject({ name: '新名称', version: 3 })
    await db.close()
  })
})
