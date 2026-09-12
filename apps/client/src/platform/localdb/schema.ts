/**
 * 本地数据库结构 v1。字段名与服务端资源保持一致（同步快照可直接落表），
 * 类型按 SQLite 惯例：金额与版本外的时间、日期、UUID 都是 TEXT，JSON 数组以 TEXT 保存。
 * 本版只包含验证数据链路所需的表；其余业务表随同步实现在后续迁移中追加。
 */
export const SCHEMA_V1: readonly string[] = [
  // 键值：同步游标、扫描与应用进度、上次同步时间等
  `CREATE TABLE kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
  )`,

  // 旅行基础信息（全部旅行都同步到本地）
  `CREATE TABLE trips (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    start_date    TEXT NOT NULL,
    end_date      TEXT NOT NULL,
    currency_code TEXT NOT NULL,
    timezone      TEXT NOT NULL,
    budget_amount TEXT,
    version       INTEGER NOT NULL CHECK (version > 0),
    updated_at    TEXT NOT NULL,
    deleted_at    TEXT
  )`,

  // 账号级账单分类
  `CREATE TABLE expense_categories (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    icon       TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    version    INTEGER NOT NULL CHECK (version > 0),
    deleted_at TEXT
  )`,

  // 账目：离线新增与编辑的主要对象
  `CREATE TABLE ledger_entries (
    id                   TEXT PRIMARY KEY,
    trip_id              TEXT NOT NULL REFERENCES trips (id),
    kind                 TEXT NOT NULL CHECK (kind IN ('expense', 'refund')),
    amount               TEXT NOT NULL,
    category_id          TEXT NOT NULL REFERENCES expense_categories (id),
    occurred_on          TEXT NOT NULL,
    notes                TEXT NOT NULL DEFAULT '',
    refunded_entry_id    TEXT REFERENCES ledger_entries (id),
    attachment_asset_ids TEXT NOT NULL DEFAULT '[]',
    version              INTEGER NOT NULL CHECK (version > 0),
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    deleted_at           TEXT
  )`,
  `CREATE INDEX ledger_entries_trip_occurred_idx ON ledger_entries (trip_id, occurred_on DESC)`,

  // 待同步操作：与业务行在同一事务写入；seq 保证按本地提交顺序推送
  `CREATE TABLE pending_operations (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    operation_id TEXT NOT NULL UNIQUE,
    type         TEXT NOT NULL,
    entity_type  TEXT NOT NULL,
    entity_id    TEXT NOT NULL,
    trip_id      TEXT,
    base_version INTEGER,
    payload      TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'conflict', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT,
    created_at   TEXT NOT NULL
  )`,
  `CREATE INDEX pending_operations_status_seq_idx ON pending_operations (status, seq)`,
  `CREATE INDEX pending_operations_entity_idx ON pending_operations (entity_type, entity_id)`,
]
