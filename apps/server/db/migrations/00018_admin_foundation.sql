-- +goose Up
-- 后台只有超级管理员；资格与用户端会话分离，现有账号不会自动获得资格。
CREATE TABLE admin_principals (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE RESTRICT,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    granted_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    reason varchar(500) NOT NULL CHECK (btrim(reason) <> '')
);

CREATE TABLE admin_sessions (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES admin_principals(account_id) ON DELETE RESTRICT,
    principal_version bigint NOT NULL,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    password_changed_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    reauthenticated_at timestamptz,
    revoked_at timestamptz,
    source_ip varchar(64) NOT NULL DEFAULT '',
    user_agent varchar(512) NOT NULL DEFAULT '',
    CHECK (expires_at > created_at),
    CHECK (last_seen_at >= created_at)
);
CREATE INDEX admin_sessions_account_active_idx ON admin_sessions(account_id, expires_at) WHERE revoked_at IS NULL;

-- 审计编号不使用级联外键：业务对象或账号清理后仍保留操作历史。
CREATE TABLE admin_audit_events (
    id uuid PRIMARY KEY,
    actor_account_id uuid,
    subject_account_id uuid,
    session_id uuid,
    action varchar(80) NOT NULL,
    resource_type varchar(80) NOT NULL DEFAULT '',
    resource_id uuid,
    result varchar(16) NOT NULL CHECK (result IN ('success', 'failure', 'denied')),
    reason varchar(500) NOT NULL DEFAULT '',
    request_id varchar(128) NOT NULL DEFAULT '',
    source_ip varchar(64) NOT NULL DEFAULT '',
    user_agent varchar(512) NOT NULL DEFAULT '',
    details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    occurred_at timestamptz NOT NULL
);
CREATE INDEX admin_audit_events_time_idx ON admin_audit_events(occurred_at DESC, id DESC);
CREATE INDEX admin_audit_events_subject_idx ON admin_audit_events(subject_account_id, occurred_at DESC);
CREATE INDEX admin_audit_events_actor_idx ON admin_audit_events(actor_account_id, occurred_at DESC);

-- +goose Down
DROP TABLE admin_audit_events;
DROP TABLE admin_sessions;
DROP TABLE admin_principals;
