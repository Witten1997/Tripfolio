-- 账目与票据（数据库设计表 9、10）与开支统计（数据库设计 §5）。
-- currency_code 不存列，查询时从 trips 派生；金额 NUMERIC(18,4) 由适配器按币种小数位转回规范字符串。

-- name: GetLedgerTripInfo :one
SELECT currency_code, timezone, budget_amount, currency_locked_at, deleted_at FROM trips
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id);

-- 币种锁定与解锁：只改 currency_locked_at，递增旅行版本并返回整行作为同步快照。
-- name: SetTripCurrencyLock :one
UPDATE trips
SET currency_locked_at = sqlc.narg(locked_at), version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: ExpenseCategoryActive :one
SELECT EXISTS (
    SELECT 1 FROM expense_categories
    WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
) AS active;

-- 票据引用校验：同账号同旅行、未删除的图片资产，状态不限。
-- name: ListTripImageAssetIDs :many
SELECT id FROM assets
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id)::uuid AND deleted_at IS NULL
  AND id = ANY(sqlc.arg(ids)::uuid[])
  AND COALESCE(media_type, declared_media_type, 'image/') LIKE 'image/%';

-- name: GetLedgerEntry :one
SELECT sqlc.embed(l), t.currency_code
FROM ledger_entries l
JOIN trips t ON t.account_id = l.account_id AND t.id = l.trip_id
WHERE l.account_id = sqlc.arg(account_id) AND l.trip_id = sqlc.arg(trip_id) AND l.id = sqlc.arg(id);

-- name: GetLedgerEntryForUpdate :one
SELECT sqlc.embed(l), t.currency_code
FROM ledger_entries l
JOIN trips t ON t.account_id = l.account_id AND t.id = l.trip_id
WHERE l.account_id = sqlc.arg(account_id) AND l.trip_id = sqlc.arg(trip_id) AND l.id = sqlc.arg(id)
FOR UPDATE OF l;

-- name: LedgerEntryIDExists :one
SELECT EXISTS (SELECT 1 FROM ledger_entries l WHERE l.id = sqlc.arg(id))
    OR EXISTS (SELECT 1 FROM entity_tombstones et WHERE et.account_id = sqlc.arg(account_id) AND et.entity_type = 'ledger_entry' AND et.entity_id = sqlc.arg(id)) AS exists;

-- name: InsertLedgerEntry :one
INSERT INTO ledger_entries (id, account_id, trip_id, kind, amount, split_count, personal_amount, payer_member_id, split_mode, category_id, occurred_on, notes, refunded_entry_id, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(trip_id), sqlc.arg(kind), sqlc.arg(amount), sqlc.arg(split_count), sqlc.arg(personal_amount), sqlc.arg(payer_member_id), sqlc.arg(split_mode), sqlc.arg(category_id), sqlc.arg(occurred_on),
        sqlc.arg(notes), sqlc.narg(refunded_entry_id), sqlc.arg(created_at), sqlc.arg(created_at))
RETURNING *;

-- name: UpdateLedgerEntry :one
UPDATE ledger_entries
SET amount = sqlc.arg(amount), split_count = sqlc.arg(split_count), personal_amount = sqlc.arg(personal_amount), payer_member_id = sqlc.arg(payer_member_id), split_mode = sqlc.arg(split_mode),
    category_id = sqlc.arg(category_id), occurred_on = sqlc.arg(occurred_on), notes = sqlc.arg(notes),
    refunded_entry_id = sqlc.narg(refunded_entry_id), version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: SoftDeleteLedgerEntry :one
