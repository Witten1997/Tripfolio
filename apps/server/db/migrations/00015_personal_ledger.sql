-- +goose Up
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_split_mode_enum;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_split_mode_enum
    CHECK (split_mode IN ('even', 'ratio', 'personal'));

-- +goose Down
UPDATE ledger_entries SET split_mode = 'even' WHERE split_mode = 'personal';
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_split_mode_enum;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_split_mode_enum
    CHECK (split_mode IN ('even', 'ratio'));
