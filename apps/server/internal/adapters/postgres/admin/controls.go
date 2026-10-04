package adminpg

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/admin"
)

func controlError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.Code == "P0019" {
			return apperr.Conflicted("ADMIN_LAST_AVAILABLE", "不能封禁、撤权或注销最后一个可用超级管理员")
		}
		if pg.Code == "40001" || pg.Code == "40P01" {
			return apperr.Conflicted("ADMIN_CONCURRENT_CHANGE", "状态已发生并发变更，请刷新后重试")
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound()
	}
	return err
}

func auditedControl(ctx context.Context, tx pgx.Tx, a admin.Audit) error {
	if err := insertAudit(ctx, tx, a); err != nil {
		return apperr.New(503, "ADMIN_AUDIT_UNAVAILABLE", "操作记录暂不可用，请稍后重试").WithCause(err)
	}
	return tx.Commit(ctx)
}

// 所有后台控制先资格全局锁，再按 UUID 排序锁操作者/所属账号，再旅行、会话。
// 锁后重新检查密码复验及当前权限，不信任 HTTP 中间件的早期快照。
func lockControl(ctx context.Context, tx pgx.Tx, sess admin.Session, subject uuid.UUID, sensitive bool) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(781241,18)`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM accounts WHERE id=$1 OR id=$2 ORDER BY id FOR UPDATE`, sess.AccountID, subject)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var reauthenticated *time.Time
	err = tx.QueryRow(ctx, `SELECT s.reauthenticated_at FROM admin_sessions s JOIN accounts a ON a.id=s.account_id
		JOIN admin_principals p ON p.account_id=a.id WHERE s.id=$1 AND s.account_id=$2
		AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND s.last_seen_at>clock_timestamp()-interval '30 minutes'
		AND p.revoked_at IS NULL AND p.version=s.principal_version AND a.status='active'
		AND a.password_changed_at=s.password_changed_at FOR SHARE OF p,s`, sess.ID, sess.AccountID).Scan(&reauthenticated)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.Unauthorized("ADMIN_SESSION_EXPIRED", "后台登录已失效，请重新登录")
	}
	if err != nil {
		return err
	}
	if sensitive && (reauthenticated == nil || time.Since(*reauthenticated) > admin.ReauthWindow) {
		return apperr.Forbidden("ADMIN_REAUTH_REQUIRED", "请再次输入密码后重试")
	}
	return nil
}

func (s *Store) ControlUser(ctx context.Context, sess admin.Session, id uuid.UUID, action string, a admin.Audit) (err error) {
	defer func() { err = controlError(err) }()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockControl(ctx, tx, sess, id, true); err != nil {
		return err
	}
	var before string
	if err = tx.QueryRow(ctx, `SELECT status FROM accounts WHERE id=$1`, id).Scan(&before); err != nil {
		return err
	}
	after := before
	switch action {
	case "ban":
		if before == "deleting" {
			return apperr.Conflicted("ADMIN_ACCOUNT_STATE", "注销中的账号不能封禁")
		}
		after = "banned"
	case "unban":
		if before != "banned" {
			return apperr.Conflicted("ADMIN_ACCOUNT_STATE", "只能解封已封禁的账号")
		}
		after = "active"
	case "force-logout":
	default:
		return apperr.BadRequest("MALFORMED_REQUEST", "用户操作无效")
	}
	// version 也隔离密码验证后、会话创建前发生的强制下线。
	if _, err = tx.Exec(ctx, `UPDATE accounts SET status=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, after); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE account_sessions SET revoked_at=clock_timestamp() WHERE account_id=$1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE admin_sessions SET revoked_at=clock_timestamp() WHERE account_id=$1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if action != "force-logout" {
		if _, err = tx.Exec(ctx, `DELETE FROM trip_shares WHERE account_id=$1`, id); err != nil {
			return err
		}
	}
	a.Details = map[string]any{"before_status": before, "after_status": after}
	return auditedControl(ctx, tx, a)
}

func sharingState(ctx context.Context, tx pgx.Tx, id, owner uuid.UUID) (admin.SharingRestriction, error) {
	r := admin.SharingRestriction{TripID: id, AccountID: owner}
	err := tx.QueryRow(ctx, `SELECT restricted,reason,changed_at,version FROM trip_sharing_restrictions WHERE account_id=$1 AND trip_id=$2`, owner, id).Scan(&r.Restricted, &r.Reason, &r.ChangedAt, &r.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return r, err
}

func (s *Store) sharingControlTx(ctx context.Context, sess admin.Session, id uuid.UUID, in *admin.SharingControl, a admin.Audit) (result admin.SharingRestriction, err error) {
	defer func() { err = controlError(err) }()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var owner uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT account_id FROM trips WHERE id=$1`, id).Scan(&owner); err != nil {
		return result, err
	}
	result.TripID, result.AccountID = id, owner
	if err = lockControl(ctx, tx, sess, owner, in != nil); err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `SELECT account_id FROM trips WHERE id=$1 AND account_id=$2 FOR UPDATE`, id, owner).Scan(&owner); err != nil {
		return result, err
	}
	result, err = sharingState(ctx, tx, id, owner)
	if err != nil {
		return result, err
	}
	a.SubjectID = &owner
	if in != nil {
		var status string
		if err = tx.QueryRow(ctx, `SELECT status FROM accounts WHERE id=$1`, owner).Scan(&status); err != nil {
			return result, err
		}
		if status == "deleting" {
			return result, apperr.Forbidden("ACCOUNT_DELETING", "账号正在注销")
		}
		if in.Version != result.Version {
			return result, apperr.New(412, "VERSION_CONFLICT", "分享限制已被修改，请刷新后重试")
		}
		a.Details = map[string]any{"before_restricted": result.Restricted, "after_restricted": in.Restricted, "before_version": result.Version, "after_version": result.Version + 1}
		_, err = tx.Exec(ctx, `INSERT INTO trip_sharing_restrictions(account_id,trip_id,restricted,reason,changed_by,changed_at,version)
			VALUES($1,$2,$3,$4,$5,clock_timestamp(),1) ON CONFLICT(account_id,trip_id) DO UPDATE
			SET restricted=$3,reason=$4,changed_by=$5,changed_at=clock_timestamp(),version=trip_sharing_restrictions.version+1`, owner, id, in.Restricted, in.Reason, sess.AccountID)
		if err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM trip_shares WHERE account_id=$1 AND trip_id=$2`, owner, id); err != nil {
			return result, err
		}
		result, err = sharingState(ctx, tx, id, owner)
		if err != nil {
			return result, err
		}
	}
	return result, auditedControl(ctx, tx, a)
}

func (s *Store) SharingRestriction(ctx context.Context, sess admin.Session, id uuid.UUID, a admin.Audit) (admin.SharingRestriction, error) {
	return s.sharingControlTx(ctx, sess, id, nil, a)
}
func (s *Store) ControlSharing(ctx context.Context, sess admin.Session, id uuid.UUID, in admin.SharingControl, a admin.Audit) (admin.SharingRestriction, error) {
	return s.sharingControlTx(ctx, sess, id, &in, a)
}
