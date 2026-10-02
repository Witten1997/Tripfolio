-- +goose Up
ALTER TABLE accounts DROP CONSTRAINT accounts_status_enum;
ALTER TABLE accounts ADD CONSTRAINT accounts_status_enum CHECK (status IN ('active','banned','deleting'));

CREATE TABLE trip_sharing_restrictions (
    account_id uuid NOT NULL,
    trip_id uuid NOT NULL,
    restricted boolean NOT NULL,
    reason varchar(500) NOT NULL CHECK (btrim(reason) <> ''),
    changed_by uuid NOT NULL,
    changed_at timestamptz NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    PRIMARY KEY (account_id,trip_id),
    FOREIGN KEY (account_id,trip_id) REFERENCES trips(account_id,id) ON DELETE CASCADE
);

-- 写入守卫行使 REPEATABLE READ 的过期快照也以序列化失败结束。
CREATE TABLE admin_availability_guard (id integer PRIMARY KEY CHECK (id=1), version bigint NOT NULL);
INSERT INTO admin_availability_guard VALUES (1,1);
-- +goose StatementBegin
CREATE FUNCTION lock_admin_availability() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(781241,18);
    UPDATE admin_availability_guard SET version=version+1 WHERE id=1;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER accounts_availability_lock BEFORE UPDATE OF status OR DELETE ON accounts
FOR EACH STATEMENT EXECUTE FUNCTION lock_admin_availability();
CREATE TRIGGER principals_availability_lock BEFORE INSERT OR UPDATE OR DELETE ON admin_principals
FOR EACH STATEMENT EXECUTE FUNCTION lock_admin_availability();

-- +goose StatementBegin
CREATE FUNCTION protect_last_admin_account() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status='active' AND (TG_OP='DELETE' OR NEW.status<>'active')
       AND EXISTS (SELECT 1 FROM admin_principals WHERE account_id=OLD.id AND revoked_at IS NULL)
       AND NOT EXISTS (SELECT 1 FROM admin_principals p JOIN accounts a ON a.id=p.account_id
                       WHERE p.revoked_at IS NULL AND a.status='active' AND a.id<>OLD.id) THEN
        RAISE EXCEPTION '不能封禁或注销最后一个可用超级管理员' USING ERRCODE='P0019';
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER accounts_last_admin BEFORE UPDATE OF status OR DELETE ON accounts
FOR EACH ROW EXECUTE FUNCTION protect_last_admin_account();

-- +goose StatementBegin
CREATE FUNCTION protect_last_admin_principal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.revoked_at IS NULL AND (TG_OP='DELETE' OR NEW.revoked_at IS NOT NULL)
       AND EXISTS (SELECT 1 FROM accounts WHERE id=OLD.account_id AND status='active')
       AND NOT EXISTS (SELECT 1 FROM admin_principals p JOIN accounts a ON a.id=p.account_id
                       WHERE p.revoked_at IS NULL AND a.status='active' AND a.id<>OLD.account_id) THEN
        RAISE EXCEPTION '不能撤销最后一个可用超级管理员' USING ERRCODE='P0019';
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER principals_last_admin BEFORE UPDATE OR DELETE ON admin_principals
FOR EACH ROW EXECUTE FUNCTION protect_last_admin_principal();

-- 状态变更撤销旧会话；封禁和解封同时删除分享，旧凭证永不复活。
-- +goose StatementBegin
CREATE FUNCTION revoke_account_access_on_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IS DISTINCT FROM NEW.status THEN
        UPDATE account_sessions SET revoked_at=clock_timestamp() WHERE account_id=NEW.id AND revoked_at IS NULL;
        UPDATE admin_sessions SET revoked_at=clock_timestamp() WHERE account_id=NEW.id AND revoked_at IS NULL;
        IF OLD.status='banned' OR NEW.status='banned' THEN
            DELETE FROM trip_shares WHERE account_id=NEW.id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER accounts_revoke_access AFTER UPDATE OF status ON accounts
FOR EACH ROW EXECUTE FUNCTION revoke_account_access_on_status();

-- +goose Down
DROP TRIGGER accounts_revoke_access ON accounts;
DROP FUNCTION revoke_account_access_on_status();
DROP TRIGGER principals_last_admin ON admin_principals;
DROP FUNCTION protect_last_admin_principal();
DROP TRIGGER accounts_last_admin ON accounts;
DROP FUNCTION protect_last_admin_account();
DROP TRIGGER principals_availability_lock ON admin_principals;
DROP TRIGGER accounts_availability_lock ON accounts;
DROP FUNCTION lock_admin_availability();
DROP TABLE admin_availability_guard;
DROP TABLE trip_sharing_restrictions;
-- 存在封禁账号时拒绝降级，避免静默解封。
ALTER TABLE accounts DROP CONSTRAINT accounts_status_enum;
ALTER TABLE accounts ADD CONSTRAINT accounts_status_enum CHECK (status IN ('active','deleting'));
