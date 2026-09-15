-- 旅行分享（数据库设计 v0.4 表 24）。解析查询联出主人账号状态与旅行删除标记，访客侧一次查询完成全部前置判断。

-- name: GetTripShareByTrip :one
SELECT * FROM trip_shares
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id);

-- name: ResolveTripShareToken :one
SELECT s.*, a.status AS owner_status, t.deleted_at AS trip_deleted_at
FROM trip_shares s
JOIN accounts a ON a.id = s.account_id
JOIN trips t ON t.account_id = s.account_id AND t.id = s.trip_id
WHERE s.token = sqlc.arg(token);

-- name: InsertTripShare :one
INSERT INTO trip_shares (id, account_id, trip_id, token, created_at)
VALUES (sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(trip_id), sqlc.arg(token), sqlc.arg(created_at))
ON CONFLICT (account_id, trip_id) DO NOTHING
RETURNING *;

-- name: RotateTripShare :one
UPDATE trip_shares
SET token = sqlc.arg(token), rotated_at = sqlc.arg(rotated_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id)
RETURNING *;

-- name: DeleteTripShare :execrows
DELETE FROM trip_shares
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id);

-- name: RecordTripShareView :exec
UPDATE trip_shares
SET view_count = view_count + 1, last_viewed_at = sqlc.arg(viewed_at)
WHERE id = sqlc.arg(id);
