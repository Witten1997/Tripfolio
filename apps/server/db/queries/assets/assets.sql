-- 私有文件资产（数据库设计表 14）。对象键由适配器按 account_id、asset_id、upload_attempt 推导后传入；
-- 暂存键、最终键、缩略图键与声明信息不进入公开模型，由独立查询按需读取。

-- name: GetAssetTripInfo :one
SELECT deleted_at FROM trips
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id);

-- name: AssetIDExists :one
SELECT EXISTS (SELECT 1 FROM assets a WHERE a.id = sqlc.arg(id))
    OR EXISTS (SELECT 1 FROM entity_tombstones et WHERE et.account_id = sqlc.arg(account_id) AND et.entity_type = 'asset' AND et.entity_id = sqlc.arg(id)) AS exists;

-- name: GetAsset :one
SELECT * FROM assets
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id);

-- name: GetAssetForUpdate :one
SELECT * FROM assets
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- 批量读取保持输入顺序：unnest WITH ORDINALITY 再按序号排序，适配器据此判断缺失项。
-- name: GetAssetsByIDs :many
SELECT a.*
FROM unnest(sqlc.arg(ids)::uuid[]) WITH ORDINALITY AS req(id, ord)
JOIN assets a ON a.id = req.id AND a.account_id = sqlc.arg(account_id)
ORDER BY req.ord;

-- name: InsertAsset :one
INSERT INTO assets (
    id, account_id, version, created_at, updated_at, scope, trip_id, original_name,
    status, upload_attempt, staging_object_key, expected_size, declared_media_type, client_sha256,
    upload_expires_at, thumbnail_status
) VALUES (
    sqlc.arg(id), sqlc.arg(account_id), 1, sqlc.arg(created_at), sqlc.arg(created_at), sqlc.arg(scope), sqlc.narg(trip_id), sqlc.arg(original_name),
    'uploading', 1, sqlc.arg(staging_object_key), sqlc.arg(expected_size), sqlc.arg(declared_media_type), sqlc.narg(client_sha256),
    sqlc.arg(upload_expires_at), 'none'
)
RETURNING *;

-- 新尝试：序号加 1、换暂存键与截止时间、回到 uploading 并清掉上次错误码；最终键留空由校验重写。
-- name: StartAssetAttempt :one
UPDATE assets
SET status = 'uploading', upload_attempt = sqlc.arg(upload_attempt), staging_object_key = sqlc.arg(staging_object_key),
    upload_expires_at = sqlc.arg(upload_expires_at), confirmed_at = NULL, error_code = NULL,
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- 续签：只延长当前尝试的截止时间，暂存键与序号不变。
-- name: RenewAssetAttempt :one
UPDATE assets
SET upload_expires_at = sqlc.arg(upload_expires_at), version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- 确认：转入 processing，不再有确认窗口；暂存键保留给 worker 读取。
-- name: MarkAssetProcessing :one
UPDATE assets
SET status = 'processing', confirmed_at = sqlc.arg(confirmed_at), upload_expires_at = NULL,
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- 校验通过：写入最终键与实际元数据，清空暂存键（ready 的 CHECK 要求）；缩略图状态由适配器按类型传入。
-- name: MarkAssetReady :one
UPDATE assets
SET status = 'ready', object_key = sqlc.arg(object_key), staging_object_key = NULL, upload_expires_at = NULL, error_code = NULL,
    media_type = sqlc.arg(media_type), byte_size = sqlc.arg(byte_size), sha256 = sqlc.arg(sha256),
    width = sqlc.narg(width), height = sqlc.narg(height),
    exif_taken_at_local = sqlc.narg(exif_taken_at_local), exif_latitude = sqlc.narg(exif_latitude), exif_longitude = sqlc.narg(exif_longitude),
    thumbnail_status = sqlc.arg(thumbnail_status),
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- 校验失败：记录可公开的错误码并清空暂存键，等待用户开启新尝试。
-- name: MarkAssetFailed :one
UPDATE assets
SET status = 'failed', error_code = sqlc.arg(error_code), staging_object_key = NULL, upload_expires_at = NULL,
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: SetAssetThumbnail :one
UPDATE assets
SET thumbnail_status = sqlc.arg(thumbnail_status), thumbnail_object_key = sqlc.narg(thumbnail_object_key),
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- 列表按 (created_at, id) 降序走部分索引 assets_trip_created_idx；ids 为空数组时不过滤。
-- name: ListTripAssets :many
SELECT * FROM assets a
WHERE a.account_id = sqlc.arg(account_id) AND a.trip_id = sqlc.arg(trip_id) AND a.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR a.status = sqlc.narg(status)::text)
  AND (cardinality(sqlc.arg(ids)::uuid[]) = 0 OR a.id = ANY(sqlc.arg(ids)::uuid[]))
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL
       OR (a.created_at, a.id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg(row_limit);
