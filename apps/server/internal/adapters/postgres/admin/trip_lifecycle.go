package adminpg

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/modules/travel/trip"
)

type TripLifecycleStore struct {
	pool  *pgxpool.Pool
	queue *river.Client[pgx.Tx]
	clock clock.Clock
}

func NewTripLifecycleStore(pool *pgxpool.Pool, queue *river.Client[pgx.Tx], clk clock.Clock) *TripLifecycleStore {
	return &TripLifecycleStore{pool: pool, queue: queue, clock: clk}
}

type lifecycleScope struct {
	changes  []write.Change
	warnings []string
	jobs     []write.JobArgs
	primary  *write.EntityRef
	affected []write.EntityRef
}

func (s *lifecycleScope) Record(c write.Change)         { s.changes = append(s.changes, c) }
func (s *lifecycleScope) Warn(w string)                 { s.warnings = append(s.warnings, w) }
func (s *lifecycleScope) SetPrimary(r write.EntityRef)  { s.primary = &r }
func (s *lifecycleScope) AddAffected(r write.EntityRef) { s.affected = append(s.affected, r) }
func (s *lifecycleScope) Enqueue(j write.JobArgs) error { s.jobs = append(s.jobs, j); return nil }

// The surrounding admin transaction owns authorization, exact version checks, audit and commit.
type lifecycleUnit struct {
	scope *lifecycleScope
	repo  trip.Repo
}

func (u lifecycleUnit) Run(ctx context.Context, req write.Request, fn func(context.Context, write.Scope, trip.Repo) error, reload func(context.Context, trip.Repo) (any, error)) (write.Result, error) {
	if err := fn(ctx, u.scope, u.repo); err != nil {
		return write.Result{}, err
	}
	var data any
	var err error
	if reload != nil {
		data, err = reload(ctx, u.repo)
	}
	return write.Result{OperationID: req.OperationID, Primary: u.scope.primary, Affected: u.scope.affected, Warnings: u.scope.warnings, Data: data}, err
}