UPDATE ledger_entries
SET deleted_at = sqlc.arg(deleted_at), version = version + 1, updated_at = sqlc.arg(deleted_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;

-- 票据引用：整体替换。
-- name: DeleteLedgerAttachments :exec
DELETE FROM ledger_attachments
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND ledger_entry_id = sqlc.arg(ledger_entry_id);

-- name: InsertLedgerAttachments :exec
INSERT INTO ledger_attachments (account_id, trip_id, ledger_entry_id, asset_id, sort_order, created_at)
SELECT sqlc.arg(account_id), sqlc.arg(trip_id), sqlc.arg(ledger_entry_id), a.asset_id, (a.ord - 1)::int, sqlc.arg(created_at)
FROM unnest(sqlc.arg(asset_ids)::uuid[]) WITH ORDINALITY AS a(asset_id, ord);

-- name: ListLedgerAttachments :many
SELECT ledger_entry_id, asset_id FROM ledger_attachments
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND ledger_entry_id = ANY(sqlc.arg(entry_ids)::uuid[])
ORDER BY ledger_entry_id, sort_order, asset_id;

-- 分摊份额：整体替换；amounts 与 member_ids 一一对应，顺序即 sort_order。
-- name: DeleteLedgerSplits :exec
DELETE FROM ledger_entry_splits
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND ledger_entry_id = sqlc.arg(ledger_entry_id);

-- name: InsertLedgerSplits :exec
INSERT INTO ledger_entry_splits (account_id, trip_id, ledger_entry_id, member_id, amount, sort_order)
SELECT sqlc.arg(account_id), sqlc.arg(trip_id), sqlc.arg(ledger_entry_id), m.member_id, a.amount::numeric, (m.ord - 1)::int
FROM unnest(sqlc.arg(member_ids)::uuid[]) WITH ORDINALITY AS m(member_id, ord)
JOIN unnest(sqlc.arg(amounts)::text[]) WITH ORDINALITY AS a(amount, ord) ON a.ord = m.ord;

-- name: ListLedgerSplits :many
SELECT ledger_entry_id, member_id, amount FROM ledger_entry_splits
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND ledger_entry_id = ANY(sqlc.arg(entry_ids)::uuid[])
ORDER BY ledger_entry_id, sort_order, member_id;

-- 关联到某原支出的全部有效退款并锁定，按创建时间、id 升序。
-- name: ListLinkedRefundsForUpdate :many
SELECT * FROM ledger_entries
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND refunded_entry_id = sqlc.arg(expense_id)
  AND deleted_at IS NULL AND kind = 'refund'
ORDER BY created_at, id
FOR UPDATE;

-- name: CountActiveLedgerEntries :one
SELECT count(*) FROM ledger_entries
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL;

-- 列表：接口设计 3.9 LedgerFilters；游标用行比较走 (account_id, trip_id, occurred_on DESC, id DESC) 部分索引。
-- name: ListLedgerEntries :many
SELECT sqlc.embed(l), t.currency_code
FROM ledger_entries l
JOIN trips t ON t.account_id = l.account_id AND t.id = l.trip_id
WHERE l.account_id = sqlc.arg(account_id) AND l.trip_id = sqlc.arg(trip_id) AND l.deleted_at IS NULL
  AND (sqlc.narg(date_from)::date IS NULL OR l.occurred_on >= sqlc.narg(date_from)::date)
  AND (sqlc.narg(date_to)::date IS NULL OR l.occurred_on <= sqlc.narg(date_to)::date)
  AND (sqlc.narg(category_id)::uuid IS NULL OR l.category_id = sqlc.narg(category_id)::uuid)
  AND (sqlc.narg(kind)::text IS NULL OR l.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(refunded_entry_id)::uuid IS NULL OR l.refunded_entry_id = sqlc.narg(refunded_entry_id)::uuid)
  AND (sqlc.narg(cursor_occurred_on)::date IS NULL
       OR (l.occurred_on, l.id) < (sqlc.narg(cursor_occurred_on)::date, sqlc.narg(cursor_id)::uuid))
ORDER BY l.occurred_on DESC, l.id DESC
LIMIT sqlc.arg(row_limit);

-- 统计（数据库设计 §5）：净额用 CASE 聚合，按实际 occurred_on；支出与退款都取「我」的份额 personal_amount；下面四条在同一个只读一致性事务中执行。
-- name: LedgerFilteredTotals :one
SELECT COALESCE(SUM(CASE WHEN kind = 'expense' THEN personal_amount END), 0)::numeric AS expense_amount,
       COALESCE(SUM(CASE WHEN kind = 'refund' THEN personal_amount END), 0)::numeric AS refund_amount,
       count(*) AS entry_count
FROM ledger_entries
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL
  AND (sqlc.narg(date_from)::date IS NULL OR occurred_on >= sqlc.narg(date_from)::date)
  AND (sqlc.narg(date_to)::date IS NULL OR occurred_on <= sqlc.narg(date_to)::date)
  AND (sqlc.narg(category_id)::uuid IS NULL OR category_id = sqlc.narg(category_id)::uuid);

-- name: LedgerTripTotals :one
SELECT COALESCE(SUM(CASE WHEN kind = 'expense' THEN personal_amount END), 0)::numeric AS expense_amount,
       COALESCE(SUM(CASE WHEN kind = 'refund' THEN personal_amount END), 0)::numeric AS refund_amount
FROM ledger_entries
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL;

-- 每个有效分类以及仍被本旅行有效账目引用的已删除分类；筛选范围的金额与整趟的金额分别聚合。
-- name: LedgerCategoryTotals :many
SELECT c.id AS category_id, c.name, c.icon, c.sort_order,
       COALESCE(SUM(CASE WHEN l.kind = 'expense'
                              AND (sqlc.narg(date_from)::date IS NULL OR l.occurred_on >= sqlc.narg(date_from)::date)
                              AND (sqlc.narg(date_to)::date IS NULL OR l.occurred_on <= sqlc.narg(date_to)::date)
                              AND (sqlc.narg(category_id)::uuid IS NULL OR l.category_id = sqlc.narg(category_id)::uuid)
                         THEN l.personal_amount END), 0)::numeric AS filtered_expense,
       COALESCE(SUM(CASE WHEN l.kind = 'refund'
                              AND (sqlc.narg(date_from)::date IS NULL OR l.occurred_on >= sqlc.narg(date_from)::date)
                              AND (sqlc.narg(date_to)::date IS NULL OR l.occurred_on <= sqlc.narg(date_to)::date)
                              AND (sqlc.narg(category_id)::uuid IS NULL OR l.category_id = sqlc.narg(category_id)::uuid)
                         THEN l.personal_amount END), 0)::numeric AS filtered_refund,
       COALESCE(SUM(CASE WHEN l.kind = 'expense' THEN l.personal_amount END), 0)::numeric AS trip_expense,
       COALESCE(SUM(CASE WHEN l.kind = 'refund' THEN l.personal_amount END), 0)::numeric AS trip_refund
FROM expense_categories c
LEFT JOIN ledger_entries l
       ON l.account_id = c.account_id AND l.category_id = c.id AND l.trip_id = sqlc.arg(trip_id) AND l.deleted_at IS NULL
WHERE c.account_id = sqlc.arg(account_id)
GROUP BY c.id, c.name, c.icon, c.sort_order, c.deleted_at
HAVING c.deleted_at IS NULL OR count(l.id) > 0
ORDER BY c.sort_order, c.id;

-- 每日明细：只含有账目的日期，按日期降序，游标取更早的日期。
-- name: LedgerDailyTotals :many
SELECT occurred_on,
       COALESCE(SUM(CASE WHEN kind = 'expense' THEN personal_amount END), 0)::numeric AS expense_amount,
       COALESCE(SUM(CASE WHEN kind = 'refund' THEN personal_amount END), 0)::numeric AS refund_amount
FROM ledger_entries
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL
  AND (sqlc.narg(date_from)::date IS NULL OR occurred_on >= sqlc.narg(date_from)::date)
  AND (sqlc.narg(date_to)::date IS NULL OR occurred_on <= sqlc.narg(date_to)::date)
  AND (sqlc.narg(category_id)::uuid IS NULL OR category_id = sqlc.narg(category_id)::uuid)
  AND (sqlc.narg(cursor_date)::date IS NULL OR occurred_on < sqlc.narg(cursor_date)::date)
GROUP BY occurred_on
ORDER BY occurred_on DESC
LIMIT sqlc.arg(row_limit);
