-- 统一写事务：账号锁、变更日志、操作收据、字段级合并读取。

-- name: LockAccountForWrite :one
SELECT s.last_seq, a.status
FROM account_sync_state s
JOIN accounts a ON a.id = s.account_id
WHERE s.account_id = $1
FOR UPDATE OF s;

-- name: AdvanceAccountSeq :exec
UPDATE account_sync_state
SET last_seq = $2, updated_at = $3
WHERE account_id = $1;

-- name: InsertSyncChange :exec
INSERT INTO sync_changes (
    account_id, seq, batch_id, batch_end_seq, entity_type, entity_id, trip_id,
    entity_version, change_kind, schema_version, snapshot, changed_fields, requires_snapshot, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: GetMutationReceipt :one
SELECT * FROM mutation_receipts
WHERE account_id = $1 AND operation_id = $2;

-- name: InsertMutationReceipt :exec
INSERT INTO mutation_receipts (account_id, operation_id, operation_type, request_hash, result, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ChangedFieldsBetweenVersions :many
SELECT entity_version, changed_fields
FROM sync_changes
WHERE account_id = $1 AND entity_type = $2 AND entity_id = $3
  AND entity_version > $4 AND entity_version <= $5
  AND change_kind = 'upsert'
ORDER BY entity_version;

-- name: ListSyncChanges :many
SELECT * FROM sync_changes
WHERE account_id = $1 AND seq > $2 AND seq <= $3
ORDER BY seq
LIMIT $4;
