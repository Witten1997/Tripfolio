// Package adminpg 是后台模块的 PostgreSQL 适配器。
package adminpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accountpg "tripfolio/server/internal/adapters/postgres/account"
	"tripfolio/server/internal/modules/account"
	"tripfolio/server/internal/modules/admin"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ admin.Store = (*Store)(nil)

func (s *Store) Credentials(ctx context.Context, email string) (account.Account, bool, error) {
	acc, found, err := accountpg.NewStore(s.pool).AccountByEmailKey(ctx, email)
	if err != nil || !found {
		return acc, false, err
	}
	var enabled bool
	err = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM admin_principals WHERE account_id=$1 AND revoked_at IS NULL)`, acc.ID).Scan(&enabled)
	return acc, enabled, err
}

func insertAudit(ctx context.Context, tx pgx.Tx, a admin.Audit) error {
	details := a.Details
	if details == nil {
		details = map[string]any{}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_audit_events
		(id,actor_account_id,subject_account_id,session_id,action,resource_type,resource_id,result,reason,request_id,source_ip,user_agent,details,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		a.ID, a.ActorID, a.SubjectID, a.SessionID, a.Action, a.ResourceType, a.ResourceID, a.Result, a.Reason, a.Request.RequestID, a.Request.IP, a.Request.UserAgent, raw, a.OccurredAt)
	return err
}

func (s *Store) Audit(ctx context.Context, a admin.Audit) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := insertAudit(ctx, tx, a); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ChangePrincipal 仅供受控 CLI 使用；资格变更、会话撤销与审计同事务提交。
func (s *Store) ChangePrincipal(ctx context.Context, email, reason string, grant bool) (uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	// 串行化资格变更，避免并发撤销绕过最后一个超级管理员保护。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(781241,18)`); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	var status string
	err = tx.QueryRow(ctx, `SELECT id,status FROM accounts WHERE email_key=$1 AND email_verified_at IS NOT NULL FOR UPDATE`, email).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("账号不存在或邮箱尚未验证")
	}
	if err != nil {
		return uuid.Nil, err
	}
	if grant && status != "active" {
		return uuid.Nil, fmt.Errorf("只能授予正常账号超级管理员资格")
	}
	now := time.Now().UTC()
	action := "principal.grant"
	if grant {
		_, err = tx.Exec(ctx, `INSERT INTO admin_principals(account_id,reason,granted_at) VALUES($1,$2,$3)
			ON CONFLICT(account_id) DO UPDATE SET version=admin_principals.version+1,revoked_at=NULL,granted_at=$3,reason=$2
			WHERE admin_principals.revoked_at IS NOT NULL`, id, reason, now)
	} else {
		action = "principal.revoke"
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_principals WHERE account_id=$1 AND revoked_at IS NULL)`, id).Scan(&active); err != nil {
			return uuid.Nil, err
		}
		if !active {
			return uuid.Nil, fmt.Errorf("该账号没有有效的超级管理员资格")
		}
		var others int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM admin_principals p JOIN accounts a ON a.id=p.account_id WHERE p.revoked_at IS NULL AND a.status='active' AND p.account_id<>$1`, id).Scan(&others); err != nil {
			return uuid.Nil, err
		}
		if others == 0 {
			return uuid.Nil, fmt.Errorf("不能撤销最后一个可用超级管理员")
		}
		_, err = tx.Exec(ctx, `UPDATE admin_principals SET revoked_at=$2,version=version+1,reason=$3 WHERE account_id=$1`, id, now, reason)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE admin_sessions SET revoked_at=$2 WHERE account_id=$1 AND revoked_at IS NULL`, id, now)
		}
	}
	if err != nil {
		return uuid.Nil, err
	}
	if err := insertAudit(ctx, tx, admin.Audit{ID: uuid.New(), SubjectID: &id, ResourceType: "admin_principal", ResourceID: &id, Action: action, Result: "success", Reason: reason, Details: map[string]any{"source": "local_cli"}, OccurredAt: now}); err != nil {
		return uuid.Nil, err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) OpenSession(ctx context.Context, sess admin.Session, hash []byte, passwordHash string, audit admin.Audit) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	// 锁住资格和账号直到会话落库，阻止验密与创建会话之间的撤权/改密竞态。
	var version int64
	var changed time.Time
	err = tx.QueryRow(ctx, `SELECT p.version,a.password_changed_at FROM admin_principals p JOIN accounts a ON a.id=p.account_id
		WHERE a.id=$1 AND a.password_hash=$2 AND a.status='active' AND p.revoked_at IS NULL FOR SHARE OF p,a`, sess.AccountID, passwordHash).Scan(&version, &changed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_sessions(id,account_id,principal_version,token_hash,password_changed_at,created_at,last_seen_at,expires_at,reauthenticated_at,source_ip,user_agent)
		VALUES($1,$2,$3,$4,$5,$6,$6,$7,$6,$8,$9)`, sess.ID, sess.AccountID, version, hash, changed, sess.CreatedAt, sess.ExpiresAt, sess.IP, sess.UserAgent)
	if err != nil {
		return false, err
	}
	if err = insertAudit(ctx, tx, audit); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Store) Authenticate(ctx context.Context, hash []byte, now time.Time, idle time.Duration) (admin.Session, bool, error) {
	var sess admin.Session
	err := s.pool.QueryRow(ctx, `UPDATE admin_sessions s SET last_seen_at=$2 FROM admin_principals p,accounts a
		WHERE s.token_hash=$1 AND s.account_id=p.account_id AND a.id=s.account_id AND p.revoked_at IS NULL
		AND p.version=s.principal_version AND a.status='active' AND a.password_changed_at=s.password_changed_at
		AND s.revoked_at IS NULL AND s.expires_at>$2 AND s.last_seen_at>$3
		RETURNING s.id,s.account_id,a.email,a.nickname,s.created_at,s.last_seen_at,s.expires_at,s.reauthenticated_at,s.source_ip,s.user_agent`, hash, now, now.Add(-idle)).Scan(
		&sess.ID, &sess.AccountID, &sess.Email, &sess.Nickname, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt, &sess.ReauthenticatedAt, &sess.IP, &sess.UserAgent)
	if errors.Is(err, pgx.ErrNoRows) {
		return sess, false, nil
	}
	return sess, err == nil, err
}

