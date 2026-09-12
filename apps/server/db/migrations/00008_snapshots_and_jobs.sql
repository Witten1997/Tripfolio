-- +goose Up
-- 快照、导出与清理任务。见数据库设计 v0.3 表 19–23。

CREATE TABLE data_snapshots (
    id                uuid PRIMARY KEY,
    account_id        uuid        NOT NULL REFERENCES accounts (id),
    purpose           varchar(16) NOT NULL,
    selected_trip_ids jsonb       NOT NULL,
    status            varchar(16) NOT NULL DEFAULT 'queued',
    high_water_seq    bigint,
    item_count        bigint      NOT NULL DEFAULT 0,
    schema_version    smallint    NOT NULL DEFAULT 1,
    error_code        varchar(64),
    created_at        timestamptz NOT NULL DEFAULT now(),
    captured_at       timestamptz,
    expires_at        timestamptz NOT NULL,
    CONSTRAINT data_snapshots_account_id_unique UNIQUE (account_id, id),
    CONSTRAINT data_snapshots_purpose_enum CHECK (purpose IN ('baseline', 'trip_reload', 'export')),
    CONSTRAINT data_snapshots_selected_array CHECK (jsonb_typeof(selected_trip_ids) = 'array'),
    CONSTRAINT data_snapshots_status_enum CHECK (status IN ('queued', 'building', 'ready', 'failed', 'invalidated', 'expired')),
    CONSTRAINT data_snapshots_high_water_nonnegative CHECK (high_water_seq IS NULL OR high_water_seq >= 0),
    CONSTRAINT data_snapshots_item_count_nonnegative CHECK (item_count >= 0),
    CONSTRAINT data_snapshots_ready_requires_capture CHECK (status <> 'ready' OR (high_water_seq IS NOT NULL AND captured_at IS NOT NULL))
);

CREATE INDEX data_snapshots_account_created_idx ON data_snapshots (account_id, created_at DESC);
CREATE INDEX data_snapshots_status_expires_idx ON data_snapshots (status, expires_at);

CREATE TABLE snapshot_items (
    snapshot_id    uuid        NOT NULL,
    account_id     uuid        NOT NULL,
    ordinal        bigint      NOT NULL,
    trip_id        uuid,
    entity_type    varchar(32) NOT NULL,
    entity_id      uuid        NOT NULL,
    entity_version bigint      NOT NULL,
    payload        jsonb       NOT NULL,
    CONSTRAINT snapshot_items_pk PRIMARY KEY (snapshot_id, ordinal),
    CONSTRAINT snapshot_items_entity_unique UNIQUE (snapshot_id, entity_type, entity_id),
    CONSTRAINT snapshot_items_snapshot_fk FOREIGN KEY (account_id, snapshot_id) REFERENCES data_snapshots (account_id, id) ON DELETE CASCADE,
    CONSTRAINT snapshot_items_ordinal_positive CHECK (ordinal > 0),
    CONSTRAINT snapshot_items_version_positive CHECK (entity_version > 0),
    CONSTRAINT snapshot_items_payload_object CHECK (jsonb_typeof(payload) = 'object')
);

CREATE INDEX snapshot_items_trip_idx ON snapshot_items (account_id, trip_id, snapshot_id);

CREATE TABLE snapshot_asset_refs (
    snapshot_id uuid NOT NULL,
    account_id  uuid NOT NULL,
    asset_id    uuid NOT NULL,
    CONSTRAINT snapshot_asset_refs_pk PRIMARY KEY (snapshot_id, asset_id),
    CONSTRAINT snapshot_asset_refs_snapshot_fk FOREIGN KEY (account_id, snapshot_id) REFERENCES data_snapshots (account_id, id) ON DELETE CASCADE,
    CONSTRAINT snapshot_asset_refs_asset_fk FOREIGN KEY (account_id, asset_id) REFERENCES assets (account_id, id)
);

CREATE INDEX snapshot_asset_refs_asset_idx ON snapshot_asset_refs (account_id, asset_id);

