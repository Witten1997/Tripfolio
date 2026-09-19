-- +goose Up
-- 旅行成员与账目分摊。见数据库设计 v0.6 表 9、25、26 与 docs/superpowers/plans/2026-09-19-成员与分摊.md。
-- 成员进入同步流（trip_member）；分摊份额随账目的 splits 字段同步，不单独记日志。

CREATE TABLE trip_members (
    id            uuid PRIMARY KEY,
    account_id    uuid          NOT NULL REFERENCES accounts (id),
    version       bigint        NOT NULL DEFAULT 1,
    created_at    timestamptz   NOT NULL DEFAULT now(),
    updated_at    timestamptz   NOT NULL DEFAULT now(),
    deleted_at    timestamptz,
    trip_id       uuid          NOT NULL,
    name          varchar(30)   NOT NULL,
    share_percent numeric(5,2)  NOT NULL,
    sort_order    integer       NOT NULL DEFAULT 0,
    is_self       boolean       NOT NULL DEFAULT false,
    CONSTRAINT trip_members_account_trip_id_unique UNIQUE (account_id, trip_id, id),
    CONSTRAINT trip_members_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id),
    CONSTRAINT trip_members_version_positive CHECK (version > 0),
    CONSTRAINT trip_members_name_length CHECK (char_length(name) BETWEEN 1 AND 30 AND btrim(name) <> ''),
    CONSTRAINT trip_members_share_percent_range CHECK (share_percent >= 0 AND share_percent <= 100),
    CONSTRAINT trip_members_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX trip_members_trip_name_unique
    ON trip_members (account_id, trip_id, lower(btrim(name))) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX trip_members_trip_self_unique
    ON trip_members (account_id, trip_id) WHERE is_self AND deleted_at IS NULL;
CREATE INDEX trip_members_trip_order_idx
    ON trip_members (account_id, trip_id, sort_order, id) WHERE deleted_at IS NULL;

-- 存量旅行补齐「我」（含回收站中的旅行，恢复后仍可用）。
INSERT INTO trip_members (id, account_id, trip_id, name, share_percent, sort_order, is_self, created_at, updated_at)
SELECT gen_random_uuid(), t.account_id, t.id, '我', 100, 0, true, t.created_at, t.created_at
FROM trips t;

ALTER TABLE ledger_entries
    ADD COLUMN payer_member_id uuid,
    ADD COLUMN split_mode      varchar(8) NOT NULL DEFAULT 'even',
    ADD CONSTRAINT ledger_entries_split_mode_enum CHECK (split_mode IN ('even', 'ratio'));

UPDATE ledger_entries l
SET payer_member_id = m.id
FROM trip_members m
WHERE m.account_id = l.account_id AND m.trip_id = l.trip_id AND m.is_self AND m.deleted_at IS NULL;

ALTER TABLE ledger_entries
    ALTER COLUMN payer_member_id SET NOT NULL,
    ADD CONSTRAINT ledger_entries_payer_fk FOREIGN KEY (account_id, trip_id, payer_member_id) REFERENCES trip_members (account_id, trip_id, id),
    DROP CONSTRAINT ledger_entries_refund_not_split;

CREATE INDEX ledger_entries_payer_idx ON ledger_entries (account_id, trip_id, payer_member_id) WHERE deleted_at IS NULL;

CREATE TABLE ledger_entry_splits (
    account_id      uuid          NOT NULL,
    trip_id         uuid          NOT NULL,
    ledger_entry_id uuid          NOT NULL,
    member_id       uuid          NOT NULL,
    amount          numeric(18,4) NOT NULL,
    sort_order      integer       NOT NULL DEFAULT 0,
    CONSTRAINT ledger_entry_splits_pk PRIMARY KEY (account_id, trip_id, ledger_entry_id, member_id),
    CONSTRAINT ledger_entry_splits_entry_fk FOREIGN KEY (account_id, trip_id, ledger_entry_id) REFERENCES ledger_entries (account_id, trip_id, id),
    CONSTRAINT ledger_entry_splits_member_fk FOREIGN KEY (account_id, trip_id, member_id) REFERENCES trip_members (account_id, trip_id, id),
    CONSTRAINT ledger_entry_splits_amount_nonnegative CHECK (amount >= 0),
    CONSTRAINT ledger_entry_splits_sort_nonnegative CHECK (sort_order >= 0)
);

CREATE INDEX ledger_entry_splits_member_idx ON ledger_entry_splits (account_id, trip_id, member_id);

-- 存量账目：「我」的份额沿用 personal_amount；均摊给匿名他人的部分不归属任何成员。
INSERT INTO ledger_entry_splits (account_id, trip_id, ledger_entry_id, member_id, amount, sort_order)
SELECT l.account_id, l.trip_id, l.id, l.payer_member_id, l.personal_amount, 0
FROM ledger_entries l;

ALTER TABLE sync_changes DROP CONSTRAINT sync_changes_entity_type_enum;
ALTER TABLE sync_changes ADD CONSTRAINT sync_changes_entity_type_enum CHECK (entity_type IN (
    'expense_category', 'trip', 'trip_member', 'itinerary_item', 'packing_item', 'todo',
    'ledger_entry', 'reservation', 'document', 'photo', 'asset'
));

-- +goose Down
ALTER TABLE sync_changes DROP CONSTRAINT sync_changes_entity_type_enum;
ALTER TABLE sync_changes ADD CONSTRAINT sync_changes_entity_type_enum CHECK (entity_type IN (
    'expense_category', 'trip', 'itinerary_item', 'packing_item', 'todo',
    'ledger_entry', 'reservation', 'document', 'photo', 'asset'
));
DROP TABLE ledger_entry_splits;
DROP INDEX ledger_entries_payer_idx;
ALTER TABLE ledger_entries
    ADD CONSTRAINT ledger_entries_refund_not_split CHECK (kind = 'expense' OR split_count = 1),
    DROP CONSTRAINT ledger_entries_payer_fk,
    DROP CONSTRAINT ledger_entries_split_mode_enum,
    DROP COLUMN split_mode,
    DROP COLUMN payer_member_id;
DROP TABLE trip_members;
