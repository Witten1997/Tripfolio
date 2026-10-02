package adminpg

import (
	"context"

	"github.com/jackc/pgx/v5"
	"tripfolio/server/internal/modules/admin"
)

func (s *Store) Audits(ctx context.Context, f admin.AuditFilter) (admin.AuditPage, error) {
	out := admin.AuditPage{Data: []admin.AuditEntry{}, Page: f.Page, PageSize: f.PageSize, AsOf: f.AsOf}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	const source = ` FROM admin_audit_events WHERE ($1::uuid IS NULL OR actor_account_id=$1)
		AND ($2::uuid IS NULL OR subject_account_id=$2 OR details->'owner_ids' @> jsonb_build_array($2::text)
			OR (action='user.list' AND details->'resource_ids' @> jsonb_build_array($2::text)))
		AND ($3='' OR action=$3) AND ($4='' OR result=$4)
		AND (nullif($5,'')::date IS NULL OR occurred_at>=nullif($5,'')::date::timestamp AT TIME ZONE 'Asia/Shanghai')
		AND (nullif($6,'')::date IS NULL OR occurred_at<(nullif($6,'')::date+1)::timestamp AT TIME ZONE 'Asia/Shanghai') AND occurred_at<=$7`
	args := []any{f.ActorID, f.SubjectID, f.Action, f.Result, f.DateFrom, f.DateTo, f.AsOf}
	if err = tx.QueryRow(ctx, `SELECT count(*)`+source, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	// 不读取自由文本原因、浏览器标识与 details 原文。数字字段也限制类型及长度。
	rows, err := tx.Query(ctx, `SELECT id,actor_account_id,subject_account_id,resource_id,action,resource_type,result,request_id,source_ip,occurred_at,
		CASE WHEN jsonb_typeof(details->'result_count')='number' AND details->>'result_count' ~ '^[0-9]{1,9}$' THEN (details->>'result_count')::bigint ELSE NULL END`+source+` ORDER BY occurred_at DESC,id DESC LIMIT $8 OFFSET $9`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v admin.AuditEntry
		if err = rows.Scan(&v.ID, &v.ActorID, &v.SubjectID, &v.ResourceID, &v.Action, &v.ResourceType, &v.Result, &v.RequestID, &v.SourceIP, &v.OccurredAt, &v.ResultCount); err != nil {
			rows.Close()
			return out, err
		}
		out.Data = append(out.Data, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	return out, tx.Commit(ctx)
}

func (s *Store) JobCounts(ctx context.Context) (admin.JobCounts, error) {
	var out admin.JobCounts
	err := s.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE state='running'), count(*) FILTER (WHERE state IN ('available','scheduled','pending')),
		count(*) FILTER (WHERE state='retryable'),count(*) FILTER (WHERE state='discarded'),
		(SELECT count(*) FROM deletion_jobs WHERE status IN ('queued','running')),
		(SELECT count(*) FROM deletion_jobs WHERE status='failed') FROM river_job`).Scan(&out.Running, &out.Pending, &out.Retryable, &out.Discarded, &out.DeletionPending, &out.DeletionFailed)
	return out, err
}

func (s *Store) Jobs(ctx context.Context, f admin.JobFilter) (admin.JobPage, error) {
	out := admin.JobPage{Data: []admin.Job{}, Page: f.Page, PageSize: f.PageSize}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	const source = ` FROM river_job WHERE state IN ('retryable','discarded') AND ($1='' OR state::text=$1)`
	if err = tx.QueryRow(ctx, `SELECT count(*)`+source, f.State).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,kind,state::text,attempt,max_attempts,created_at,attempted_at,scheduled_at,
		left(coalesce(errors[array_length(errors,1)]->>'error',''),4096)`+source+` ORDER BY coalesce(attempted_at,created_at) DESC,id DESC LIMIT $2 OFFSET $3`, f.State, f.PageSize, (f.Page-1)*f.PageSize)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v admin.Job
		if err = rows.Scan(&v.ID, &v.Kind, &v.State, &v.Attempt, &v.MaxAttempts, &v.CreatedAt, &v.AttemptedAt, &v.ScheduledAt, &v.ErrorSummary); err != nil {
			rows.Close()
			return out, err
		}
		out.Data = append(out.Data, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	return out, tx.Commit(ctx)
}

func (s *Store) DeletionJobs(ctx context.Context, f admin.JobFilter) (admin.DeletionJobPage, error) {
	out := admin.DeletionJobPage{Data: []admin.DeletionJob{}, Page: f.Page, PageSize: f.PageSize}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	const source = ` FROM deletion_jobs WHERE ($1='' OR status=$1)`
	if err = tx.QueryRow(ctx, `SELECT count(*)`+source, f.State).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT id,owner_account_id,target_trip_id,scope,status,stage,processed_items,total_items,created_at,finished_at,coalesce(error_code,'')`+source+` ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, f.State, f.PageSize, (f.Page-1)*f.PageSize)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v admin.DeletionJob
		if err = rows.Scan(&v.ID, &v.OwnerID, &v.TripID, &v.Scope, &v.Status, &v.Stage, &v.ProcessedItems, &v.TotalItems, &v.CreatedAt, &v.FinishedAt, &v.ErrorSummary); err != nil {
			rows.Close()
			return out, err
		}
		out.Data = append(out.Data, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	return out, tx.Commit(ctx)
}
