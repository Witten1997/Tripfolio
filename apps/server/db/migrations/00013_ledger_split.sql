-- +goose Up
ALTER TABLE ledger_entries
    ADD COLUMN split_count integer NOT NULL DEFAULT 1,
    ADD COLUMN personal_amount numeric(18,4) NOT NULL DEFAULT 0,
    ADD CONSTRAINT ledger_entries_split_count_range CHECK (split_count BETWEEN 1 AND 9999),
    ADD CONSTRAINT ledger_entries_refund_not_split CHECK (kind = 'expense' OR split_count = 1),
    ADD CONSTRAINT ledger_entries_personal_nonnegative CHECK (personal_amount >= 0);

UPDATE ledger_entries SET personal_amount = amount;

-- +goose Down
ALTER TABLE ledger_entries
    DROP CONSTRAINT ledger_entries_personal_nonnegative,
    DROP CONSTRAINT ledger_entries_refund_not_split,
    DROP CONSTRAINT ledger_entries_split_count_range,
    DROP COLUMN personal_amount,
    DROP COLUMN split_count;
