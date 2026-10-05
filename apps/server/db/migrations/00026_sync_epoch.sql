-- +goose Up
ALTER TABLE account_sync_state
    ADD COLUMN sync_epoch uuid NOT NULL DEFAULT gen_random_uuid();

-- +goose Down
ALTER TABLE account_sync_state DROP COLUMN sync_epoch;
