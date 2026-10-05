-- +goose Up
CREATE TABLE account_sync_capabilities (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    collection_guards_required boolean NOT NULL DEFAULT false,
    v2_enabled_epoch uuid,
    enabled_at timestamptz,
    CONSTRAINT sync_capabilities_activation_pair CHECK ((v2_enabled_epoch IS NULL) = (enabled_at IS NULL)),
    CONSTRAINT sync_capabilities_activation_guard CHECK (v2_enabled_epoch IS NULL OR collection_guards_required),
    CONSTRAINT sync_capabilities_epoch_nonzero CHECK (v2_enabled_epoch IS NULL OR v2_enabled_epoch <> '00000000-0000-0000-0000-000000000000'::uuid)
);

-- +goose Down
-- +goose StatementBegin
DO $migration$
BEGIN
    IF EXISTS (SELECT 1 FROM account_sync_capabilities WHERE collection_guards_required OR v2_enabled_epoch IS NOT NULL) THEN
        RAISE EXCEPTION 'SYNC_GUARD_DOWNGRADE_FORBIDDEN';
    END IF;
END $migration$;
-- +goose StatementEnd
DROP TABLE account_sync_capabilities;
