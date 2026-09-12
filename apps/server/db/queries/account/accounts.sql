-- 账号基础查询。规范化邮箱 email_key 由服务端生成（去首尾空格、小写），不在 SQL 中处理。

-- 时间一律由应用时钟给出（数据库时钟可能与应用不一致，会触发 updated_at >= created_at 约束）。
-- name: CreateAccount :one
INSERT INTO accounts (id, email, email_key, email_verified_at, password_hash, password_changed_at, nickname, default_timezone, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $4, $6, $7, $4, $4)
RETURNING *;

-- name: GetAccountByEmailKey :one
SELECT * FROM accounts
WHERE email_key = $1;

-- name: GetAccountByID :one
SELECT * FROM accounts
WHERE id = $1;

-- name: AccountExistsByEmailKey :one
SELECT EXISTS (SELECT 1 FROM accounts WHERE email_key = $1) AS exists;

-- name: UpdateAccountPassword :exec
UPDATE accounts
SET password_hash = $2, password_changed_at = $3, updated_at = $3
WHERE id = $1;

-- name: UpdateAccountProfile :one
UPDATE accounts
SET nickname = $2, avatar_asset_id = $3, default_timezone = $4, version = version + 1, updated_at = $5
WHERE id = $1
RETURNING *;

-- name: SetAccountStatus :exec
UPDATE accounts
SET status = $2, version = version + 1, updated_at = $3
WHERE id = $1;

-- name: CreateAccountSyncState :exec
INSERT INTO account_sync_state (account_id)
VALUES ($1);

-- name: GetAccountSyncState :one
SELECT * FROM account_sync_state
WHERE account_id = $1;