func (s *TripLifecycleStore) MutateTrip(ctx context.Context, sess admin.Session, id uuid.UUID, cmd admin.TripMutation, audit *admin.Audit) (admin.TripMutationResult, error) {
	var out admin.TripMutationResult
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var owner uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT account_id FROM trips WHERE id=$1`, id).Scan(&owner); errors.Is(err, pgx.ErrNoRows) {
		return out, apperr.NotFound()
	} else if err != nil {
		return out, err
	}
	audit.SubjectID = &owner
	// This order matches account controls and the user writer.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(781241,18)`); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM accounts WHERE id=ANY($1::uuid[]) ORDER BY id FOR UPDATE`, []uuid.UUID{owner, sess.AccountID})
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var locked uuid.UUID
		if err = rows.Scan(&locked); err != nil {
			rows.Close()
			return out, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	now := s.clock.Now()
	var reauth *time.Time
	err = tx.QueryRow(ctx, `SELECT s.reauthenticated_at FROM admin_sessions s JOIN accounts a ON a.id=s.account_id JOIN admin_principals p ON p.account_id=a.id
		WHERE s.id=$1 AND s.account_id=$2 AND a.status='active' AND p.revoked_at IS NULL AND p.version=s.principal_version
		AND s.password_changed_at=a.password_changed_at AND s.revoked_at IS NULL AND s.expires_at>$3 AND s.last_seen_at>$4 FOR UPDATE OF s`, sess.ID, sess.AccountID, now, now.Add(-admin.SessionIdle)).Scan(&reauth)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, apperr.Unauthorized("ADMIN_SESSION_EXPIRED", "后台登录已失效")
	}
	if err != nil {
		return out, err
	}
	if (cmd.Action == "purge" || cmd.Action == "retry") && (reauth == nil || now.Before(*reauth) || now.Sub(*reauth) > admin.ReauthWindow) {
		return out, apperr.Forbidden("ADMIN_REAUTH_REQUIRED", "请重新验证密码")
	}
	var lastSeq int64
	if err = tx.QueryRow(ctx, `SELECT last_seq FROM account_sync_state WHERE account_id=$1 FOR UPDATE`, owner).Scan(&lastSeq); err != nil {
		return out, err
	}
	q := dbgen.New(tx)
	repo := travelpg.NewTripRepository(&pgcore.TxScope{Tx: tx, Queries: q, AccountID: owner, Now: now})
	before, found, err := repo.GetForUpdate(ctx, owner, id)
	if err != nil {
		return out, err
	}
	if !found {
		return out, apperr.NotFound()
	}
	if int64(before.Version) != cmd.Version {
		return out, apperr.Conflicted("VERSION_CONFLICT", "旅行已被修改，请刷新概况后重试")
	}
	scope := &lifecycleScope{warnings: []string{}}
	travel := trip.NewService(lifecycleUnit{scope: scope, repo: repo}, nil, nil, s.clock, admin.ReauthWindow)
	// Only owner identity goes into travel/sync; the actual administrator stays in audit.
	ownerActor := actor.Actor{AccountID: owner, ReauthenticatedAt: reauth}
	op := uuid.New()
	var result write.Result
	switch cmd.Action {
	case "edit":
		result, err = travel.Update(ctx, ownerActor, op, id, cmd.Version, cmd.Changes.Patch())
	case "archive":
		result, err = travel.SetArchived(ctx, ownerActor, op, id, cmd.Version, *cmd.Archived)
	case "trash":
		result, err = travel.Trash(ctx, ownerActor, op, id, cmd.Version)
	case "restore":
		result, err = travel.Restore(ctx, ownerActor, op, id, cmd.Version)
	case "purge":
		result, err = travel.Purge(ctx, ownerActor, op, id, cmd.Version, cmd.Confirm)
	case "retry":
		if before.PurgeRequestedAt == nil {
			return out, apperr.Conflicted("PURGE_NOT_REQUESTED", "尚未申请永久清理")
		}
		var unlocked bool
		if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "trip-objects:"+owner.String()+":"+id.String()).Scan(&unlocked); err != nil {
			return out, err
		}
		if !unlocked {
			return out, apperr.Conflicted("PURGE_BUSY", "清理或对象校验仍在执行，请稍后重试")
		}
		var jobID uuid.UUID
		err = tx.QueryRow(ctx, `UPDATE deletion_jobs SET status='queued',error_code=NULL,finished_at=NULL WHERE owner_account_id=$1 AND target_trip_id=$2 AND scope='trip' AND (status='failed' OR (status IN ('queued','running') AND NOT EXISTS(SELECT 1 FROM river_job r WHERE r.kind='trip_purge' AND r.args->>'job_id'=deletion_jobs.id::text AND r.state IN ('available','pending','running','retryable','scheduled')))) RETURNING id`, owner, id).Scan(&jobID)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, apperr.Conflicted("PURGE_NOT_RETRYABLE", "仅失败或已中断的清理任务可以重试")
		}
		if err == nil {
			err = scope.Enqueue(trip.PurgeJobArgs{JobID: jobID, AccountID: owner, TripID: id})
			scope.AddAffected(write.EntityRef{Type: trip.EntityTypeDeletionJob, ID: jobID})
			result.Data = before
		}
	}
	if err != nil {
		return out, err
	}
	if cmd.Action == "trash" || cmd.Action == "purge" || cmd.Action == "retry" {
		if _, err = tx.Exec(ctx, `DELETE FROM trip_shares WHERE account_id=$1 AND trip_id=$2`, owner, id); err != nil {
			return out, err
		}
	}
	after, ok := result.Data.(trip.Resource)
	if !ok {
		return out, errors.New("missing trip mutation result")
	}
	out.Trip = admin.OverviewOf(after)
	out.Warnings = scope.warnings
	for _, r := range scope.affected {
		if r.Type == trip.EntityTypeDeletionJob {
			jobID := r.ID
			out.JobID = &jobID
		}
	}
	audit.Details = map[string]any{"before": admin.OverviewOf(before), "after": out.Trip, "job_id": out.JobID, "warnings": out.Warnings}
	if err = insertAudit(ctx, tx, *audit); err != nil {
		return out, apperr.New(503, "ADMIN_AUDIT_UNAVAILABLE", "操作记录暂不可用，请稍后重试").WithCause(err)
	}
	seq, err := pgcore.RecordChanges(ctx, q, owner, op, lastSeq, scope.changes, now)
	if err != nil {
		return out, err
	}
	if seq != lastSeq {
		if _, err = tx.Exec(ctx, `UPDATE account_sync_state SET last_seq=$2,updated_at=$3 WHERE account_id=$1`, owner, seq, now); err != nil {
			return out, err
		}
	}
	for _, job := range scope.jobs {
		if s.queue == nil {
			return out, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "清理队列不可用")
		}
		if _, err = s.queue.InsertTx(ctx, tx, job.(river.JobArgs), &river.InsertOpts{MaxAttempts: 1}); err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}
