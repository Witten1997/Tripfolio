package adminpg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// Both cleanup scopes use the same object manifest, phases, and object deletion verifier.
// A nil target denotes account scope; a zero UUID is never interpreted as an unbounded owner.
type tripPurgeRun struct {
	conn       *pgxpool.Conn
	release    func()
	args       trip.PurgeJobArgs
	target     *uuid.UUID
	adminAudit bool
	prefixes   []string
	explicit   map[string]bool
}

func (s *TripPurgeStore) Acquire(ctx context.Context, a trip.PurgeJobArgs) (trip.PurgeRun, error) {
	if a.AccountID == uuid.Nil || a.JobID == uuid.Nil {
		return nil, &trip.PurgeError{Code: "PURGE_SCOPE_MISMATCH"}
	}
	var target *uuid.UUID
	scope := "account"
	if a.TripID != uuid.Nil {
		target = &a.TripID
		scope = "trip"
	}
	var via string
	err := s.pool.QueryRow(ctx, `SELECT requested_via FROM deletion_jobs WHERE id=$1 AND owner_account_id=$2 AND target_trip_id IS NOT DISTINCT FROM $3::uuid AND scope=$4`, a.JobID, a.AccountID, target, scope).Scan(&via)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &trip.PurgeError{Code: "PURGE_SCOPE_MISMATCH"}
	}
	if err != nil {
		return nil, err
	}
	var conn *pgxpool.Conn
	var release func()
	if target == nil {
		conn, release, err = pgcore.LockAccountObjects(ctx, s.pool, a.AccountID)
	} else {
		conn, release, err = pgcore.LockTripObjects(ctx, s.pool, a.AccountID, *target)
	}
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "55P03" {
			return nil, &trip.PurgeBusy{}
		}
		return nil, err
	}
	return &tripPurgeRun{conn: conn, release: release, args: a, target: target, adminAudit: via == "admin", explicit: map[string]bool{}}, nil
}
func (r *tripPurgeRun) Close() { r.release() }
func (r *tripPurgeRun) audit(ctx context.Context, tx pgx.Tx, action, result string) error {
	if !r.adminAudit {
		return nil
	}
	a := admin.Audit{ID: uuid.New(), SubjectID: &r.args.AccountID, ResourceType: "trip", ResourceID: r.target, Action: action, Result: result, OccurredAt: time.Now().UTC(), Details: map[string]any{"job_id": r.args.JobID, "executor": "worker"}}
	if err := insertAudit(ctx, tx, a); err != nil {
		return &trip.PurgeError{Code: "ADMIN_AUDIT_UNAVAILABLE", Cause: err}
	}
	return nil
}

const relatedSnapshots = `SELECT s.id FROM data_snapshots s WHERE s.account_id=$1 AND ($2::uuid IS NULL OR
 s.selected_trip_ids @> jsonb_build_array($2::uuid::text)
 OR EXISTS(SELECT 1 FROM snapshot_items i WHERE i.account_id=$1 AND i.snapshot_id=s.id AND (i.trip_id=$2 OR (i.entity_type='trip' AND i.entity_id=$2) OR i.payload->>'trip_id'=$2::uuid::text))
 OR EXISTS(SELECT 1 FROM snapshot_asset_refs f JOIN assets a ON a.account_id=f.account_id AND a.id=f.asset_id WHERE f.account_id=$1 AND f.snapshot_id=s.id AND a.trip_id=$2))`
const relatedExports = `SELECT e.id FROM export_jobs e WHERE e.account_id=$1 AND ($2::uuid IS NULL OR
 e.selected_trip_ids @> jsonb_build_array($2::uuid::text) OR e.snapshot_id IN (` + relatedSnapshots + `))`

