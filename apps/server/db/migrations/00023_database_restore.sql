-- +goose Up
CREATE SCHEMA tripfolio_restore;
REVOKE ALL ON SCHEMA tripfolio_restore FROM PUBLIC;
CREATE TABLE tripfolio_restore.control (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    epoch bigint NOT NULL DEFAULT 0
);
INSERT INTO tripfolio_restore.control DEFAULT VALUES;
CREATE TABLE tripfolio_restore.jobs (
    id uuid PRIMARY KEY,
    backup_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('queued','preparing','restoring','succeeded','failed')),
    token_hash bytea NOT NULL,
    secret text NOT NULL,
    config jsonb NOT NULL,
    manifest jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    error_code text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX restore_one_active ON tripfolio_restore.jobs ((true)) WHERE state IN ('queued','preparing','restoring');
REVOKE ALL ON ALL TABLES IN SCHEMA tripfolio_restore FROM PUBLIC;

-- +goose Down
DROP SCHEMA tripfolio_restore CASCADE;
