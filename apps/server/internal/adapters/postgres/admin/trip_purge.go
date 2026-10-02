package adminpg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/modules/assets"
	"tripfolio/server/internal/modules/travel/trip"
)

type TripPurgeStore struct{ pool *pgxpool.Pool }

func NewTripPurgeStore(pool *pgxpool.Pool) *TripPurgeStore { return &TripPurgeStore{pool: pool} }

type tripPurgeRun struct {
	conn     *pgxpool.Conn
	release  func()
	args     trip.PurgeJobArgs
	prefixes []string
	explicit map[string]bool
}

func (s *TripPurgeStore) Acquire(ctx context.Context, a trip.PurgeJobArgs) (trip.PurgeRun, error) {
	var valid bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deletion_jobs WHERE id=$1 AND owner_account_id=$2 AND target_trip_id=$3 AND scope='trip')`, a.JobID, a.AccountID, a.TripID).Scan(&valid)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, &trip.PurgeError{Code: "PURGE_SCOPE_MISMATCH"}
	}
	conn, release, err := pgcore.LockTripObjects(ctx, s.pool, a.AccountID, a.TripID)
	if err != nil {
		return nil, err
	}
	return &tripPurgeRun{conn: conn, release: release, args: a, explicit: map[string]bool{}}, nil
}
func (r *tripPurgeRun) Close() { r.release() }
func (r *tripPurgeRun) audit(ctx context.Context, tx pgx.Tx, action, result string) error {
	a := admin.Audit{ID: uuid.New(), SubjectID: &r.args.AccountID, ResourceType: "trip", ResourceID: &r.args.TripID, Action: action, Result: result, OccurredAt: time.Now().UTC(), Details: map[string]any{"job_id": r.args.JobID, "executor": "worker"}}
	if err := insertAudit(ctx, tx, a); err != nil {
		return &trip.PurgeError{Code: "ADMIN_AUDIT_UNAVAILABLE", Cause: err}
	}
	return nil
}

const relatedSnapshots = `SELECT s.id FROM data_snapshots s WHERE s.account_id=$1 AND
 (s.selected_trip_ids @> jsonb_build_array($2::uuid::text)
 OR EXISTS(SELECT 1 FROM snapshot_items i WHERE i.account_id=$1 AND i.snapshot_id=s.id AND (i.trip_id=$2 OR (i.entity_type='trip' AND i.entity_id=$2) OR i.payload->>'trip_id'=$2::uuid::text))
 OR EXISTS(SELECT 1 FROM snapshot_asset_refs f JOIN assets a ON a.account_id=f.account_id AND a.id=f.asset_id WHERE f.account_id=$1 AND f.snapshot_id=s.id AND a.trip_id=$2))`
const relatedExports = `SELECT e.id FROM export_jobs e WHERE e.account_id=$1 AND
 (e.selected_trip_ids @> jsonb_build_array($2::uuid::text) OR e.snapshot_id IN (` + relatedSnapshots + `))`

func (r *tripPurgeRun) lock(ctx context.Context, tx pgx.Tx) (int64, error) {
	var owner uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, r.args.AccountID).Scan(&owner); err != nil {
		return 0, err
	}
	var seq int64
	err := tx.QueryRow(ctx, `SELECT last_seq FROM account_sync_state WHERE account_id=$1 FOR UPDATE`, owner).Scan(&seq)
	return seq, err
}

func (r *tripPurgeRun) Prepare(ctx context.Context) (trip.PurgePlan, error) {
	plan := trip.PurgePlan{}
	tx, err := r.conn.Begin(ctx)
	if err != nil {
		return plan, err
	}
	defer tx.Rollback(ctx)
	if _, err = r.lock(ctx, tx); err != nil {
		return plan, err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM deletion_jobs WHERE id=$1 AND owner_account_id=$2 AND target_trip_id=$3 AND scope='trip' FOR UPDATE`, r.args.JobID, r.args.AccountID, r.args.TripID).Scan(&status); err != nil {
		return plan, err
	}
	if status == "completed" {
		plan.Completed = true
		return plan, tx.Commit(ctx)
	}
	if status == "failed" {
		return plan, &trip.PurgeError{Code: "PURGE_RETRY_REQUIRED"}
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL AND purge_requested_at IS NOT NULL FROM trips WHERE account_id=$1 AND id=$2 FOR UPDATE`, r.args.AccountID, r.args.TripID).Scan(&valid); err != nil {
		return plan, err
	}
	if !valid {
		return plan, &trip.PurgeError{Code: "PURGE_SCOPE_MISMATCH"}
	}
	if _, err = tx.Exec(ctx, `UPDATE deletion_jobs SET status='running',stage='revoke_access',started_at=coalesce(started_at,now()),error_code=NULL WHERE id=$1`, r.args.JobID); err != nil {
		return plan, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM trip_shares WHERE account_id=$1 AND trip_id=$2`, r.args.AccountID, r.args.TripID); err != nil {
		return plan, err
	}
	if _, err = tx.Exec(ctx, `UPDATE data_snapshots SET status='invalidated' WHERE account_id=$1 AND id IN (`+relatedSnapshots+`)`, r.args.AccountID, r.args.TripID); err != nil {
		return plan, err
	}
	if _, err = tx.Exec(ctx, `UPDATE export_jobs SET status='invalidated' WHERE account_id=$1 AND id IN (`+relatedExports+`)`, r.args.AccountID, r.args.TripID); err != nil {
		return plan, err
	}
	rows, err := tx.Query(ctx, `SELECT id,staging_object_key,object_key,thumbnail_object_key,greatest(updated_at+$3::interval,upload_expires_at) FROM assets WHERE account_id=$1 AND trip_id=$2 ORDER BY id`, r.args.AccountID, r.args.TripID, fmt.Sprintf("%d seconds", int(assets.UploadWindow.Seconds())))
	if err != nil {
		return plan, err
	}
	for rows.Next() {
		var id uuid.UUID
		var staging, original, thumbnail *string
		var expires time.Time
		if err = rows.Scan(&id, &staging, &original, &thumbnail, &expires); err != nil {
			rows.Close()
			return plan, err
		}
		assetPrefixes := []string{}
		for _, root := range []string{"staging/", "assets/", "thumbnails/"} {
			assetPrefixes = append(assetPrefixes, root+r.args.AccountID.String()+"/"+id.String()+"/")
		}
		for _, key := range []*string{staging, original, thumbnail} {
			if key == nil {
				continue
			}
			allowed := false
			for _, prefix := range assetPrefixes {
				if strings.HasPrefix(*key, prefix) {
					allowed = true
				}
			}
			if !allowed {
				rows.Close()
				return plan, &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
			}
			plan.Keys = append(plan.Keys, *key)
		}
		plan.Prefixes = append(plan.Prefixes, assetPrefixes...)
		if expires.After(plan.NotBefore) {
			plan.NotBefore = expires.Add(time.Minute)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return plan, err
	}
	rows, err = tx.Query(ctx, `SELECT object_key FROM export_jobs WHERE account_id=$1 AND id IN (`+relatedExports+`) AND object_key IS NOT NULL`, r.args.AccountID, r.args.TripID)
	if err != nil {
		return plan, err
	}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return plan, err
		}
		if !strings.HasPrefix(key, "exports/"+r.args.AccountID.String()+"/") {
			rows.Close()
			return plan, &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
		}
		r.explicit[key] = true
		plan.Keys = append(plan.Keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return plan, err
	}
	r.prefixes = plan.Prefixes
	if time.Now().Before(plan.NotBefore) {
		if _, err = tx.Exec(ctx, `UPDATE deletion_jobs SET error_code='UPLOAD_AUTHORIZATION_ACTIVE' WHERE id=$1`, r.args.JobID); err != nil {
			return plan, err
		}
	}
	if err = r.audit(ctx, tx, "trip.purge.start", "success"); err != nil {
		return plan, err
	}
	return plan, tx.Commit(ctx)
}