CREATE TABLE export_jobs (
    id                uuid PRIMARY KEY,
    account_id        uuid        NOT NULL REFERENCES accounts (id),
    format            varchar(24) NOT NULL,
    scope             varchar(16) NOT NULL,
    selected_trip_ids jsonb       NOT NULL,
    filters           jsonb       NOT NULL DEFAULT '{}'::jsonb,
    snapshot_id       uuid,
    status            varchar(16) NOT NULL DEFAULT 'queued',
    processed_items   bigint      NOT NULL DEFAULT 0,
    total_items       bigint,
    object_key        text,
    byte_size         bigint,
    sha256            bytea,
    error_code        varchar(64),
    captured_at       timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    finished_at       timestamptz,
    expires_at        timestamptz,
    CONSTRAINT export_jobs_account_id_unique UNIQUE (account_id, id),
    CONSTRAINT export_jobs_snapshot_fk FOREIGN KEY (account_id, snapshot_id) REFERENCES data_snapshots (account_id, id),
    CONSTRAINT export_jobs_object_key_unique UNIQUE (object_key),
    CONSTRAINT export_jobs_format_enum CHECK (format IN ('trip_archive', 'expense_csv')),
    CONSTRAINT export_jobs_scope_enum CHECK (scope IN ('all', 'selected')),
    CONSTRAINT export_jobs_selected_array CHECK (jsonb_typeof(selected_trip_ids) = 'array'),
    CONSTRAINT export_jobs_filters_object CHECK (jsonb_typeof(filters) = 'object'),
    CONSTRAINT export_jobs_status_enum CHECK (status IN ('queued', 'running', 'ready', 'failed', 'invalidated', 'expired')),
    CONSTRAINT export_jobs_processed_nonnegative CHECK (processed_items >= 0),
    CONSTRAINT export_jobs_total_nonnegative CHECK (total_items IS NULL OR total_items >= 0),
    CONSTRAINT export_jobs_byte_size_nonnegative CHECK (byte_size IS NULL OR byte_size >= 0),
    CONSTRAINT export_jobs_sha256_len CHECK (sha256 IS NULL OR octet_length(sha256) = 32)
);

CREATE INDEX export_jobs_account_created_idx ON export_jobs (account_id, created_at DESC, id DESC);
CREATE INDEX export_jobs_status_expires_idx ON export_jobs (status, expires_at);

CREATE TABLE deletion_jobs (
    id                 uuid PRIMARY KEY,
    owner_account_id   uuid        NOT NULL,
    scope              varchar(16) NOT NULL,
    target_trip_id     uuid,
    status             varchar(16) NOT NULL DEFAULT 'queued',
    stage              varchar(32) NOT NULL DEFAULT 'revoke_access',
    processed_items    bigint      NOT NULL DEFAULT 0,
    total_items        bigint,
    receipt_token_hash bytea,
    receipt_expires_at timestamptz,
    error_code         varchar(64),
    created_at         timestamptz NOT NULL DEFAULT now(),
    started_at         timestamptz,
    finished_at        timestamptz,
    retain_until       timestamptz,
    CONSTRAINT deletion_jobs_scope_enum CHECK (scope IN ('trip', 'account')),
    CONSTRAINT deletion_jobs_scope_target_pair CHECK ((scope = 'trip') = (target_trip_id IS NOT NULL)),
    CONSTRAINT deletion_jobs_status_enum CHECK (status IN ('queued', 'running', 'completed', 'failed')),
    CONSTRAINT deletion_jobs_stage_enum CHECK (stage IN ('revoke_access', 'remove_objects', 'remove_rows', 'finalize', 'done')),
    CONSTRAINT deletion_jobs_processed_nonnegative CHECK (processed_items >= 0),
    CONSTRAINT deletion_jobs_total_nonnegative CHECK (total_items IS NULL OR total_items >= 0),
    CONSTRAINT deletion_jobs_receipt_hash_len CHECK (receipt_token_hash IS NULL OR octet_length(receipt_token_hash) = 32)
);

CREATE UNIQUE INDEX deletion_jobs_active_trip_unique
    ON deletion_jobs (owner_account_id, target_trip_id)
    WHERE scope = 'trip' AND status IN ('queued', 'running', 'failed');
CREATE UNIQUE INDEX deletion_jobs_active_account_unique
    ON deletion_jobs (owner_account_id)
    WHERE scope = 'account' AND status IN ('queued', 'running', 'failed');
CREATE INDEX deletion_jobs_owner_created_idx ON deletion_jobs (owner_account_id, created_at DESC);
CREATE INDEX deletion_jobs_status_created_idx ON deletion_jobs (status, created_at);
CREATE INDEX deletion_jobs_retain_until_idx ON deletion_jobs (retain_until) WHERE retain_until IS NOT NULL;

-- +goose Down
DROP TABLE deletion_jobs;
DROP TABLE export_jobs;
DROP TABLE snapshot_asset_refs;
DROP TABLE snapshot_items;
DROP TABLE data_snapshots;
