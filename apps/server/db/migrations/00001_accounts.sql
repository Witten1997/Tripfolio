-- +goose Up
-- 账号与账号级同步水位。字段与约束见 docs/database/2026-09-11-P0数据库表结构设计.md 表 1、表 15。
-- avatar_asset_id 到 assets 的外键在 assets 表建立后由后续迁移补建。

CREATE TABLE accounts (
    id                  uuid PRIMARY KEY,
    email               varchar(254) NOT NULL,
    email_key           varchar(254) NOT NULL,
    email_verified_at   timestamptz  NOT NULL,
    password_hash       text         NOT NULL,
    password_changed_at timestamptz  NOT NULL DEFAULT now(),
    nickname            varchar(64)  NOT NULL,
    avatar_asset_id     uuid,
    default_timezone    varchar(64)  NOT NULL DEFAULT 'Asia/Shanghai',
    status              varchar(16)  NOT NULL DEFAULT 'active',
    version             bigint       NOT NULL DEFAULT 1,
    created_at          timestamptz  NOT NULL DEFAULT now(),
    updated_at          timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT accounts_email_key_unique UNIQUE (email_key),
    CONSTRAINT accounts_email_not_blank CHECK (btrim(email) <> ''),
    CONSTRAINT accounts_email_key_not_blank CHECK (btrim(email_key) <> ''),
    CONSTRAINT accounts_nickname_length CHECK (char_length(nickname) BETWEEN 1 AND 64 AND btrim(nickname) <> ''),
    CONSTRAINT accounts_status_enum CHECK (status IN ('active', 'deleting')),
    CONSTRAINT accounts_version_positive CHECK (version > 0),
    CONSTRAINT accounts_updated_not_before_created CHECK (updated_at >= created_at)
);

-- 绝大多数行是 active，只索引需要清理扫描的非 active 行
CREATE INDEX accounts_status_non_active_idx ON accounts (status) WHERE status <> 'active';

CREATE TABLE account_sync_state (
    account_id         uuid        PRIMARY KEY REFERENCES accounts (id),
    last_seq           bigint      NOT NULL DEFAULT 0,
    retained_after_seq bigint      NOT NULL DEFAULT 0,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT account_sync_state_last_seq_nonnegative CHECK (last_seq >= 0),
    CONSTRAINT account_sync_state_retained_nonnegative CHECK (retained_after_seq >= 0),
    CONSTRAINT account_sync_state_retained_le_last CHECK (retained_after_seq <= last_seq)
) WITH (fillfactor = 70);

-- +goose Down
DROP TABLE account_sync_state;
DROP TABLE accounts;