func (s *Store) Sessions(ctx context.Context, id uuid.UUID, now time.Time, idle time.Duration) ([]admin.Session, error) {
	rows, err := s.pool.Query(ctx, `SELECT s.id,s.account_id,s.created_at,s.last_seen_at,s.expires_at,s.source_ip,s.user_agent
		FROM admin_sessions s JOIN admin_principals p ON p.account_id=s.account_id JOIN accounts a ON a.id=s.account_id
		WHERE s.account_id=$1 AND s.revoked_at IS NULL AND s.expires_at>$2 AND s.last_seen_at>$3
		AND p.revoked_at IS NULL AND p.version=s.principal_version AND a.status='active' AND a.password_changed_at=s.password_changed_at
		ORDER BY s.last_seen_at DESC,s.id DESC`, id, now, now.Add(-idle))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []admin.Session{}
	for rows.Next() {
		var item admin.Session
		if err := rows.Scan(&item.ID, &item.AccountID, &item.CreatedAt, &item.LastSeenAt, &item.ExpiresAt, &item.IP, &item.UserAgent); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RevokeSession(ctx context.Context, accountID, id uuid.UUID, audit admin.Audit) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE admin_sessions SET revoked_at=$3 WHERE account_id=$1 AND id=$2 AND revoked_at IS NULL`, accountID, id, audit.OccurredAt)
	if err != nil {
		return err
	}
	if err = insertAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Reauthenticate(ctx context.Context, accountID, id uuid.UUID, passwordHash string, audit admin.Audit) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE admin_sessions s SET reauthenticated_at=$4 FROM accounts a,admin_principals p
		WHERE s.id=$2 AND s.account_id=$1 AND a.id=s.account_id AND a.password_hash=$3 AND a.password_changed_at=s.password_changed_at
		AND p.account_id=a.id AND p.revoked_at IS NULL AND p.version=s.principal_version AND a.status='active'
		AND s.revoked_at IS NULL AND s.expires_at>$4 AND s.last_seen_at>$5`, accountID, id, passwordHash, audit.OccurredAt, audit.OccurredAt.Add(-admin.SessionIdle))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err = insertAudit(ctx, tx, audit); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
