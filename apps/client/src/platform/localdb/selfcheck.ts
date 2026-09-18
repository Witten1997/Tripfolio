import { LocalDatabase, type LocalLedgerEntry } from './database'
import type { SqlConnectionOpener } from './executor'
import { LOCAL_SCHEMA_VERSION } from './migrations'
import { localDatabaseName } from './names'

export interface SelfCheckStep {
  name: string
  ok: boolean
  detail: string
}

export interface SelfCheckReport {
  ok: boolean
  startedAt: string
  durationMs: number
  steps: SelfCheckStep[]
}

export interface SelfCheckIds {
  accountA: string
  accountB: string
}

const DEFAULT_IDS: SelfCheckIds = { accountA: 'selfcheck-a', accountB: 'selfcheck-b' }

const TRIP_ID = '019ed632-51c0-7000-8000-0000000000a1'
const CATEGORY_ID = '019ed632-51c0-7000-8000-0000000000c1'
const ENTRY_ID = '019ed632-51c0-7000-8000-0000000000e1'
const ROLLBACK_ENTRY_ID = '019ed632-51c0-7000-8000-0000000000e2'
const OPERATION_ID = '019ed632-51c0-7000-8000-0000000000f1'

/**
 * SQLite 数据链路自检（技术选型第 10 节验证项）：
 * 迁移、同事务写入业务行与待同步操作、失败回滚、外键约束、关闭重开后数据保留、按账号分库隔离、删除。
 * 同一份场景既在单元测试里跑（Node 内置 sqlite），也在真机的自检页面跑（Capacitor 插件）。
 */
