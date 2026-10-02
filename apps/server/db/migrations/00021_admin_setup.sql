-- +goose Up
SELECT pg_advisory_xact_lock(781241,18);
CREATE TABLE admin_setup (
    id integer PRIMARY KEY CHECK (id=1),
    completed_at timestamptz
);
INSERT INTO admin_setup (id,completed_at)
SELECT 1, CASE WHEN EXISTS (SELECT 1 FROM admin_principals)
    OR EXISTS (SELECT 1 FROM admin_audit_events WHERE action IN ('principal.grant','setup.initialize') AND result='success')
    THEN now() END;

-- 完成标记独立于账号和资格保存，不因撤权或账号变更重新开放。
-- +goose StatementBegin
CREATE FUNCTION protect_admin_setup_completion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='DELETE' OR (OLD.completed_at IS NOT NULL AND NEW.completed_at IS DISTINCT FROM OLD.completed_at) THEN
        RAISE EXCEPTION '后台初始化状态不可重置' USING ERRCODE='P0021';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER admin_setup_completion_guard BEFORE UPDATE OR DELETE ON admin_setup
FOR EACH ROW EXECUTE FUNCTION protect_admin_setup_completion();

-- CLI 或其他受控方式首次授予资格，也在同一事务内关闭初始化。
-- +goose StatementBegin
CREATE FUNCTION close_admin_setup_on_grant() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.revoked_at IS NULL THEN
        UPDATE admin_setup SET completed_at=clock_timestamp() WHERE id=1 AND completed_at IS NULL;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER principals_close_setup AFTER INSERT OR UPDATE ON admin_principals
FOR EACH ROW EXECUTE FUNCTION close_admin_setup_on_grant();

-- +goose Down
DROP TRIGGER principals_close_setup ON admin_principals;
DROP FUNCTION close_admin_setup_on_grant();
DROP TABLE admin_setup;
DROP FUNCTION protect_admin_setup_completion();
