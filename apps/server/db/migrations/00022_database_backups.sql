-- +goose Up
CREATE TABLE admin_backup_settings (
    id integer PRIMARY KEY CHECK (id = 1),
    database_id uuid NOT NULL DEFAULT gen_random_uuid(),
    version bigint NOT NULL DEFAULT 1,
    config jsonb NOT NULL,
    next_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO admin_backup_settings(id, config) VALUES (1, '{"enabled":false,"time":"03:00","retain":7,"url":"","username":"","secret":"","destination":""}');

CREATE TABLE admin_backup_runs (
    id uuid PRIMARY KEY,
    trigger text NOT NULL CHECK (trigger IN ('manual','scheduled')),
    state text NOT NULL CHECK (state IN ('queued','running','uploading','verifying','retrying','succeeded','failed')),
    config jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    snapshot_at timestamptz,
    finished_at timestamptz,
    size_bytes bigint NOT NULL DEFAULT 0,
    sha256 text NOT NULL DEFAULT '',
    error_code text NOT NULL DEFAULT '',
    cleanup_error text NOT NULL DEFAULT '',
    remote_deleted_at timestamptz,
    manifest jsonb NOT NULL DEFAULT '{}'
);
CREATE UNIQUE INDEX admin_backup_one_active ON admin_backup_runs ((true)) WHERE state IN ('queued','running','uploading','verifying','retrying');
CREATE INDEX admin_backup_runs_created ON admin_backup_runs(created_at DESC, id DESC);

-- +goose Down
DROP TABLE admin_backup_runs;
DROP TABLE admin_backup_settings;
