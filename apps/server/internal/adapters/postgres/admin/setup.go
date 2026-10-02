package adminpg

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	financepg "tripfolio/server/internal/adapters/postgres/finance"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/admin"
)

func (s *Store) SetupOpen(ctx context.Context) (bool, error) {
	var open bool
	err := s.pool.QueryRow(ctx, `SELECT completed_at IS NULL FROM admin_setup WHERE id=1`).Scan(&open)
	// 状态行缺失时保持关闭，不自动创建新的初始化机会。
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return open, err
}

func (s *Store) InitializeAdmin(ctx context.Context, cmd admin.SetupCommand, audit admin.Audit) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// 与 CLI、账号状态及管理资格变更共享锁顺序。
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(781241,18)`); err != nil {
		return err
	}
	var open bool
	err = tx.QueryRow(ctx, `SELECT completed_at IS NULL FROM admin_setup WHERE id=1 FOR UPDATE`).Scan(&open)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !open) {
		return apperr.NotFound()
	}
	if err != nil {
		return err
	}
	a := cmd.Account
	q := dbgen.New(tx)
	if cmd.Create {
		_, err = q.CreateAccount(ctx, dbgen.CreateAccountParams{ID: a.ID, Email: a.Email, EmailKey: a.EmailKey,
			EmailVerifiedAt: a.CreatedAt, PasswordHash: a.PasswordHash, Nickname: a.Nickname, DefaultTimezone: a.DefaultTimezone})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return apperr.Conflicted("ACCOUNT_ALREADY_EXISTS", "该邮箱已注册，请选择使用已有账号")
			}
			return err
		}
		if err = q.CreateAccountSyncState(ctx, a.ID); err != nil {
			return err
		}
		seq, err := financepg.SeedPresets(ctx, q, a.ID, uuid.New(), 0, a.CreatedAt)
		if err != nil {
			return err
		}
		if err = q.AdvanceAccountSeq(ctx, dbgen.AdvanceAccountSeqParams{AccountID: a.ID, LastSeq: seq, UpdatedAt: a.CreatedAt}); err != nil {
			return err
		}
	} else {
		var hash, status string
		var version int64
		err = tx.QueryRow(ctx, `SELECT password_hash,status,version FROM accounts WHERE id=$1 AND email_key=$2 FOR UPDATE`, a.ID, a.EmailKey).Scan(&hash, &status, &version)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (hash != a.PasswordHash || status != "active" || version != a.Version)) {
			return apperr.Unauthorized("ADMIN_INVALID_CREDENTIALS", "账号状态已变化，请重新验证后提交")
		}
		if err != nil {
			return err
		}
	}
	// 数据库触发器与资格插入同时完成一次性关闭；任何后续错误均回滚。
	if _, err = tx.Exec(ctx, `INSERT INTO admin_principals(account_id,reason,granted_at) VALUES($1,$2,$3)`, a.ID, audit.Reason, audit.OccurredAt); err != nil {
		return err
	}
	if err = insertAudit(ctx, tx, audit); err != nil {
		return apperr.New(503, "ADMIN_AUDIT_UNAVAILABLE", "操作记录暂不可用，请稍后重试").WithCause(err)
	}
	return tx.Commit(ctx)
}
