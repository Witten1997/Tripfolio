-- +goose Up
-- 旅行与账号级账单分类。见数据库设计 v0.3 表 4、表 8。

CREATE TABLE trips (
    id                 uuid PRIMARY KEY,
    account_id         uuid          NOT NULL REFERENCES accounts (id),
    version            bigint        NOT NULL DEFAULT 1,
    created_at         timestamptz   NOT NULL DEFAULT now(),
    updated_at         timestamptz   NOT NULL DEFAULT now(),
    deleted_at         timestamptz,
    name               varchar(120)  NOT NULL,
    start_date         date          NOT NULL,
    end_date           date          NOT NULL,
    destination        varchar(300)  NOT NULL DEFAULT '',
    notes              text          NOT NULL DEFAULT '',
    timezone           varchar(64)   NOT NULL,
    currency_code      varchar(3)    NOT NULL DEFAULT 'CNY',
    currency_locked_at timestamptz,
    budget_amount      numeric(18,4),
    archived_at        timestamptz,
    purge_after_at     timestamptz,
    purge_requested_at timestamptz,
    CONSTRAINT trips_account_id_unique UNIQUE (account_id, id),
    CONSTRAINT trips_version_positive CHECK (version > 0),
    CONSTRAINT trips_name_length CHECK (char_length(name) BETWEEN 1 AND 120 AND btrim(name) <> ''),
    CONSTRAINT trips_dates_order CHECK (end_date >= start_date),
    CONSTRAINT trips_notes_length CHECK (char_length(notes) <= 10000),
    CONSTRAINT trips_currency_format CHECK (currency_code ~ '^[A-Z]{3}$'),
    CONSTRAINT trips_budget_nonnegative CHECK (budget_amount IS NULL OR budget_amount >= 0),
    CONSTRAINT trips_delete_purge_pair CHECK ((deleted_at IS NULL) = (purge_after_at IS NULL)),
    CONSTRAINT trips_purge_requires_deleted CHECK (purge_requested_at IS NULL OR deleted_at IS NOT NULL),
    CONSTRAINT trips_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE INDEX trips_account_start_idx ON trips (account_id, start_date DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX trips_account_updated_idx ON trips (account_id, updated_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX trips_recycle_bin_idx ON trips (account_id, deleted_at DESC, id DESC) WHERE deleted_at IS NOT NULL;
CREATE INDEX trips_purge_after_idx ON trips (purge_after_at) WHERE purge_after_at IS NOT NULL;

CREATE TABLE expense_categories (
    id         uuid PRIMARY KEY,
    account_id uuid        NOT NULL REFERENCES accounts (id),
    version    bigint      NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    name       varchar(40) NOT NULL,
    icon       varchar(32),
    sort_order integer     NOT NULL DEFAULT 0,
    is_preset  boolean     NOT NULL DEFAULT false,
    CONSTRAINT expense_categories_account_id_unique UNIQUE (account_id, id),
    CONSTRAINT expense_categories_version_positive CHECK (version > 0),
    CONSTRAINT expense_categories_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT expense_categories_sort_nonnegative CHECK (sort_order >= 0),
    CONSTRAINT expense_categories_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX expense_categories_account_name_unique
    ON expense_categories (account_id, lower(btrim(name))) WHERE deleted_at IS NULL;
CREATE INDEX expense_categories_account_sort_idx
    ON expense_categories (account_id, sort_order, id) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE expense_categories;
DROP TABLE trips;