func (r *tripPurgeRun) SetObjects(ctx context.Context, keys []string) ([]string, error) {
	tx, err := r.conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	for _, key := range keys {
		allowed := r.explicit[key]
		for _, prefix := range r.prefixes {
			if strings.HasPrefix(key, prefix) {
				allowed = true
			}
		}
		if !allowed {
			return nil, &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
		}
		// No other asset may reference an object included by a prefix or export.
		var shared bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE (account_id<>$1 OR trip_id IS DISTINCT FROM $2) AND ($3 IN (object_key,staging_object_key,thumbnail_object_key))) OR EXISTS(SELECT 1 FROM export_jobs WHERE object_key=$3 AND NOT(account_id=$1 AND id IN (`+relatedExports+`)))`, r.args.AccountID, r.args.TripID, key).Scan(&shared); err != nil {
			return nil, err
		}
		if shared {
			return nil, &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO trip_purge_objects(job_id,account_id,trip_id,object_key) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, r.args.JobID, r.args.AccountID, r.args.TripID, key); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE deletion_jobs SET stage='remove_objects',total_items=(SELECT count(*) FROM trip_purge_objects WHERE job_id=$1),processed_items=(SELECT count(*) FROM trip_purge_objects WHERE job_id=$1 AND removed_at IS NOT NULL) WHERE id=$1`, r.args.JobID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT object_key FROM trip_purge_objects WHERE job_id=$1 AND account_id=$2 AND trip_id=$3 ORDER BY object_key`, r.args.JobID, r.args.AccountID, r.args.TripID)
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		allowed := r.explicit[key]
		for _, prefix := range r.prefixes {
			if strings.HasPrefix(key, prefix) {
				allowed = true
			}
		}
		if !allowed {
			rows.Close()
			return nil, &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
		}
		out = append(out, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}
func (r *tripPurgeRun) ObjectRemoved(ctx context.Context, key string) error {
	_, err := r.conn.Exec(ctx, `WITH done AS (UPDATE trip_purge_objects SET removed_at=now() WHERE job_id=$1 AND account_id=$2 AND trip_id=$3 AND object_key=$4 AND removed_at IS NULL RETURNING job_id) UPDATE deletion_jobs SET processed_items=processed_items+(SELECT count(*) FROM done) WHERE id=$1 AND owner_account_id=$2 AND target_trip_id=$3`, r.args.JobID, r.args.AccountID, r.args.TripID, key)
	return err
}

var purgeTables = []struct{ table, entity string }{
	{"itinerary_route_legs", ""}, {"trip_route_summaries", ""}, {"ledger_entry_splits", ""}, {"ledger_attachments", ""},
	{"documents", "document"}, {"photos", "photo"}, {"ledger_entries", "ledger_entry"}, {"reservations", "reservation"},
	{"trip_members", "trip_member"}, {"itinerary_items", "itinerary_item"}, {"packing_items", "packing_item"}, {"todo_items", "todo"}, {"assets", "asset"},
}

func (r *tripPurgeRun) Finish(ctx context.Context) error {
	if _, err := r.conn.Exec(ctx, `UPDATE deletion_jobs SET stage='remove_rows' WHERE id=$1 AND owner_account_id=$2 AND target_trip_id=$3 AND status='running'`, r.args.JobID, r.args.AccountID, r.args.TripID); err != nil {
		return err
	}
	tx, err := r.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	seq, err := r.lock(ctx, tx)
	if err != nil {
		return err
	}
	var version int64
	var deleted time.Time
	if err = tx.QueryRow(ctx, `SELECT version,deleted_at FROM trips WHERE account_id=$1 AND id=$2 AND purge_requested_at IS NOT NULL FOR UPDATE`, r.args.AccountID, r.args.TripID).Scan(&version, &deleted); err != nil {
		return err
	}
	var pending int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM trip_purge_objects WHERE job_id=$1 AND removed_at IS NULL`, r.args.JobID).Scan(&pending); err != nil {
		return err
	}
	if pending != 0 {
		return errors.New("unverified objects remain")
	}
	if err = r.audit(ctx, tx, "trip.purge.complete", "success"); err != nil {
		return err
	}
	// Preserve IDs only, so replayed client writes cannot resurrect deleted entities.
	for _, t := range purgeTables {
		if t.entity == "" {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO entity_tombstones(account_id,entity_type,entity_id,trip_id,last_version,deleted_at,purged_at) SELECT account_id,$3,id,trip_id,version,coalesce(deleted_at,now()),now() FROM `+t.table+` WHERE account_id=$1 AND trip_id=$2 ON CONFLICT DO NOTHING`, r.args.AccountID, r.args.TripID, t.entity); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO entity_tombstones(account_id,entity_type,entity_id,trip_id,last_version,deleted_at,purged_at) VALUES($1,'trip',$2,$2,$3,$4,now()) ON CONFLICT DO NOTHING`, r.args.AccountID, r.args.TripID, version+1, deleted); err != nil {
		return err
	}
	// Invalidate derived exports before deleting their snapshot references, preserving unrelated snapshots.
	if _, err = tx.Exec(ctx, `DELETE FROM export_jobs WHERE account_id=$1 AND id IN (`+relatedExports+`)`, r.args.AccountID, r.args.TripID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE data_snapshots SET selected_trip_ids=selected_trip_ids-$2::uuid::text,item_count=(SELECT count(*) FROM snapshot_items i WHERE i.snapshot_id=data_snapshots.id AND i.trip_id IS DISTINCT FROM $2::uuid AND i.entity_id NOT IN (SELECT entity_id FROM entity_tombstones WHERE account_id=$1 AND trip_id=$2) AND (i.payload->>'trip_id') IS DISTINCT FROM $2::uuid::text) WHERE account_id=$1 AND id IN (`+relatedSnapshots+`)`, r.args.AccountID, r.args.TripID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM snapshot_asset_refs f USING assets a WHERE f.account_id=$1 AND a.account_id=f.account_id AND f.asset_id=a.id AND a.trip_id=$2`, r.args.AccountID, r.args.TripID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM snapshot_items WHERE account_id=$1 AND (trip_id=$2 OR entity_id IN (SELECT entity_id FROM entity_tombstones WHERE account_id=$1 AND trip_id=$2) OR payload->>'trip_id'=$2::uuid::text)`, r.args.AccountID, r.args.TripID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM mutation_receipts m WHERE account_id=$1 AND (operation_id IN (SELECT batch_id FROM sync_changes WHERE account_id=$1 AND trip_id=$2) OR result->'primary'->>'id' IN (SELECT entity_id::text FROM entity_tombstones WHERE account_id=$1 AND trip_id=$2) OR EXISTS(SELECT 1 FROM jsonb_array_elements(coalesce(result->'affected','[]'::jsonb)) a WHERE a->>'id' IN (SELECT entity_id::text FROM entity_tombstones WHERE account_id=$1 AND trip_id=$2)))`, r.args.AccountID, r.args.TripID); err != nil {
		return err
	}
	// Redaction retains contiguous sequence numbers without retaining private payloads.
	if _, err = tx.Exec(ctx, `UPDATE sync_changes SET change_kind='redacted',snapshot=NULL,changed_fields=NULL,requires_snapshot=false WHERE account_id=$1 AND trip_id=$2`, r.args.AccountID, r.args.TripID); err != nil {
		return err
	}
	for _, t := range purgeTables {
		if _, err = tx.Exec(ctx, `DELETE FROM `+t.table+` WHERE account_id=$1 AND trip_id=$2`, r.args.AccountID, r.args.TripID); err != nil {
			return err
		}
	}
	// Parent deletion enforces completeness through all foreign keys, including sharing restrictions.
	tag, err := tx.Exec(ctx, `DELETE FROM trips WHERE account_id=$1 AND id=$2`, r.args.AccountID, r.args.TripID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("target trip missing at finalization")
	}
	now := time.Now().UTC()
	endSeq, err := pgcore.RecordChanges(ctx, dbgen.New(tx), r.args.AccountID, uuid.New(), seq, []write.Change{{EntityType: "trip", EntityID: r.args.TripID, TripID: &r.args.TripID, Version: version + 1, Kind: write.ChangePurge}}, now)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE account_sync_state SET last_seq=$2,updated_at=$3 WHERE account_id=$1`, r.args.AccountID, endSeq, now); err != nil {
		return err
	}
	tag, err = tx.Exec(ctx, `UPDATE deletion_jobs SET stage='done',status='completed',finished_at=now(),error_code=NULL WHERE id=$1 AND owner_account_id=$2 AND target_trip_id=$3 AND status='running'`, r.args.JobID, r.args.AccountID, r.args.TripID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("cleanup job changed during finalization")
	}
	if _, err = tx.Exec(ctx, `DELETE FROM trip_purge_objects WHERE job_id=$1 AND account_id=$2 AND trip_id=$3`, r.args.JobID, r.args.AccountID, r.args.TripID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *tripPurgeRun) Fail(ctx context.Context, code string) error {
	if code == "PURGE_RETRY_REQUIRED" {
		return nil
	}
	tag, err := r.conn.Exec(ctx, `UPDATE deletion_jobs SET status='failed',error_code=$4,finished_at=now() WHERE id=$1 AND owner_account_id=$2 AND target_trip_id=$3 AND scope='trip' AND status<>'completed'`, r.args.JobID, r.args.AccountID, r.args.TripID, code)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	// Failure telemetry must remain retryable even when audit storage caused the failure.
	tx, err := r.conn.Begin(ctx)
	if err != nil {
		return nil
	}
	defer tx.Rollback(ctx)
	if err = r.audit(ctx, tx, "trip.purge.failed", "failure"); err != nil {
		return nil
	}
	return tx.Commit(ctx)
}
