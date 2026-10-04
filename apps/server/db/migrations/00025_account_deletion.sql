-- +goose Up
ALTER TABLE deletion_jobs ADD COLUMN requested_via varchar(16) NOT NULL DEFAULT 'user'
    CHECK (requested_via IN ('user','admin'));
UPDATE deletion_jobs d SET requested_via='admin' WHERE EXISTS (
    SELECT 1 FROM admin_audit_events a WHERE a.action LIKE 'trip.%'
    AND a.details->>'job_id'=d.id::text AND a.actor_account_id IS NOT NULL);
ALTER TABLE deletion_jobs ADD CONSTRAINT deletion_jobs_owner_unique UNIQUE(id,owner_account_id);
-- Reuse the durable object manifest for account cleanup, whose trip_id is NULL.
ALTER TABLE trip_purge_objects ALTER COLUMN trip_id DROP NOT NULL;
ALTER TABLE trip_purge_objects ADD CONSTRAINT trip_purge_objects_owner_fk
    FOREIGN KEY(job_id,account_id) REFERENCES deletion_jobs(id,owner_account_id);

-- Only the explicitly authenticated requesting session survives as a restricted recovery channel.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION revoke_account_access_on_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IS DISTINCT FROM NEW.status THEN
        UPDATE account_sessions SET revoked_at=clock_timestamp()
        WHERE account_id=NEW.id AND revoked_at IS NULL
          AND NOT (NEW.status='deleting' AND id::text=coalesce(current_setting('tripfolio.deletion_session',true),''));
        UPDATE admin_sessions SET revoked_at=clock_timestamp() WHERE account_id=NEW.id AND revoked_at IS NULL;
        IF OLD.status='banned' OR NEW.status='banned' THEN
            DELETE FROM trip_shares WHERE account_id=NEW.id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION revoke_account_access_on_status() RETURNS trigger LANGUAGE plpgsql AS $$
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
-- Reject downgrade while account object manifests still exist.
ALTER TABLE trip_purge_objects ALTER COLUMN trip_id SET NOT NULL;
ALTER TABLE trip_purge_objects DROP CONSTRAINT trip_purge_objects_owner_fk;
ALTER TABLE deletion_jobs DROP CONSTRAINT deletion_jobs_owner_unique;
ALTER TABLE deletion_jobs DROP COLUMN requested_via;
