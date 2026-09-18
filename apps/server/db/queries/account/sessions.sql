-- 登录会话。摘要列只做常量时间比较，所有查找按主键。

-- name: CreateSession :one
INSERT INTO account_sessions (id, account_id, client_kind, device_id, device_name, refresh_token_hash, csrf_token_hash, expires_at, created_at, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
RETURNING *;

-- name: GetSession :one
SELECT * FROM account_sessions
WHERE id = $1;

-- name: GetSessionForUpdate :one
SELECT * FROM account_sessions
WHERE id = $1
FOR UPDATE;

-- name: RotateSession :exec
UPDATE account_sessions
SET refresh_token_hash = $2,
    previous_refresh_token_hash = $3,
    previous_rotated_at = $4,
    csrf_token_hash = $5,
    last_seen_at = $6
WHERE id = $1;

-- name: ReissueSessionWithinGrace :exec
UPDATE account_sessions
SET refresh_token_hash = $2, csrf_token_hash = $3, last_seen_at = $4
WHERE id = $1;

-- name: TouchSession :exec
UPDATE account_sessions
SET last_seen_at = $2
WHERE id = $1 AND last_seen_at < $2::timestamptz - interval '1 minute';

-- name: SetSessionReauthenticated :exec
UPDATE account_sessions
SET reauthenticated_at = $2
WHERE id = $1;

-- name: RevokeSession :exec
UPDATE account_sessions
SET revoked_at = $2
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAccountSessions :exec
UPDATE account_sessions
SET revoked_at = $2
WHERE account_id = $1 AND revoked_at IS NULL;

-- name: RevokeOtherAccountSessions :exec
UPDATE account_sessions
SET revoked_at = $3
WHERE account_id = $1 AND id <> $2 AND revoked_at IS NULL;

-- name: ListActiveSessions :many
SELECT * FROM account_sessions
WHERE account_id = $1 AND revoked_at IS NULL AND expires_at > $2
ORDER BY created_at DESC;
