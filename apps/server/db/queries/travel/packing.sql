-- 行李清单物品（数据库设计表 6）。同分类同名唯一由部分唯一索引兜底，应用层先用 PackingNameTaken 预检以返回 422。

-- name: GetPackingItem :one
SELECT * FROM packing_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id);

-- name: GetPackingItemForUpdate :one
SELECT * FROM packing_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- name: PackingItemIDExists :one
SELECT EXISTS (SELECT 1 FROM packing_items p WHERE p.id = sqlc.arg(id))
    OR EXISTS (SELECT 1 FROM entity_tombstones et WHERE et.account_id = sqlc.arg(account_id) AND et.entity_type = 'packing_item' AND et.entity_id = sqlc.arg(id)) AS exists;

-- name: PackingNameTaken :one
SELECT EXISTS (
    SELECT 1 FROM packing_items
    WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL
      AND category = sqlc.arg(category) AND lower(btrim(name)) = lower(btrim(sqlc.arg(name)::text))
      AND id <> sqlc.arg(exclude_id)
) AS exists;

-- name: InsertPackingItem :one
INSERT INTO packing_items (id, account_id, trip_id, name, category, quantity, notes, status, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(trip_id), sqlc.arg(name), sqlc.arg(category), sqlc.arg(quantity), sqlc.arg(notes), sqlc.arg(status),
        sqlc.arg(created_at), sqlc.arg(created_at))
RETURNING *;

-- name: UpdatePackingItem :one
UPDATE packing_items
SET name = sqlc.arg(name), category = sqlc.arg(category), quantity = sqlc.arg(quantity), notes = sqlc.arg(notes), status = sqlc.arg(status),
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: SoftDeletePackingItem :one
UPDATE packing_items
SET deleted_at = sqlc.arg(deleted_at), version = version + 1, updated_at = sqlc.arg(deleted_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;

-- 列表：接口设计 3.9 PackingFilters；游标走 (account_id, trip_id, category, created_at, id) 部分索引。
-- name: ListPackingItems :many
SELECT * FROM packing_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL
  AND (sqlc.narg(category)::text IS NULL OR category = sqlc.narg(category)::text)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(cursor_category)::text IS NULL
       OR (category, created_at, id) > (sqlc.narg(cursor_category)::text, sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY category, created_at, id
LIMIT sqlc.arg(row_limit);