export async function runLocalDbSelfCheck(
  opener: SqlConnectionOpener,
  ids: SelfCheckIds = DEFAULT_IDS,
): Promise<SelfCheckReport> {
  const startedAt = new Date()
  const steps: SelfCheckStep[] = []
  let dbA: LocalDatabase | null = null
  let dbB: LocalDatabase | null = null

  const step = async (name: string, fn: () => Promise<string>): Promise<boolean> => {
    try {
      steps.push({ name, ok: true, detail: await fn() })
      return true
    } catch (error) {
      steps.push({
        name,
        ok: false,
        detail: error instanceof Error ? error.message : String(error),
      })
      return false
    }
  }
  const expect = (condition: boolean, message: string): void => {
    if (!condition) throw new Error(message)
  }

  // 先清掉上次未清理的残留，保证可重复运行
  await opener.delete(localDatabaseName(ids.accountA)).catch(() => undefined)
  await opener.delete(localDatabaseName(ids.accountB)).catch(() => undefined)

  try {
    await step('打开账号 A 的数据库并迁移到最新结构', async () => {
      dbA = await LocalDatabase.open(opener, ids.accountA)
      const version = await dbA.schemaVersion()
      expect(version === LOCAL_SCHEMA_VERSION, `结构版本 ${version}，期望 ${LOCAL_SCHEMA_VERSION}`)
      return `${dbA.name}，结构版本 ${version}`
    })

    await step('同一事务写入账目与待同步操作', async () => {
      const db = dbA!
      await db.upsertTrip({
        id: TRIP_ID,
        name: '自检旅行',
        start_date: '2026-10-01',
        end_date: '2026-10-05',
        currency_code: 'CNY',
        timezone: 'Asia/Shanghai',
        budget_amount: '3000.00',
        version: 1,
        updated_at: '2026-09-12T00:00:00Z',
        deleted_at: null,
      })
      await db.upsertExpenseCategory({
        id: CATEGORY_ID,
        name: '美食',
        icon: 'food',
        sort_order: 2,
        version: 1,
        deleted_at: null,
      })
      await db.createLedgerEntryOffline(sampleEntry(ENTRY_ID), OPERATION_ID)
      const entries = await db.listLedgerEntries(TRIP_ID)
      const pending = await db.listPendingOperations()
      expect(entries.length === 1 && entries[0]!.sync_pending, '账目应存在且标记为待同步')
      expect(
        pending.length === 1 && pending[0]!.operation_id === OPERATION_ID,
        '待同步操作应有 1 条',
      )
      expect(
        (pending[0]!.payload as { amount?: string }).amount === '128.50',
        'payload 应包含规范化金额',
      )
      return `账目 ${entries.length} 条，待同步 ${pending.length} 条`
    })

    await step('事务中途失败时整体回滚', async () => {
      const db = dbA!
      const before = await db.count('ledger_entries')
      let thrown = false
      try {
        await db.transaction(async (tx) => {
          const e = sampleEntry(ROLLBACK_ENTRY_ID)
          await tx.run(
            `INSERT INTO ledger_entries (id, trip_id, kind, amount, category_id, occurred_on, notes, attachment_asset_ids, version, created_at, updated_at)
             VALUES (?, ?, ?, ?, ?, ?, ?, '[]', 1, ?, ?)`,
            [
              e.id,
              e.trip_id,
              e.kind,
              e.amount,
              e.category_id,
              e.occurred_on,
              e.notes,
              e.created_at,
              e.updated_at,
            ],
          )
          throw new Error('模拟中途失败')
        })
      } catch (error) {
        thrown = error instanceof Error && error.message === '模拟中途失败'
      }
      const after = await db.count('ledger_entries')
      expect(thrown, '事务应把原始错误抛出')
      expect(after === before, `回滚后账目数应为 ${before}，实际 ${after}`)
      return `账目数保持 ${after}`
    })

    await step('外键约束生效：引用不存在的旅行被拒绝', async () => {
      const db = dbA!
      let rejected = false
      try {
        await db.createLedgerEntryOffline(
          {
            ...sampleEntry('019ed632-51c0-7000-8000-0000000000e3'),
            trip_id: '019ed632-51c0-7000-8000-000000000bad',
          },
          '019ed632-51c0-7000-8000-0000000000f3',
        )
      } catch {
        rejected = true
      }
      expect(rejected, '外键约束没有生效')
      const pending = await db.count('pending_operations')
      expect(pending === 1, `失败的写入不应留下待同步操作，实际 ${pending} 条`)
      return '插入被拒绝，且未留下半条记录'
    })

    await step('关闭后重新打开：数据保留，迁移不重复', async () => {
      await dbA!.close()
      dbA = await LocalDatabase.open(opener, ids.accountA)
      const entries = await dbA.count('ledger_entries')
      const pending = await dbA.count('pending_operations')
      const version = await dbA.schemaVersion()
      expect(entries === 1 && pending === 1, `重开后账目 ${entries} 条、待同步 ${pending} 条`)
      expect(version === LOCAL_SCHEMA_VERSION, `重开后结构版本 ${version}`)
      return `账目 ${entries} 条，待同步 ${pending} 条，结构版本 ${version}`
    })

    await step('账号 B 的数据库独立且为空', async () => {
      dbB = await LocalDatabase.open(opener, ids.accountB)
      expect(dbB.name !== dbA!.name, '两个账号的数据库名称不能相同')
      const entriesB = await dbB.count('ledger_entries')
      const entriesA = await dbA!.count('ledger_entries')
      expect(entriesB === 0, `账号 B 不应看到账目，实际 ${entriesB} 条`)
      expect(entriesA === 1, `账号 A 的账目应仍为 1 条，实际 ${entriesA}`)
      return `${dbB.name} 为空，${dbA!.name} 保持 1 条`
    })

    await step('删除账号 B 的数据库', async () => {
      const name = dbB!.name
      await dbB!.close()
      dbB = null
      await opener.delete(name)
      const exists = await opener.exists(name)
      expect(!exists, '删除后数据库仍然存在')
      return `${name} 已删除`
    })
  } finally {
    // 两个变量在闭包内赋值，TS 的流程分析看不到，这里显式标注类型
    const openA = dbA as LocalDatabase | null
    const openB = dbB as LocalDatabase | null
    await openB?.close().catch(() => undefined)
    await openA?.close().catch(() => undefined)
    await opener.delete(localDatabaseName(ids.accountA)).catch(() => undefined)
    await opener.delete(localDatabaseName(ids.accountB)).catch(() => undefined)
  }

  return {
    ok: steps.every((s) => s.ok),
    startedAt: startedAt.toISOString(),
    durationMs: Date.now() - startedAt.getTime(),
    steps,
  }
}

/**
 * 重启保留验证：每次调用把计数加一并持久化到独立的数据库。
 * 真机上重启应用后再次进入自检页，计数应延续而不是归零。
 */
export async function bumpPersistenceCounter(opener: SqlConnectionOpener): Promise<number> {
  const db = await LocalDatabase.open(opener, 'selfcheck-persist')
  try {
    const next = Number((await db.getKv('selfcheck_runs')) ?? '0') + 1
    await db.setKv('selfcheck_runs', String(next))
    return next
  } finally {
    await db.close()
  }
}

function sampleEntry(id: string): LocalLedgerEntry {
  return {
    id,
    trip_id: TRIP_ID,
    kind: 'expense',
    amount: '128.50',
    split_count: 1,
    personal_amount: '128.50',
    category_id: CATEGORY_ID,
    occurred_on: '2026-10-02',
    notes: '午餐',
    refunded_entry_id: null,
    attachment_asset_ids: [],
    version: 1,
    created_at: '2026-10-02T04:30:00Z',
    updated_at: '2026-10-02T04:30:00Z',
    deleted_at: null,
  }
}
