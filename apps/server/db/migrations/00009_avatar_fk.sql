-- +goose Up
-- 头像外键：assets 建表后补建。复合外键 (id, avatar_asset_id) → assets(account_id, id) 保证头像与账号同属。
-- 删除资产时要「只置空 accounts.avatar_asset_id、不动 accounts.id」：列子集
-- ON DELETE SET NULL (列) 需要 PostgreSQL 15+，自托管环境常见更老的版本（如 13/14），
-- 因此改用 BEFORE DELETE 触发器清理引用，外键保持默认 NO ACTION——
-- 触发器先清空指针，删除资产才不会被外键挡住；万一触发器缺失，删除会被外键拒绝，不会留下悬挂引用。
-- +goose StatementBegin
CREATE FUNCTION accounts_clear_avatar_reference() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    UPDATE accounts
       SET avatar_asset_id = NULL
     WHERE id = OLD.account_id
       AND avatar_asset_id = OLD.id;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER assets_clear_avatar_reference
    BEFORE DELETE ON assets
    FOR EACH ROW
    EXECUTE FUNCTION accounts_clear_avatar_reference();

ALTER TABLE accounts
    ADD CONSTRAINT accounts_avatar_asset_fk
    FOREIGN KEY (id, avatar_asset_id) REFERENCES assets (account_id, id);

CREATE INDEX accounts_avatar_asset_idx ON accounts (avatar_asset_id) WHERE avatar_asset_id IS NOT NULL;

-- +goose Down
DROP INDEX accounts_avatar_asset_idx;
ALTER TABLE accounts DROP CONSTRAINT accounts_avatar_asset_fk;
DROP TRIGGER assets_clear_avatar_reference ON assets;
DROP FUNCTION accounts_clear_avatar_reference();
