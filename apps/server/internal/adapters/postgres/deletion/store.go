package deletionpg

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/account"
	"tripfolio/server/internal/modules/deletion"
	"tripfolio/server/internal/modules/travel/trip"
)

type Store struct {
	pool   *pgxpool.Pool
	queue  *river.Client[pgx.Tx]
	writer *pgcore.Writer
}

func NewStore(p *pgxpool.Pool, q *river.Client[pgx.Tx], w *pgcore.Writer) *Store {
	return &Store{p, q, w}
}
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apperr.As(err); ok {
		return err
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "P0019" {
		return apperr.Conflicted("ADMIN_LAST_AVAILABLE", "不能注销最后一个可用超级管理员")
	}
	return apperr.Dependency(err)
}
func session(ctx context.Context, tx pgx.Tx, a actor.Actor, recent bool) error {
	var at *time.Time
	now := time.Now().UTC()
	err := tx.QueryRow(ctx, `SELECT reauthenticated_at FROM account_sessions WHERE id=$1 AND account_id=$2 AND revoked_at IS NULL AND expires_at>$3 FOR SHARE`, a.SessionID, a.AccountID, now).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.Unauthorized("SESSION_EXPIRED", "")
	}
	if err != nil {
		return err
	}
	if recent && (at == nil || now.Before(*at) || now.Sub(*at) > 5*time.Minute) {
		return apperr.Forbidden("REAUTH_REQUIRED", "请先验证密码")
	}
	return nil
}
func exactVersion(entity string, id uuid.UUID, base, current int64) error {
	if base == current {
		return nil
	}
	return apperr.VersionConflict(&apperr.Conflict{EntityType: entity, EntityID: id, ExpectedVersion: base, CurrentVersion: current})
}
func issue(ctx context.Context, tx pgx.Tx, id, owner uuid.UUID) (deletion.Receipt, error) {
	token, err := security.RandomToken()
	if err != nil {
		return deletion.Receipt{}, err
	}
	out := deletion.Receipt{JobID: id, Token: token, ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour)}
	err = tx.QueryRow(ctx, `UPDATE deletion_jobs SET receipt_token_hash=$3,receipt_expires_at=least($4,coalesce(retain_until,$4)) WHERE id=$1 AND owner_account_id=$2 AND scope='account' AND status<>'completed' RETURNING receipt_expires_at`, id, owner, security.Digest(token), out.ExpiresAt).Scan(&out.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = apperr.NotFound()
	}
	return out, err
}
func (s *Store) Request(ctx context.Context, a actor.Actor, op uuid.UUID, base int64) (out deletion.Result, err error) {
	defer func() { err = mapError(err) }()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	// Administrative availability is always locked before accounts; matches status controls and triggers.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(781241,18)`); err != nil {
		return
	}
	var status string
	var version int64
	err = tx.QueryRow(ctx, `SELECT status,version FROM accounts WHERE id=$1 FOR UPDATE`, a.AccountID).Scan(&status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = apperr.Unauthorized("SESSION_EXPIRED", "")
	}
	if err != nil {
		return
	}
	if _, err = tx.Exec(ctx, `SELECT account_id FROM account_sync_state WHERE account_id=$1 FOR UPDATE`, a.AccountID); err != nil {
		return
	}
	if err = session(ctx, tx, a, false); err != nil {
		return
	}
	fp := write.Fingerprint("account.delete", a.AccountID.String(), &base, struct {
		Confirm bool `json:"confirm"`
	}{true})
	var hash, raw []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,result FROM mutation_receipts WHERE account_id=$1 AND operation_id=$2`, a.AccountID, op).Scan(&hash, &raw)
	if err == nil {
		if !hmac.Equal(hash, fp[:]) {
			err = apperr.Conflicted("IDEMPOTENCY_CONFLICT", "同一操作编号携带了不同的内容")
			return
		}
		if err = json.Unmarshal(raw, &out.Result); err != nil {
			return
		}
		out.Result.Replayed = true
		var id uuid.UUID
		for _, ref := range out.Result.Affected {
			if ref.Type == trip.EntityTypeDeletionJob {
				id = ref.ID
			}
		}
		if id == uuid.Nil {
			err = errors.New("missing deletion receipt reference")
			return
		}
		out.Receipt, err = issue(ctx, tx, id, a.AccountID)
		if err != nil {
			return
		}
		err = tx.Commit(ctx)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return
	}
	err = nil
	if status != "active" {
		err = account.StatusError(status)
		return
	}
	if err = session(ctx, tx, a, true); err != nil {
		return
	}
	if err = exactVersion("account", a.AccountID, base, version); err != nil {
		return
	}
	now := time.Now().UTC()
	id := uuid.New()
	if _, err = tx.Exec(ctx, `SELECT set_config('tripfolio.deletion_session',$1,true)`, a.SessionID.String()); err != nil {
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE accounts SET status='deleting',version=version+1,updated_at=$2 WHERE id=$1`, a.AccountID, now); err != nil {
		return
	}
	// The access JWT cannot be refreshed. Bound the surviving DB session to the same short window.
	if _, err = tx.Exec(ctx, `UPDATE account_sessions SET expires_at=least(expires_at,$3) WHERE account_id=$1 AND id=$2`, a.AccountID, a.SessionID, now.Add(15*time.Minute)); err != nil {
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE data_snapshots SET status='invalidated' WHERE account_id=$1`, a.AccountID); err != nil {
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE export_jobs SET status='invalidated' WHERE account_id=$1`, a.AccountID); err != nil {
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO deletion_jobs(id,owner_account_id,scope,created_at) VALUES($1,$2,'account',$3)`, id, a.AccountID, now); err != nil {
		return
	}
	out.Receipt, err = issue(ctx, tx, id, a.AccountID)
	if err != nil {
		return
	}
	ref := write.Ref("account", a.AccountID, version+1)
	out.Result = write.Result{OperationID: op, Primary: &ref, Affected: []write.EntityRef{ref, {Type: trip.EntityTypeDeletionJob, ID: id}}, Warnings: []string{}}
	raw, err = json.Marshal(out.Result)
	if err != nil {
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mutation_receipts(account_id,operation_id,operation_type,request_hash,result,created_at) VALUES($1,$2,'account.delete',$3,$4,$5)`, a.AccountID, op, fp[:], raw, now); err != nil {
		return
	}
	if _, err = s.queue.InsertTx(ctx, tx, trip.PurgeJobArgs{JobID: id, AccountID: a.AccountID}, nil); err != nil {
		return
	}
	err = tx.Commit(ctx)
	return
}
func (s *Store) Renew(ctx context.Context, a actor.Actor, id uuid.UUID) (out deletion.Receipt, err error) {
	defer func() { err = mapError(err) }()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM accounts WHERE id=$1 FOR UPDATE`, a.AccountID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = apperr.Unauthorized("SESSION_EXPIRED", "")
	}
	if err != nil {
		return
	}
	if status != "deleting" {
		err = apperr.NotFound()
		return
	}
	if err = session(ctx, tx, a, false); err != nil {
		return
	}
	out, err = issue(ctx, tx, id, a.AccountID)
	if err != nil {
		return
	}
	err = tx.Commit(ctx)
	return
}

const columns = `id,scope,status,stage,processed_items,total_items,created_at,started_at,finished_at,error_code`

func scan(row pgx.Row) (j deletion.Job, err error) {
	err = row.Scan(&j.ID, &j.Scope, &j.Status, &j.Stage, &j.ProcessedItems, &j.TotalItems, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &j.ErrorCode, &j.Retryable)
	return
}

const retryable = `(scope='trip' AND status<>'completed' AND NOT EXISTS(SELECT 1 FROM river_job r WHERE r.kind='trip_purge' AND r.args->>'job_id'=deletion_jobs.id::text AND r.state IN ('available','pending','running','retryable','scheduled')))`

func (s *Store) GetTrip(ctx context.Context, a actor.Actor, id uuid.UUID, byTrip bool) (j deletion.Job, err error) {
	predicate := "id=$2"
	if byTrip {
		predicate = "target_trip_id=$2"
	}
	j, err = scan(s.pool.QueryRow(ctx, `SELECT `+columns+`,`+retryable+` FROM deletion_jobs WHERE owner_account_id=$1 AND `+predicate+` AND scope='trip' AND EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND status='active') ORDER BY created_at DESC,id DESC LIMIT 1`, a.AccountID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		err = apperr.NotFound()
	}
	return j, mapError(err)
}
func (s *Store) GetAccount(ctx context.Context, id uuid.UUID, token string) (j deletion.Job, err error) {
	if len(token) != 43 {
		return j, apperr.Unauthorized("AUTH_REQUIRED", "需要注销查询凭证")
	}
	var digest []byte
	var expires, retain *time.Time
	err = s.pool.QueryRow(ctx, `SELECT receipt_token_hash,receipt_expires_at,retain_until FROM deletion_jobs WHERE id=$1 AND scope='account'`, id).Scan(&digest, &expires, &retain)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, apperr.Gone("RESOURCE_GONE", "注销任务不存在或已过期")
	}
	if err != nil {
		return j, mapError(err)
	}
	if !hmac.Equal(digest, security.Digest(token)) {
		return j, apperr.Unauthorized("AUTH_REQUIRED", "查询凭证无效")
	}
	now := time.Now()
	if expires == nil || !now.Before(*expires) || (retain != nil && !now.Before(*retain)) {
		return j, apperr.Gone("RESOURCE_GONE", "查询凭证已过期")
	}
	j, err = scan(s.pool.QueryRow(ctx, `SELECT `+columns+`,false FROM deletion_jobs WHERE id=$1 AND scope='account' AND receipt_token_hash=$2`, id, digest))
	if errors.Is(err, pgx.ErrNoRows) {
		err = apperr.Unauthorized("AUTH_REQUIRED", "查询凭证已失效")
	}
	return j, mapError(err)
}
func (s *Store) RetryTrip(ctx context.Context, a actor.Actor, id, op uuid.UUID, base int64) (write.Result, error) {
	fp := write.Fingerprint("trip.purge.retry", id.String(), &base, struct {
		Confirm bool `json:"confirm"`
	}{true})
	return s.writer.Run(ctx, write.Request{AccountID: a.AccountID, OperationID: op, OperationType: "trip.purge.retry", Fingerprint: fp}, func(ctx context.Context, scope *pgcore.TxScope) error {
		if err := session(ctx, scope.Tx, a, true); err != nil {
			return err
		}
		var tripID uuid.UUID
		var version int64
		err := scope.Tx.QueryRow(ctx, `SELECT t.id,t.version FROM deletion_jobs d JOIN trips t ON t.id=d.target_trip_id AND t.account_id=d.owner_account_id WHERE d.id=$1 AND d.owner_account_id=$2 AND d.scope='trip' AND t.purge_requested_at IS NOT NULL FOR UPDATE OF t`, id, a.AccountID).Scan(&tripID, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.NotFound()
		}
		if err != nil {
			return err
		}
		if err = exactVersion("trip", tripID, base, version); err != nil {
			return err
		}
		var locked bool
		if err = scope.Tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "trip-objects:"+a.AccountID.String()+":"+tripID.String()).Scan(&locked); err != nil {
			return err
		}
		if !locked {
			return apperr.Conflicted("PURGE_BUSY", "清理或资产校验仍在执行")
		}
		tag, err := scope.Tx.Exec(ctx, `UPDATE deletion_jobs SET status='queued',error_code=NULL,finished_at=NULL,requested_via='user' WHERE id=$1 AND owner_account_id=$2 AND `+retryable, id, a.AccountID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return apperr.Conflicted("PURGE_NOT_RETRYABLE", "仅失败或中断的任务可以重试")
		}
		scope.SetPrimary(write.Ref("trip", tripID, version))
		scope.AddAffected(write.EntityRef{Type: trip.EntityTypeDeletionJob, ID: id})
		return scope.Enqueue(trip.PurgeJobArgs{JobID: id, AccountID: a.AccountID, TripID: tripID})
	}, nil)
}

// Reconcile is durable recovery after exhausted River attempts, process crashes, and restored queues.
func (s *Store) Reconcile(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(781241,25)`).Scan(&locked); err != nil || !locked {
		return err
	}
	now := time.Now().UTC()
	if _, err = tx.Exec(ctx, `DELETE FROM deletion_jobs WHERE scope='account' AND retain_until<=$1`, now); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT d.id,d.owner_account_id FROM deletion_jobs d JOIN accounts a ON a.id=d.owner_account_id WHERE d.scope='account' AND d.status<>'completed' AND a.status='deleting' AND coalesce(d.finished_at,d.created_at)<$1 AND NOT EXISTS(SELECT 1 FROM river_job r WHERE r.kind='trip_purge' AND r.args->>'job_id'=d.id::text AND r.state IN ('available','pending','running','retryable','scheduled')) ORDER BY d.created_at LIMIT 50`, now.Add(-time.Minute))
	if err != nil {
		return err
	}
	var args []trip.PurgeJobArgs
	for rows.Next() {
		var a trip.PurgeJobArgs
		if err = rows.Scan(&a.JobID, &a.AccountID); err != nil {
			rows.Close()
			return err
		}
		args = append(args, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range args {
		if _, err = s.queue.InsertTx(ctx, tx, a, nil); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
