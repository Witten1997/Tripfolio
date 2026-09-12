-- +goose Up
-- 会话与邮箱验证码挑战。见数据库设计 v0.3 表 2、表 3。

CREATE TABLE account_sessions (
    id                          uuid PRIMARY KEY,
    account_id                  uuid         NOT NULL REFERENCES accounts (id),
    client_kind                 varchar(16)  NOT NULL,
    device_id                   uuid         NOT NULL,
    device_name                 varchar(120),
    refresh_token_hash          bytea        NOT NULL,
    previous_refresh_token_hash bytea,
    previous_rotated_at         timestamptz,
    csrf_token_hash             bytea,
    reauthenticated_at          timestamptz,
    created_at                  timestamptz  NOT NULL DEFAULT now(),
    last_seen_at                timestamptz  NOT NULL DEFAULT now(),
    expires_at                  timestamptz  NOT NULL,
    revoked_at                  timestamptz,
    CONSTRAINT account_sessions_client_kind_enum CHECK (client_kind IN ('web', 'android', 'harmony')),
    CONSTRAINT account_sessions_refresh_hash_len CHECK (octet_length(refresh_token_hash) = 32),
    CONSTRAINT account_sessions_previous_hash_len CHECK (previous_refresh_token_hash IS NULL OR octet_length(previous_refresh_token_hash) = 32),
    CONSTRAINT account_sessions_csrf_hash_len CHECK (csrf_token_hash IS NULL OR octet_length(csrf_token_hash) = 32),
    CONSTRAINT account_sessions_web_requires_csrf CHECK (client_kind <> 'web' OR csrf_token_hash IS NOT NULL),
    CONSTRAINT account_sessions_previous_pair CHECK ((previous_refresh_token_hash IS NULL) = (previous_rotated_at IS NULL))
);

CREATE INDEX account_sessions_account_created_idx ON account_sessions (account_id, created_at DESC);
CREATE INDEX account_sessions_expires_idx ON account_sessions (expires_at);

CREATE TABLE auth_challenges (
    id              uuid PRIMARY KEY,
    purpose         varchar(24)  NOT NULL,
    email_key       varchar(254) NOT NULL,
    code_mac        bytea        NOT NULL,
    key_id          varchar(32)  NOT NULL,
    attempts        smallint     NOT NULL DEFAULT 0,
    delivery_status varchar(16)  NOT NULL DEFAULT 'pending',
    created_at      timestamptz  NOT NULL DEFAULT now(),
    expires_at      timestamptz  NOT NULL,
    consumed_at     timestamptz,
    invalidated_at  timestamptz,
    CONSTRAINT auth_challenges_purpose_enum CHECK (purpose IN ('register', 'reset_password')),
    CONSTRAINT auth_challenges_code_mac_len CHECK (octet_length(code_mac) = 32),
    CONSTRAINT auth_challenges_attempts_range CHECK (attempts BETWEEN 0 AND 5),
    CONSTRAINT auth_challenges_delivery_enum CHECK (delivery_status IN ('pending', 'sent', 'failed'))
);

CREATE INDEX auth_challenges_email_purpose_idx ON auth_challenges (email_key, purpose, created_at DESC);
CREATE INDEX auth_challenges_expires_idx ON auth_challenges (expires_at);

-- +goose Down
DROP TABLE auth_challenges;
DROP TABLE account_sessions;
