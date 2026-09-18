import type { SqlConnection, SqlConnectionOpener, SqlExecutor, SqlRow } from './executor'
import { currentLocalSchemaVersion, migrateLocalDatabase } from './migrations'
import { localDatabaseName } from './names'

export interface LocalTrip {
  id: string
  name: string
  start_date: string
  end_date: string
  currency_code: string
  timezone: string
  budget_amount: string | null
  version: number
  updated_at: string
  deleted_at: string | null
}

export interface LocalExpenseCategory {
  id: string
  name: string
  icon: string | null
  sort_order: number
  version: number
  deleted_at: string | null
}

export interface LocalLedgerEntry {
  id: string
  trip_id: string
  kind: 'expense' | 'refund'
  amount: string
  split_count: number
  personal_amount: string
  category_id: string
  occurred_on: string
  notes: string
  refunded_entry_id: string | null
  attachment_asset_ids: string[]
  version: number
  created_at: string
  updated_at: string
  deleted_at: string | null
}

export type PendingOperationStatus = 'queued' | 'conflict' | 'failed'

export interface PendingOperation {
  seq: number
  operation_id: string
  type: string
  entity_type: string
  entity_id: string
  trip_id: string | null
  base_version: number | null
  payload: unknown
  status: PendingOperationStatus
  attempts: number
  last_error: string | null
  created_at: string
}

export type CountableTable =
  'trips' | 'expense_categories' | 'ledger_entries' | 'pending_operations'

/**
 * 某个账号的本地数据库。打开即迁移到最新结构；离线写入把业务行与待同步操作放在同一事务。
 * 这里只实现验证数据链路所需的方法，完整的同步仓储随同步协议实现补充。
 */
export class LocalDatabase {
  private constructor(
    readonly accountId: string,
    private readonly conn: SqlConnection,
  ) {}

  static async open(opener: SqlConnectionOpener, accountId: string): Promise<LocalDatabase> {
    const conn = await opener.open(localDatabaseName(accountId))
    try {
      await migrateLocalDatabase(conn)
    } catch (error) {
      await conn.close()
      throw error
    }
    return new LocalDatabase(accountId, conn)
  }

  get name(): string {
    return this.conn.name
  }

  close(): Promise<void> {
    return this.conn.close()
  }

  schemaVersion(): Promise<number> {
    return currentLocalSchemaVersion(this.conn)
  }

  /** 低层事务入口，供需要组合多条写入的调用方使用。 */
  transaction<T>(fn: (tx: SqlExecutor) => Promise<T>): Promise<T> {
    return this.conn.transaction(fn)
  }

  async count(table: CountableTable): Promise<number> {
    const rows = await this.conn.query<{ n: number }>(`SELECT count(*) AS n FROM ${table}`)
    return Number(rows[0]?.n ?? 0)
  }

  async getKv(key: string): Promise<string | null> {
    const rows = await this.conn.query<{ value: string }>('SELECT value FROM kv WHERE key = ?', [
      key,
    ])
    return rows[0]?.value ?? null
  }

  async setKv(key: string, value: string): Promise<void> {
    await this.conn.run(
      'INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value',
      [key, value],
    )
  }

  /** 写入或更新从服务端同步来的旅行；旧版本不覆盖新版本。 */
  async upsertTrip(trip: LocalTrip): Promise<void> {
    await this.conn.run(
      `INSERT INTO trips (id, name, start_date, end_date, currency_code, timezone, budget_amount, version, updated_at, deleted_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
       ON CONFLICT (id) DO UPDATE SET
         name = excluded.name, start_date = excluded.start_date, end_date = excluded.end_date,
         currency_code = excluded.currency_code, timezone = excluded.timezone, budget_amount = excluded.budget_amount,
         version = excluded.version, updated_at = excluded.updated_at, deleted_at = excluded.deleted_at
       WHERE excluded.version > trips.version`,
      [
        trip.id,
        trip.name,
        trip.start_date,
        trip.end_date,
        trip.currency_code,
        trip.timezone,
        trip.budget_amount,
        trip.version,
        trip.updated_at,
        trip.deleted_at,
      ],
    )
  }

  async upsertExpenseCategory(category: LocalExpenseCategory): Promise<void> {
    await this.conn.run(
      `INSERT INTO expense_categories (id, name, icon, sort_order, version, deleted_at)
       VALUES (?, ?, ?, ?, ?, ?)
       ON CONFLICT (id) DO UPDATE SET
         name = excluded.name, icon = excluded.icon, sort_order = excluded.sort_order,
         version = excluded.version, deleted_at = excluded.deleted_at
       WHERE excluded.version > expense_categories.version`,
      [
        category.id,
        category.name,
        category.icon,
        category.sort_order,
        category.version,
        category.deleted_at,
      ],
    )
  }

