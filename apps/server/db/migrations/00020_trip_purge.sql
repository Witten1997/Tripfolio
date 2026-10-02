-- +goose Up
ALTER TABLE deletion_jobs ADD CONSTRAINT deletion_jobs_target_unique UNIQUE(id,owner_account_id,target_trip_id);
CREATE TABLE trip_purge_objects (
    job_id uuid NOT NULL,
    account_id uuid NOT NULL,
    trip_id uuid NOT NULL,
    object_key text NOT NULL,
    removed_at timestamptz,
    PRIMARY KEY(job_id,object_key),
    CONSTRAINT trip_purge_objects_target_fk FOREIGN KEY(job_id,account_id,trip_id)
        REFERENCES deletion_jobs(id,owner_account_id,target_trip_id),
    CONSTRAINT trip_purge_objects_key_not_blank CHECK(btrim(object_key)<>'')
);
CREATE INDEX trip_purge_objects_pending ON trip_purge_objects(job_id) WHERE removed_at IS NULL;

-- +goose Down
DROP TABLE trip_purge_objects;
ALTER TABLE deletion_jobs DROP CONSTRAINT deletion_jobs_target_unique;
