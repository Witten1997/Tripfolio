-- +goose Up
-- 账目、票据引用、资料、照片。见数据库设计 v0.3 表 9、10、12、13。

CREATE TABLE ledger_entries (
    id                uuid PRIMARY KEY,
    account_id        uuid          NOT NULL REFERENCES accounts (id),
    version           bigint        NOT NULL DEFAULT 1,
    created_at        timestamptz   NOT NULL DEFAULT now(),
    updated_at        timestamptz   NOT NULL DEFAULT now(),
    deleted_at        timestamptz,
    trip_id           uuid          NOT NULL,
    kind              varchar(16)   NOT NULL,
    amount            numeric(18,4) NOT NULL,
    category_id       uuid          NOT NULL,
    occurred_on       date          NOT NULL,
    notes             varchar(4000) NOT NULL DEFAULT '',
    refunded_entry_id uuid,
    CONSTRAINT ledger_entries_account_trip_id_unique UNIQUE (account_id, trip_id, id),
    CONSTRAINT ledger_entries_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id),
    CONSTRAINT ledger_entries_category_fk FOREIGN KEY (account_id, category_id) REFERENCES expense_categories (account_id, id),
    CONSTRAINT ledger_entries_refunded_fk FOREIGN KEY (account_id, trip_id, refunded_entry_id) REFERENCES ledger_entries (account_id, trip_id, id),
    CONSTRAINT ledger_entries_version_positive CHECK (version > 0),
    CONSTRAINT ledger_entries_kind_enum CHECK (kind IN ('expense', 'refund')),
    CONSTRAINT ledger_entries_amount_positive CHECK (amount > 0),
    CONSTRAINT ledger_entries_refund_only_links CHECK (kind = 'refund' OR refunded_entry_id IS NULL),
    CONSTRAINT ledger_entries_no_self_refund CHECK (refunded_entry_id IS NULL OR refunded_entry_id <> id),
    CONSTRAINT ledger_entries_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE INDEX ledger_entries_trip_occurred_idx
    ON ledger_entries (account_id, trip_id, occurred_on DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX ledger_entries_trip_category_occurred_idx
    ON ledger_entries (account_id, trip_id, category_id, occurred_on DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX ledger_entries_category_idx
    ON ledger_entries (account_id, category_id) WHERE deleted_at IS NULL;
CREATE INDEX ledger_entries_refunded_idx
    ON ledger_entries (account_id, trip_id, refunded_entry_id) WHERE refunded_entry_id IS NOT NULL;

CREATE TABLE ledger_attachments (
    account_id      uuid        NOT NULL,
    trip_id         uuid        NOT NULL,
    ledger_entry_id uuid        NOT NULL,
    asset_id        uuid        NOT NULL,
    sort_order      integer     NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ledger_attachments_pk PRIMARY KEY (account_id, trip_id, ledger_entry_id, asset_id),
    CONSTRAINT ledger_attachments_entry_fk FOREIGN KEY (account_id, trip_id, ledger_entry_id) REFERENCES ledger_entries (account_id, trip_id, id),
    CONSTRAINT ledger_attachments_asset_fk FOREIGN KEY (account_id, trip_id, asset_id) REFERENCES assets (account_id, trip_id, id),
    CONSTRAINT ledger_attachments_sort_nonnegative CHECK (sort_order >= 0)
);

CREATE INDEX ledger_attachments_asset_idx ON ledger_attachments (account_id, asset_id);

CREATE TABLE documents (
    id             uuid PRIMARY KEY,
    account_id     uuid          NOT NULL REFERENCES accounts (id),
    version        bigint        NOT NULL DEFAULT 1,
    created_at     timestamptz   NOT NULL DEFAULT now(),
    updated_at     timestamptz   NOT NULL DEFAULT now(),
    deleted_at     timestamptz,
    trip_id        uuid          NOT NULL,
    title          varchar(200)  NOT NULL,
    notes          varchar(4000) NOT NULL DEFAULT '',
    asset_id       uuid          NOT NULL,
    reservation_id uuid,
    CONSTRAINT documents_account_trip_id_unique UNIQUE (account_id, trip_id, id),
    CONSTRAINT documents_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id),
    CONSTRAINT documents_asset_fk FOREIGN KEY (account_id, trip_id, asset_id) REFERENCES assets (account_id, trip_id, id),
    CONSTRAINT documents_reservation_fk FOREIGN KEY (account_id, trip_id, reservation_id) REFERENCES reservations (account_id, trip_id, id),
    CONSTRAINT documents_version_positive CHECK (version > 0),
    CONSTRAINT documents_title_length CHECK (char_length(title) BETWEEN 1 AND 200 AND btrim(title) <> ''),
    CONSTRAINT documents_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE INDEX documents_trip_created_idx ON documents (account_id, trip_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX documents_asset_idx ON documents (account_id, asset_id);
CREATE INDEX documents_reservation_idx ON documents (account_id, trip_id, reservation_id) WHERE reservation_id IS NOT NULL;

CREATE TABLE photos (
    id             uuid PRIMARY KEY,
    account_id     uuid          NOT NULL REFERENCES accounts (id),
    version        bigint        NOT NULL DEFAULT 1,
    created_at     timestamptz   NOT NULL DEFAULT now(),
    updated_at     timestamptz   NOT NULL DEFAULT now(),
    deleted_at     timestamptz,
    trip_id        uuid          NOT NULL,
    asset_id       uuid          NOT NULL,
    taken_at_local timestamp(0) without time zone,
    recorded_on    date          NOT NULL,
    caption        varchar(2000) NOT NULL DEFAULT '',
    sort_order     integer       NOT NULL DEFAULT 0,
    place_name     varchar(200)  NOT NULL DEFAULT '',
    address        varchar(500)  NOT NULL DEFAULT '',
    latitude       numeric(9,6),
    longitude      numeric(10,6),
    CONSTRAINT photos_account_trip_id_unique UNIQUE (account_id, trip_id, id),
    CONSTRAINT photos_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id),
    CONSTRAINT photos_asset_fk FOREIGN KEY (account_id, trip_id, asset_id) REFERENCES assets (account_id, trip_id, id),
    CONSTRAINT photos_version_positive CHECK (version > 0),
    CONSTRAINT photos_sort_nonnegative CHECK (sort_order >= 0),
    CONSTRAINT photos_coordinates_pair CHECK ((latitude IS NULL) = (longitude IS NULL)),
    CONSTRAINT photos_latitude_range CHECK (latitude IS NULL OR latitude BETWEEN -90 AND 90),
    CONSTRAINT photos_longitude_range CHECK (longitude IS NULL OR longitude BETWEEN -180 AND 180),
    CONSTRAINT photos_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE INDEX photos_trip_day_idx
    ON photos (account_id, trip_id, recorded_on DESC, (COALESCE(taken_at_local, TIMESTAMP '9999-12-31 23:59:59')), sort_order, id)
    WHERE deleted_at IS NULL;
CREATE INDEX photos_asset_idx ON photos (account_id, asset_id);

-- +goose Down
DROP TABLE photos;
DROP TABLE documents;
DROP TABLE ledger_attachments;
DROP TABLE ledger_entries;
