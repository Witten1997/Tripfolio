-- +goose Up
ALTER TABLE data_snapshots ADD COLUMN sync_epoch uuid;
ALTER TABLE data_snapshots ADD COLUMN capture_generation bigint NOT NULL DEFAULT 0 CHECK (capture_generation >= 0);
UPDATE data_snapshots s SET sync_epoch=a.sync_epoch,status='invalidated' FROM account_sync_state a WHERE a.account_id=s.account_id;
ALTER TABLE data_snapshots ALTER COLUMN sync_epoch SET NOT NULL;

ALTER TABLE packing_items ADD COLUMN sort_order integer;
WITH positions AS (
 SELECT id,(row_number() OVER (PARTITION BY account_id,trip_id ORDER BY category,created_at,id)-1)::integer AS sort_order FROM packing_items
) UPDATE packing_items p SET sort_order=v.sort_order FROM positions v WHERE v.id=p.id;
ALTER TABLE packing_items ALTER COLUMN sort_order SET DEFAULT 0;
ALTER TABLE packing_items ALTER COLUMN sort_order SET NOT NULL;

ALTER TABLE todo_items ADD COLUMN sort_order integer;
WITH positions AS (
 SELECT id,(row_number() OVER (PARTITION BY account_id,trip_id ORDER BY COALESCE(due_on,DATE '9999-12-31'),id)-1)::integer AS sort_order FROM todo_items
) UPDATE todo_items t SET sort_order=v.sort_order FROM positions v WHERE v.id=t.id;
ALTER TABLE todo_items ALTER COLUMN sort_order SET DEFAULT 0;
ALTER TABLE todo_items ALTER COLUMN sort_order SET NOT NULL;

-- +goose Down
ALTER TABLE todo_items DROP COLUMN sort_order;
ALTER TABLE packing_items DROP COLUMN sort_order;
ALTER TABLE data_snapshots DROP COLUMN capture_generation;
ALTER TABLE data_snapshots DROP COLUMN sync_epoch;