  /**
   * 离线新增账目：账目行与 ledger_entry.create 待同步操作在同一事务写入（技术选型 7.1）。
   * payload 与接口设计 4.1 一致：LedgerCreate 去掉 id 与 attachment_asset_ids。
   */
  async createLedgerEntryOffline(entry: LocalLedgerEntry, operationId: string): Promise<void> {
    const payload = {
      kind: entry.kind,
      amount: entry.amount,
      split_count: entry.split_count,
      category_id: entry.category_id,
      occurred_on: entry.occurred_on,
      notes: entry.notes,
      refunded_entry_id: entry.refunded_entry_id,
    }
    await this.conn.transaction(async (tx) => {
      await insertLedgerEntry(tx, entry)
      await tx.run(
        `INSERT INTO pending_operations (operation_id, type, entity_type, entity_id, trip_id, base_version, payload, created_at)
         VALUES (?, 'ledger_entry.create', 'ledger_entry', ?, ?, NULL, ?, ?)`,
        [operationId, entry.id, entry.trip_id, JSON.stringify(payload), entry.created_at],
      )
    })
  }

  async listLedgerEntries(
    tripId: string,
  ): Promise<Array<LocalLedgerEntry & { sync_pending: boolean }>> {
    const rows = await this.conn.query<SqlRow>(
      `SELECT l.*,
              EXISTS (
                SELECT 1 FROM pending_operations p
                WHERE p.entity_type = 'ledger_entry' AND p.entity_id = l.id AND p.status = 'queued'
              ) AS sync_pending
       FROM ledger_entries l
       WHERE l.trip_id = ? AND l.deleted_at IS NULL
       ORDER BY l.occurred_on DESC, l.id DESC`,
      [tripId],
    )
    return rows.map((row) => ({
      ...rowToLedgerEntry(row),
      sync_pending: Number(row.sync_pending) === 1,
    }))
  }

  async listPendingOperations(): Promise<PendingOperation[]> {
    const rows = await this.conn.query<SqlRow>('SELECT * FROM pending_operations ORDER BY seq')
    return rows.map((row) => ({
      seq: Number(row.seq),
      operation_id: String(row.operation_id),
      type: String(row.type),
      entity_type: String(row.entity_type),
      entity_id: String(row.entity_id),
      trip_id: row.trip_id == null ? null : String(row.trip_id),
      base_version: row.base_version == null ? null : Number(row.base_version),
      payload: JSON.parse(String(row.payload)) as unknown,
      status: String(row.status) as PendingOperationStatus,
      attempts: Number(row.attempts),
      last_error: row.last_error == null ? null : String(row.last_error),
      created_at: String(row.created_at),
    }))
  }
}

async function insertLedgerEntry(tx: SqlExecutor, entry: LocalLedgerEntry): Promise<void> {
  await tx.run(
    `INSERT INTO ledger_entries
       (id, trip_id, kind, amount, split_count, personal_amount, category_id, occurred_on, notes, refunded_entry_id, attachment_asset_ids, version, created_at, updated_at, deleted_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    [
      entry.id,
      entry.trip_id,
      entry.kind,
      entry.amount,
      entry.split_count,
      entry.personal_amount,
      entry.category_id,
      entry.occurred_on,
      entry.notes,
      entry.refunded_entry_id,
      JSON.stringify(entry.attachment_asset_ids),
      entry.version,
      entry.created_at,
      entry.updated_at,
      entry.deleted_at,
    ],
  )
}

function rowToLedgerEntry(row: SqlRow): LocalLedgerEntry {
  return {
    id: String(row.id),
    trip_id: String(row.trip_id),
    kind: String(row.kind) as LocalLedgerEntry['kind'],
    amount: String(row.amount),
    split_count: Number(row.split_count),
    personal_amount: String(row.personal_amount),
    category_id: String(row.category_id),
    occurred_on: String(row.occurred_on),
    notes: String(row.notes ?? ''),
    refunded_entry_id: row.refunded_entry_id == null ? null : String(row.refunded_entry_id),
    attachment_asset_ids: JSON.parse(String(row.attachment_asset_ids ?? '[]')) as string[],
    version: Number(row.version),
    created_at: String(row.created_at),
    updated_at: String(row.updated_at),
    deleted_at: row.deleted_at == null ? null : String(row.deleted_at),
  }
}
