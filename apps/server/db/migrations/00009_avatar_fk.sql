-- +goose Up
-- 头像外键：assets 建表后补建。列子集 SET NULL（PostgreSQL 15+）只置空 avatar_asset_id，不动 id。
ALTER TABLE accounts
    ADD CONSTRAINT accounts_avatar_asset_fk
    FOREIGN KEY (id, avatar_asset_id) REFERENCES assets (account_id, id)
    ON DELETE SET NULL (avatar_asset_id);

CREATE INDEX accounts_avatar_asset_idx ON accounts (avatar_asset_id) WHERE avatar_asset_id IS NOT NULL;

-- +goose Down
DROP INDEX accounts_avatar_asset_idx;
ALTER TABLE accounts DROP CONSTRAINT accounts_avatar_asset_fk;
