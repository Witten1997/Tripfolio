-- +goose Up
-- 旅行分享链接。见数据库设计 v0.4 表 24 与 docs/superpowers/specs/2026-09-15-旅行分享-design.md。
-- 不进入同步体系：不写变更日志、不递增旅行版本；关闭分享是物理删行。

CREATE TABLE trip_shares (
    id             uuid PRIMARY KEY,
    account_id     uuid        NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    trip_id        uuid        NOT NULL,
    token          varchar(22) NOT NULL,
    created_at     timestamptz NOT NULL,
    rotated_at     timestamptz,
    last_viewed_at timestamptz,
    view_count     bigint      NOT NULL DEFAULT 0,
    CONSTRAINT trip_shares_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id) ON DELETE CASCADE,
    CONSTRAINT trip_shares_trip_unique UNIQUE (account_id, trip_id),
    CONSTRAINT trip_shares_token_unique UNIQUE (token),
    CONSTRAINT trip_shares_token_format CHECK (token ~ '^[A-Za-z0-9_-]{22}$'),
    CONSTRAINT trip_shares_view_count_nonnegative CHECK (view_count >= 0)
);

-- +goose Down
DROP TABLE trip_shares;
