-- 旅行成员（数据库设计表 25）。percent 为 NUMERIC(5,2)，适配器按 2 位小数转回字符串。

-- name: GetTripMember :one
SELECT * FROM trip_members
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id);

-- 有效成员按 sort_order、id 升序。
-- name: ListTripMembers :many
SELECT * FROM trip_members
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL
ORDER BY sort_order, id;

-- 整体保存前锁定本旅行全部有效成员。
-- name: ListTripMembersForUpdate :many
SELECT * FROM trip_members
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL
ORDER BY sort_order, id
FOR UPDATE;

-- name: TripMemberIDExists :one
SELECT EXISTS (SELECT 1 FROM trip_members m WHERE m.id = sqlc.arg(id))
    OR EXISTS (SELECT 1 FROM entity_tombstones et WHERE et.account_id = sqlc.arg(account_id) AND et.entity_type = 'trip_member' AND et.entity_id = sqlc.arg(id)) AS exists;

-- name: InsertTripMember :one
INSERT INTO trip_members (id, account_id, trip_id, name, share_percent, sort_order, is_self, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(trip_id), sqlc.arg(name), sqlc.arg(share_percent), sqlc.arg(sort_order), sqlc.arg(is_self),
        sqlc.arg(created_at), sqlc.arg(created_at))
RETURNING *;

-- name: UpdateTripMember :one
UPDATE trip_members
SET name = sqlc.arg(name), share_percent = sqlc.arg(share_percent), sort_order = sqlc.arg(sort_order),
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: SoftDeleteTripMember :one
UPDATE trip_members
SET deleted_at = sqlc.arg(deleted_at), version = version + 1, updated_at = sqlc.arg(deleted_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;

-- 删除前的引用检查：有效账目的付款人或分摊参与人。
-- name: CountTripMemberReferences :one
SELECT count(*) FROM ledger_entries l
WHERE l.account_id = sqlc.arg(account_id) AND l.trip_id = sqlc.arg(trip_id) AND l.deleted_at IS NULL
  AND (l.payer_member_id = sqlc.arg(member_id)
       OR EXISTS (SELECT 1 FROM ledger_entry_splits s
                  WHERE s.account_id = l.account_id AND s.trip_id = l.trip_id AND s.ledger_entry_id = l.id AND s.member_id = sqlc.arg(member_id)));

-- 成员结算（接口设计 3.8 TripSettlement）：每位有效成员作为付款人与参与人的支出、退款合计。
-- name: TripMemberSettlement :many
SELECT m.id AS member_id, m.name, m.is_self,
       COALESCE((SELECT SUM(CASE WHEN l.kind = 'expense' THEN l.amount ELSE -l.amount END) FROM ledger_entries l
                 WHERE l.account_id = m.account_id AND l.trip_id = m.trip_id AND l.payer_member_id = m.id AND l.deleted_at IS NULL), 0)::numeric AS paid_amount,
       COALESCE((SELECT SUM(CASE WHEN l.kind = 'expense' THEN s.amount ELSE -s.amount END) FROM ledger_entry_splits s
                 JOIN ledger_entries l ON l.account_id = s.account_id AND l.trip_id = s.trip_id AND l.id = s.ledger_entry_id
                 WHERE s.account_id = m.account_id AND s.trip_id = m.trip_id AND s.member_id = m.id AND l.deleted_at IS NULL), 0)::numeric AS owed_amount
FROM trip_members m
WHERE m.account_id = sqlc.arg(account_id) AND m.trip_id = sqlc.arg(trip_id) AND m.deleted_at IS NULL
ORDER BY m.sort_order, m.id;