func (r *tripPurgeRun) lock(ctx context.Context, tx pgx.Tx) (int64, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM accounts WHERE id=$1 FOR UPDATE`, r.args.AccountID).Scan(&status)
	if err != nil {
		return 0, err
	}
	if r.target == nil && status != "deleting" {
		return 0, &trip.PurgeError{Code: "PURGE_SCOPE_MISMATCH"}
	}
	var seq int64
	err = tx.QueryRow(ctx, `SELECT last_seq FROM account_sync_state WHERE account_id=$1 FOR UPDATE`, r.args.AccountID).Scan(&seq)
	return seq, err
}
func (r *tripPurgeRun) invalidate(ctx context.Context, tx pgx.Tx) error {
	for _, sql := range []string{
		`DELETE FROM trip_shares WHERE account_id=$1 AND ($2::uuid IS NULL OR trip_id=$2)`,
		`UPDATE data_snapshots SET status='invalidated',selected_trip_ids=CASE WHEN $2::uuid IS NOT NULL AND NOT(selected_trip_ids @> jsonb_build_array($2::uuid::text)) THEN selected_trip_ids||jsonb_build_array($2::uuid::text) ELSE selected_trip_ids END WHERE account_id=$1 AND id IN (` + relatedSnapshots + `)`,
		`UPDATE export_jobs SET status='invalidated' WHERE account_id=$1 AND id IN (` + relatedExports + `)`,
	} {
		if _, err := tx.Exec(ctx, sql, r.args.AccountID, r.target); err != nil {
			return err
		}
	}
	return nil
}
func (r *tripPurgeRun) Prepare(ctx context.Context) (plan trip.PurgePlan, err error) {
	tx, err := r.conn.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var status string
	// Completed jobs remain idempotent even after the owning account is gone.
	err = tx.QueryRow(ctx, `SELECT status FROM deletion_jobs WHERE id=$1`, r.args.JobID).Scan(&status)
	if err != nil {
		return
	}
	if status == "completed" {
		plan.Completed = true
		err = tx.Commit(ctx)
		return
	}
	if _, err = r.lock(ctx, tx); err != nil {
		return
	}
	if err = tx.QueryRow(ctx, `SELECT status FROM deletion_jobs WHERE id=$1 FOR UPDATE`, r.args.JobID).Scan(&status); err != nil {
		return
	}
	if status == "failed" && r.target != nil {
		err = &trip.PurgeError{Code: "PURGE_RETRY_REQUIRED"}
		return
	}
	if r.target != nil {
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL AND purge_requested_at IS NOT NULL FROM trips WHERE account_id=$1 AND id=$2 FOR UPDATE`, r.args.AccountID, r.target).Scan(&valid); err != nil {
			return
		}
		if !valid {
			err = &trip.PurgeError{Code: "PURGE_SCOPE_MISMATCH"}
			return
		}
	}
	now := time.Now().UTC()
	var recoveryUntil time.Time
	if r.target == nil {
		if err = tx.QueryRow(ctx, `SELECT created_at+interval '1 minute' FROM deletion_jobs WHERE id=$1`, r.args.JobID).Scan(&recoveryUntil); err != nil {
			return
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE deletion_jobs SET status='running',stage='revoke_access',started_at=coalesce(started_at,$2),finished_at=NULL,error_code=NULL WHERE id=$1`, r.args.JobID, now); err != nil {
		return
	}
	if err = r.invalidate(ctx, tx); err != nil {
		return
	}
	rows, err := tx.Query(ctx, `SELECT id,staging_object_key,object_key,thumbnail_object_key,greatest(updated_at+$3::interval,upload_expires_at) FROM assets WHERE account_id=$1 AND ($2::uuid IS NULL OR trip_id=$2) ORDER BY id`, r.args.AccountID, r.target, fmt.Sprintf("%d seconds", int(assets.UploadWindow.Seconds())))
	if err != nil {
		return
	}
	for rows.Next() {
		var id uuid.UUID
		var staging, original, thumbnail *string
		var expires time.Time
		if err = rows.Scan(&id, &staging, &original, &thumbnail, &expires); err != nil {
			break
		}
		prefixes := []string{}
		for _, root := range []string{"staging/", "assets/", "thumbnails/"} {
			prefixes = append(prefixes, root+r.args.AccountID.String()+"/"+id.String()+"/")
		}
		for _, key := range []*string{staging, original, thumbnail} {
			if key == nil {
				continue
			}
			allowed := false
			for _, prefix := range prefixes {
				allowed = allowed || strings.HasPrefix(*key, prefix)
			}
			if !allowed {
				err = &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
				break
			}
			plan.Keys = append(plan.Keys, *key)
		}
		if err != nil {
			break
		}
		plan.Prefixes = append(plan.Prefixes, prefixes...)
		if expires.Add(time.Minute).After(plan.NotBefore) {
			plan.NotBefore = expires.Add(time.Minute)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return
	}
	rows, err = tx.Query(ctx, `SELECT object_key FROM export_jobs WHERE account_id=$1 AND id IN (`+relatedExports+`) AND object_key IS NOT NULL`, r.args.AccountID, r.target)
	if err != nil {
		return
	}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			break
		}
		if !strings.HasPrefix(key, "exports/"+r.args.AccountID.String()+"/") {
			err = &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
			break
		}
		r.explicit[key] = true
		plan.Keys = append(plan.Keys, key)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return
	}
	// A crash in remove_rows can leave already-deleted asset rows. The durable manifest retains their bounded prefixes.
	rows, err = tx.Query(ctx, `SELECT object_key FROM trip_purge_objects WHERE job_id=$1 AND account_id=$2 AND trip_id IS NOT DISTINCT FROM $3::uuid`, r.args.JobID, r.args.AccountID, r.target)
	if err != nil {
		return
	}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			break
		}
		parts := strings.Split(key, "/")
		if len(parts) < 3 || parts[1] != r.args.AccountID.String() {
			err = &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
			break
		}
		if parts[0] == "exports" {
			r.explicit[key] = true
		} else if len(parts) >= 4 && (parts[0] == "staging" || parts[0] == "assets" || parts[0] == "thumbnails") {
			if _, parseErr := uuid.Parse(parts[2]); parseErr != nil {
				err = &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
				break
			}
			for _, root := range []string{"staging/", "assets/", "thumbnails/"} {
				plan.Prefixes = append(plan.Prefixes, root+parts[1]+"/"+parts[2]+"/")
			}
		} else {
			err = &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
			break
		}
		plan.Keys = append(plan.Keys, key)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return
	}
	if r.target == nil {
		plan.Prefixes = nil
		for _, root := range []string{"staging/", "assets/", "thumbnails/", "exports/"} {
			plan.Prefixes = append(plan.Prefixes, root+r.args.AccountID.String()+"/")
		}
	}
	seen := map[string]bool{}
	r.prefixes = nil
	for _, prefix := range plan.Prefixes {
		if !seen[prefix] {
			seen[prefix] = true
			r.prefixes = append(r.prefixes, prefix)
		}
	}
	plan.Prefixes = r.prefixes
	waitCode := "UPLOAD_AUTHORIZATION_ACTIVE"
	if recoveryUntil.After(plan.NotBefore) {
		plan.NotBefore = recoveryUntil
		waitCode = "RECEIPT_RECOVERY_WINDOW"
	}
	if now.Before(plan.NotBefore) {
		if _, err = tx.Exec(ctx, `UPDATE deletion_jobs SET error_code=$2 WHERE id=$1`, r.args.JobID, waitCode); err != nil {
			return
		}
	}
	if err = r.audit(ctx, tx, "trip.purge.start", "success"); err != nil {
		return
	}
	err = tx.Commit(ctx)
	return
}
func (r *tripPurgeRun) allowed(key string) bool {
	if r.explicit[key] {
		return true
	}
	for _, p := range r.prefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}
func (r *tripPurgeRun) SetObjects(ctx context.Context, keys []string) ([]string, error) {
	// Bounded manifest transactions; no database transaction spans object I/O.
	seen := map[string]bool{}
	unique := []string{}
	for _, key := range keys {
		if !seen[key] {
			seen[key] = true
			unique = append(unique, key)
		}
	}
	for start := 0; start < len(unique); start += 200 {
		batch := unique[start:min(start+200, len(unique))]
		if err := r.saveObjects(ctx, batch); err != nil {
			return nil, err
		}
	}
	_, err := r.conn.Exec(ctx, `UPDATE deletion_jobs SET stage='remove_objects',total_items=(SELECT count(*) FROM trip_purge_objects WHERE job_id=$1),processed_items=(SELECT count(*) FROM trip_purge_objects WHERE job_id=$1 AND removed_at IS NOT NULL) WHERE id=$1`, r.args.JobID)
	if err != nil {
		return nil, err
	}
	rows, err := r.conn.Query(ctx, `SELECT object_key FROM trip_purge_objects WHERE job_id=$1 AND account_id=$2 AND trip_id IS NOT DISTINCT FROM $3::uuid ORDER BY object_key`, r.args.JobID, r.args.AccountID, r.target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		if !r.allowed(key) {
			return nil, &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
		}
		out = append(out, key)
	}
	return out, rows.Err()
}
func (r *tripPurgeRun) saveObjects(ctx context.Context, keys []string) error {
	tx, err := r.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, key := range keys {
		if !r.allowed(key) {
			return &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
		}
		var shared bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE (account_id<>$1 OR ($2::uuid IS NOT NULL AND trip_id IS DISTINCT FROM $2)) AND $3 IN(object_key,staging_object_key,thumbnail_object_key)) OR EXISTS(SELECT 1 FROM export_jobs WHERE object_key=$3 AND NOT(account_id=$1 AND id IN (`+relatedExports+`)))`, r.args.AccountID, r.target, key).Scan(&shared)
		if err != nil {
			return err
		}
		if shared {
			return &trip.PurgeError{Code: "OBJECT_SCOPE_MISMATCH"}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO trip_purge_objects(job_id,account_id,trip_id,object_key) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, r.args.JobID, r.args.AccountID, r.target, key); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (r *tripPurgeRun) ObjectRemoved(ctx context.Context, key string) error {
	_, err := r.conn.Exec(ctx, `WITH done AS(UPDATE trip_purge_objects SET removed_at=$5 WHERE job_id=$1 AND account_id=$2 AND trip_id IS NOT DISTINCT FROM $3::uuid AND object_key=$4 AND removed_at IS NULL RETURNING job_id) UPDATE deletion_jobs SET processed_items=processed_items+(SELECT count(*) FROM done) WHERE id=$1`, r.args.JobID, r.args.AccountID, r.target, key, time.Now().UTC())
	return err
}

var purgeTables = []struct{ table, entity string }{
	{"itinerary_route_legs", ""}, {"trip_route_summaries", ""}, {"ledger_entry_splits", ""}, {"ledger_attachments", ""},
	{"documents", "document"}, {"photos", "photo"}, {"ledger_entries", "ledger_entry"}, {"reservations", "reservation"},
	{"trip_members", "trip_member"}, {"itinerary_items", "itinerary_item"}, {"packing_items", "packing_item"}, {"todo_items", "todo"}, {"assets", "asset"},
}

// Every batch commits tombstones with its rows. Objects have all been verified before this method is entered.
func (r *tripPurgeRun) removeBatch(ctx context.Context, table, entity string) (int64, error) {
	tx, err := r.conn.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err = r.lock(ctx, tx); err != nil {
		return 0, err
	}
	if err = r.audit(ctx, tx, "trip.purge.batch", "success"); err != nil {
		return 0, err
	}
	if table == "assets" {
		if err = r.invalidate(ctx, tx); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM snapshot_asset_refs f USING assets a WHERE f.account_id=$1 AND a.account_id=f.account_id AND f.asset_id=a.id AND ($2::uuid IS NULL OR a.trip_id=$2)`, r.args.AccountID, r.target); err != nil {
			return 0, err
		}
	}
	order := "ctid"
	if table == "ledger_entries" {
		order = "(kind='refund') DESC,ctid"
	}
	sql := `WITH selected AS(SELECT ctid FROM ` + table + ` WHERE account_id=$1 AND ($2::uuid IS NULL OR trip_id=$2) ORDER BY ` + order + ` LIMIT 200)`
	if entity != "" && r.target != nil {
		sql += `,stones AS(INSERT INTO entity_tombstones(account_id,entity_type,entity_id,trip_id,last_version,deleted_at,purged_at) SELECT account_id,$3,id,trip_id,version,coalesce(deleted_at,$4),$4 FROM ` + table + ` WHERE ctid IN(SELECT ctid FROM selected) ON CONFLICT DO NOTHING) DELETE FROM ` + table + ` WHERE ctid IN(SELECT ctid FROM selected)`
		tag, err := tx.Exec(ctx, sql, r.args.AccountID, r.target, entity, time.Now().UTC())
		if err != nil {
			return 0, err
		}
		return tag.RowsAffected(), tx.Commit(ctx)
	}
	sql += ` DELETE FROM ` + table + ` WHERE ctid IN(SELECT ctid FROM selected)`
	tag, err := tx.Exec(ctx, sql, r.args.AccountID, r.target)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}
func (r *tripPurgeRun) Finish(ctx context.Context) error {
	var pending int64
	if err := r.conn.QueryRow(ctx, `SELECT count(*) FROM trip_purge_objects WHERE job_id=$1 AND removed_at IS NULL`, r.args.JobID).Scan(&pending); err != nil {
		return err
	}
	if pending != 0 {
		return errors.New("unverified objects remain")
	}
	if _, err := r.conn.Exec(ctx, `UPDATE deletion_jobs SET stage='remove_rows' WHERE id=$1 AND status='running'`, r.args.JobID); err != nil {
		return err
	}
	// Invalidate before each batch so a concurrent snapshot can never pin an asset indefinitely.
	for _, t := range purgeTables {
		for {
			n, err := r.removeBatch(ctx, t.table, t.entity)
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
		}
	}
	if r.target == nil {
		return r.finishAccount(ctx)
	}
	return r.finishTrip(ctx)
}
func (r *tripPurgeRun) finishTrip(ctx context.Context) error {
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
	if err = tx.QueryRow(ctx, `SELECT version,deleted_at FROM trips WHERE account_id=$1 AND id=$2 AND purge_requested_at IS NOT NULL FOR UPDATE`, r.args.AccountID, r.target).Scan(&version, &deleted); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err = r.invalidate(ctx, tx); err != nil {
		return err
	}
	if err = r.audit(ctx, tx, "trip.purge.complete", "success"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO entity_tombstones(account_id,entity_type,entity_id,trip_id,last_version,deleted_at,purged_at) VALUES($1,'trip',$2,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, r.args.AccountID, r.target, version+1, deleted, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM export_jobs WHERE account_id=$1 AND id IN (`+relatedExports+`)`, r.args.AccountID, r.target); err != nil {
		return err
	}
	// Invalidated snapshots cannot be read; remove all their retained payload, including account-wide snapshots.
	if _, err = tx.Exec(ctx, `DELETE FROM data_snapshots WHERE account_id=$1 AND id IN (`+relatedSnapshots+`)`, r.args.AccountID, r.target); err != nil {
		return err
	}
	// Receipts contain IDs only. Keep them to preserve idempotency after resource deletion.
	if _, err = tx.Exec(ctx, `UPDATE sync_changes SET change_kind='redacted',snapshot=NULL,changed_fields=NULL,requires_snapshot=false WHERE account_id=$1 AND trip_id=$2`, r.args.AccountID, r.target); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM trips WHERE account_id=$1 AND id=$2`, r.args.AccountID, r.target); err != nil {
		return err
	}
	endSeq, err := pgcore.RecordChanges(ctx, dbgen.New(tx), r.args.AccountID, uuid.New(), seq, []write.Change{{EntityType: "trip", EntityID: *r.target, TripID: r.target, Version: version + 1, Kind: write.ChangePurge}}, now)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE account_sync_state SET last_seq=$2,updated_at=$3 WHERE account_id=$1`, r.args.AccountID, endSeq, now); err != nil {
		return err
	}
	if err = r.complete(ctx, tx, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *tripPurgeRun) finishAccount(ctx context.Context) error {
	// Remove each remaining account table in bounded transactions. Final credentials/account deletion is atomic.
	for _, table := range []string{"trips", "export_jobs", "data_snapshots", "expense_categories", "sync_changes", "mutation_receipts", "entity_tombstones"} {
		for {
			n, err := r.accountBatch(ctx, table)
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
		}
	}
	tx, err := r.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(781241,18)`); err != nil {
		return err
	}
	if _, err = r.lock(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE deletion_jobs SET stage='finalize' WHERE id=$1`, r.args.JobID); err != nil {
		return err
	}
	for _, sql := range []string{
		`UPDATE admin_audit_events SET details='{"redacted":true}',reason='',source_ip='',user_agent='',request_id='' WHERE actor_account_id=$1 OR subject_account_id=$1`,
		`DELETE FROM auth_challenges WHERE email_key=(SELECT email_key FROM accounts WHERE id=$1)`,
		`DELETE FROM account_sessions WHERE account_id=$1`,
		`DELETE FROM admin_sessions WHERE account_id=$1`,
		`DELETE FROM admin_principals WHERE account_id=$1`,
		`DELETE FROM trip_purge_objects WHERE account_id=$1`,
		`DELETE FROM deletion_jobs WHERE owner_account_id=$1 AND scope='trip'`,
		`DELETE FROM account_sync_state WHERE account_id=$1`,
		`DELETE FROM accounts WHERE id=$1`,
	} {
		if _, err = tx.Exec(ctx, sql, r.args.AccountID); err != nil {
			return err
		}
	}
	// Cancelled verifiers cannot run again against a removed account. The current job may finish normally.
	if _, err = tx.Exec(ctx, `DELETE FROM river_job WHERE args->>'account_id'=$1 AND (args->>'job_id') IS DISTINCT FROM $2`, r.args.AccountID.String(), r.args.JobID.String()); err != nil {
		return err
	}
	if err = r.complete(ctx, tx, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *tripPurgeRun) accountBatch(ctx context.Context, table string) (int64, error) {
	tx, err := r.conn.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err = r.lock(ctx, tx); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE ctid IN(SELECT ctid FROM `+table+` WHERE account_id=$1 ORDER BY ctid LIMIT 200)`, r.args.AccountID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}
func (r *tripPurgeRun) complete(ctx context.Context, tx pgx.Tx, now time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE deletion_jobs SET status='completed',stage='done',finished_at=$2,error_code=NULL,retain_until=CASE WHEN scope='account' THEN $2::timestamptz+interval '7 days' ELSE NULL END,receipt_expires_at=CASE WHEN scope='account' THEN least(receipt_expires_at,$2::timestamptz+interval '7 days') ELSE receipt_expires_at END WHERE id=$1 AND status='running'`, r.args.JobID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("cleanup job changed during finalization")
	}
	_, err = tx.Exec(ctx, `DELETE FROM trip_purge_objects WHERE job_id=$1`, r.args.JobID)
	return err
}
func (r *tripPurgeRun) Fail(ctx context.Context, code string) error {
	if code == "PURGE_RETRY_REQUIRED" {
		return nil
	}
	tag, err := r.conn.Exec(ctx, `UPDATE deletion_jobs SET status='failed',error_code=$2,finished_at=$3 WHERE id=$1 AND status<>'completed'`, r.args.JobID, code, time.Now().UTC())
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
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
