-- +goose Up
-- 同步日志、操作收据、墓碑。见数据库设计 v0.3 表 16、17、18。

CREATE TABLE sync_changes (
    account_id        uuid        NOT NULL REFERENCES accounts (id),
    seq               bigint      NOT NULL,
    batch_id          uuid        NOT NULL,
    batch_end_seq     bigint      NOT NULL,
    entity_type       varchar(32) NOT NULL,
    entity_id         uuid        NOT NULL,
    trip_id           uuid,
    entity_version    bigint      NOT NULL,
    change_kind       varchar(16) NOT NULL,
    schema_version    smallint    NOT NULL DEFAULT 1,
    snapshot          jsonb,
    changed_fields    text[],
    requires_snapshot boolean     NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sync_changes_pk PRIMARY KEY (account_id, seq),
    CONSTRAINT sync_changes_seq_positive CHECK (seq > 0),
    CONSTRAINT sync_changes_batch_end_ge_seq CHECK (batch_end_seq >= seq),
    CONSTRAINT sync_changes_entity_type_enum CHECK (entity_type IN (
        'expense_category', 'trip', 'itinerary_item', 'packing_item', 'todo',
        'ledger_entry', 'reservation', 'document', 'photo', 'asset'
    )),
    CONSTRAINT sync_changes_version_positive CHECK (entity_version > 0),
    CONSTRAINT sync_changes_kind_enum CHECK (change_kind IN ('upsert', 'delete', 'purge', 'redacted')),
    CONSTRAINT sync_changes_upsert_payload CHECK (
        (change_kind = 'upsert' AND jsonb_typeof(snapshot) = 'object' AND changed_fields IS NOT NULL)
        OR (change_kind <> 'upsert' AND snapshot IS NULL AND changed_fields IS NULL)
    ),
    CONSTRAINT sync_changes_account_scope CHECK ((entity_type = 'expense_category') = (trip_id IS NULL))
);

CREATE INDEX sync_changes_trip_seq_idx ON sync_changes (account_id, trip_id, seq);
CREATE INDEX sync_changes_created_idx ON sync_changes (created_at);
CREATE INDEX sync_changes_batch_idx ON sync_changes (account_id, batch_id);
CREATE INDEX sync_changes_entity_version_idx ON sync_changes (account_id, entity_type, entity_id, entity_version);

CREATE TABLE mutation_receipts (
    account_id     uuid        NOT NULL REFERENCES accounts (id),
    operation_id   uuid        NOT NULL,
    operation_type varchar(64) NOT NULL,
    request_hash   bytea       NOT NULL,
    result         jsonb       NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mutation_receipts_pk PRIMARY KEY (account_id, operation_id),
    CONSTRAINT mutation_receipts_hash_len CHECK (octet_length(request_hash) = 32),
    CONSTRAINT mutation_receipts_result_object CHECK (jsonb_typeof(result) = 'object')
);

CREATE TABLE entity_tombstones (
    account_id   uuid        NOT NULL REFERENCES accounts (id),
    entity_type  varchar(32) NOT NULL,
    entity_id    uuid        NOT NULL,
    trip_id      uuid,
    last_version bigint      NOT NULL,
    deleted_at   timestamptz NOT NULL,
    purged_at    timestamptz NOT NULL,
    CONSTRAINT entity_tombstones_pk PRIMARY KEY (account_id, entity_type, entity_id),
    CONSTRAINT entity_tombstones_version_positive CHECK (last_version > 0)
);

CREATE INDEX entity_tombstones_trip_idx ON entity_tombstones (account_id, trip_id);

-- +goose Down
DROP TABLE entity_tombstones;
DROP TABLE mutation_receipts;
DROP TABLE sync_changes;
