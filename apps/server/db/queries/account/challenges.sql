-- 邮箱验证码挑战。

-- name: InvalidateChallenges :exec
UPDATE auth_challenges
SET invalidated_at = $3
WHERE email_key = $1 AND purpose = $2 AND consumed_at IS NULL AND invalidated_at IS NULL;

-- name: CreateChallenge :one
INSERT INTO auth_challenges (id, purpose, email_key, code_mac, key_id, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetChallengeForUpdate :one
SELECT * FROM auth_challenges
WHERE id = $1
FOR UPDATE;

-- name: IncrementChallengeAttempts :exec
UPDATE auth_challenges
SET attempts = attempts + 1
WHERE id = $1;

-- name: ConsumeChallenge :exec
UPDATE auth_challenges
SET consumed_at = $2
WHERE id = $1;

-- name: SetChallengeDelivery :exec
UPDATE auth_challenges
SET delivery_status = $2
WHERE id = $1;

-- name: CountChallengesSince :one
SELECT count(*) AS n FROM auth_challenges
WHERE email_key = $1 AND purpose = $2 AND created_at >= $3;

-- name: LatestChallengeCreatedAt :one
SELECT created_at FROM auth_challenges
WHERE email_key = $1 AND purpose = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: DeleteExpiredChallenges :execrows
DELETE FROM auth_challenges
WHERE expires_at < $1;
