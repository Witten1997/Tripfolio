// Package accountpg 是 account 模块的 PostgreSQL 适配。
package accountpg

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	financepg "tripfolio/server/internal/adapters/postgres/finance"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/modules/account"
)

// Store 实现 account.Store。
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewStore 创建存储。
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

var _ account.Store = (*Store)(nil)

func toAccount(row dbgen.Account) account.Account {
	var avatar *uuid.UUID
	if row.AvatarAssetID.Valid {
		id := row.AvatarAssetID.UUID
		avatar = &id
	}
	return account.Account{
		ID: row.ID, Email: row.Email, EmailKey: row.EmailKey, PasswordHash: row.PasswordHash, Nickname: row.Nickname,
		AvatarAssetID: avatar, DefaultTimezone: row.DefaultTimezone, Status: row.Status, Version: row.Version,
		CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt),
	}
}

func toSession(row dbgen.AccountSession) account.Session {
	return account.Session{
		ID: row.ID, AccountID: row.AccountID, ClientKind: actor.ClientKind(row.ClientKind), DeviceID: row.DeviceID, DeviceName: row.DeviceName,
		RefreshTokenHash: row.RefreshTokenHash, PreviousRefreshTokenHash: row.PreviousRefreshTokenHash, PreviousRotatedAt: pgcore.UTCPtr(row.PreviousRotatedAt),
		CSRFTokenHash: row.CsrfTokenHash, ReauthenticatedAt: pgcore.UTCPtr(row.ReauthenticatedAt),
		CreatedAt: pgcore.UTC(row.CreatedAt), LastSeenAt: pgcore.UTC(row.LastSeenAt), ExpiresAt: pgcore.UTC(row.ExpiresAt), RevokedAt: pgcore.UTCPtr(row.RevokedAt),
	}
}

func toChallenge(row dbgen.AuthChallenge) account.Challenge {
	return account.Challenge{
		ID: row.ID, Purpose: account.Purpose(row.Purpose), EmailKey: row.EmailKey, CodeMAC: row.CodeMac, KeyID: row.KeyID,
		Attempts: int(row.Attempts), DeliveryStatus: row.DeliveryStatus, CreatedAt: pgcore.UTC(row.CreatedAt), ExpiresAt: pgcore.UTC(row.ExpiresAt),
		ConsumedAt: pgcore.UTCPtr(row.ConsumedAt), InvalidatedAt: pgcore.UTCPtr(row.InvalidatedAt),
	}
}

func (s *Store) AccountByEmailKey(ctx context.Context, key string) (account.Account, bool, error) {
	row, err := s.q.GetAccountByEmailKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return account.Account{}, false, nil
	}
	if err != nil {
		return account.Account{}, false, err
	}
	return toAccount(row), true, nil
}

func (s *Store) AccountByID(ctx context.Context, id uuid.UUID) (account.Account, bool, error) {
	row, err := s.q.GetAccountByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return account.Account{}, false, nil
	}
	if err != nil {
		return account.Account{}, false, err
	}
	return toAccount(row), true, nil
}

func (s *Store) AccountExists(ctx context.Context, key string) (bool, error) {
	return s.q.AccountExistsByEmailKey(ctx, key)
}

// CreateAccount 在一个事务中创建账号、同步状态与预设分类（含账号级变更日志）。
func (s *Store) CreateAccount(ctx context.Context, a account.Account) (account.Account, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return account.Account{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	row, err := q.CreateAccount(ctx, dbgen.CreateAccountParams{
		ID: a.ID, Email: a.Email, EmailKey: a.EmailKey, EmailVerifiedAt: a.CreatedAt, PasswordHash: a.PasswordHash,
		Nickname: a.Nickname, DefaultTimezone: a.DefaultTimezone,
	})
	if err != nil {
		return account.Account{}, err
	}
	if err := q.CreateAccountSyncState(ctx, a.ID); err != nil {
		return account.Account{}, err
	}
	endSeq, err := financepg.SeedPresets(ctx, q, a.ID, uuid.New(), 0, a.CreatedAt)
	if err != nil {
		return account.Account{}, err
	}
	if err := q.AdvanceAccountSeq(ctx, dbgen.AdvanceAccountSeqParams{AccountID: a.ID, LastSeq: endSeq, UpdatedAt: a.CreatedAt}); err != nil {
		return account.Account{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return account.Account{}, err
	}
	return toAccount(row), nil
}

func (s *Store) UpdatePassword(ctx context.Context, id uuid.UUID, hash string, now time.Time) error {
	return s.q.UpdateAccountPassword(ctx, dbgen.UpdateAccountPasswordParams{ID: id, PasswordHash: hash, PasswordChangedAt: now})
}

func (s *Store) UpdateProfile(ctx context.Context, id uuid.UUID, nickname string, avatar *uuid.UUID, tz string, now time.Time) (account.Account, error) {
	var avatarID uuid.NullUUID
	if avatar != nil {
		avatarID = uuid.NullUUID{UUID: *avatar, Valid: true}
	}
	row, err := s.q.UpdateAccountProfile(ctx, dbgen.UpdateAccountProfileParams{ID: id, Nickname: nickname, AvatarAssetID: avatarID, DefaultTimezone: tz, UpdatedAt: now})
	if err != nil {
		return account.Account{}, err
	}
	return toAccount(row), nil
}

func (s *Store) SetAccountStatus(ctx context.Context, id uuid.UUID, status string, now time.Time) error {
	return s.q.SetAccountStatus(ctx, dbgen.SetAccountStatusParams{ID: id, Status: status, UpdatedAt: now})
}

func (s *Store) CreateSession(ctx context.Context, sess account.Session) (account.Session, error) {
	row, err := s.q.CreateSession(ctx, dbgen.CreateSessionParams{
		ID: sess.ID, AccountID: sess.AccountID, ClientKind: string(sess.ClientKind), DeviceID: sess.DeviceID, DeviceName: sess.DeviceName,
		RefreshTokenHash: sess.RefreshTokenHash, CsrfTokenHash: sess.CSRFTokenHash, ExpiresAt: sess.ExpiresAt, CreatedAt: sess.CreatedAt,
	})
	if err != nil {
		return account.Session{}, err
	}
	return toSession(row), nil
}

func (s *Store) SessionByID(ctx context.Context, id uuid.UUID) (account.Session, bool, error) {
	row, err := s.q.GetSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return account.Session{}, false, nil
	}
	if err != nil {
		return account.Session{}, false, err
	}
	return toSession(row), true, nil
}

func (s *Store) SessionWithAccount(ctx context.Context, id uuid.UUID) (account.Session, account.Account, bool, error) {
	row, err := s.q.GetSessionWithAccount(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return account.Session{}, account.Account{}, false, nil
	}
	if err != nil {
		return account.Session{}, account.Account{}, false, err
	}
	return toSession(row.AccountSession), toAccount(row.Account), true, nil
}

func (s *Store) RotateSessionTx(ctx context.Context, id uuid.UUID, fn func(account.Session) (account.SessionRotation, error)) (account.Session, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return account.Session{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	row, err := q.GetSessionForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return account.Session{}, account.ErrNoSession
	}
	if err != nil {
		return account.Session{}, err
	}
	sess := toSession(row)
	rot, err := fn(sess)
	if err != nil {
		return account.Session{}, err
	}
	switch rot.Kind {
	case "rotate":
		err = q.RotateSession(ctx, dbgen.RotateSessionParams{
			ID: id, RefreshTokenHash: rot.RefreshTokenHash, PreviousRefreshTokenHash: rot.PreviousRefreshTokenHash,
			PreviousRotatedAt: rot.PreviousRotatedAt, CsrfTokenHash: rot.CSRFTokenHash, LastSeenAt: rot.LastSeenAt,
		})
	case "reissue":
		err = q.ReissueSessionWithinGrace(ctx, dbgen.ReissueSessionWithinGraceParams{ID: id, RefreshTokenHash: rot.RefreshTokenHash, CsrfTokenHash: rot.CSRFTokenHash, LastSeenAt: rot.LastSeenAt})
	default:
		err = errors.New("未知的会话轮换类型 " + rot.Kind)
	}
	if err != nil {
		return account.Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return account.Session{}, err
	}
	sess.RefreshTokenHash = rot.RefreshTokenHash
	sess.PreviousRefreshTokenHash = rot.PreviousRefreshTokenHash
	sess.PreviousRotatedAt = rot.PreviousRotatedAt
	sess.CSRFTokenHash = rot.CSRFTokenHash
	sess.LastSeenAt = rot.LastSeenAt
	return sess, nil
}

func (s *Store) TouchSession(ctx context.Context, id uuid.UUID, now time.Time) error {
	return s.q.TouchSession(ctx, dbgen.TouchSessionParams{ID: id, LastSeenAt: now})
}

func (s *Store) SetReauthenticated(ctx context.Context, id uuid.UUID, now time.Time) error {
	return s.q.SetSessionReauthenticated(ctx, dbgen.SetSessionReauthenticatedParams{ID: id, ReauthenticatedAt: &now})
}

func (s *Store) RevokeSession(ctx context.Context, id uuid.UUID, now time.Time) error {
	return s.q.RevokeSession(ctx, dbgen.RevokeSessionParams{ID: id, RevokedAt: &now})
}

func (s *Store) RevokeAccountSessions(ctx context.Context, accountID uuid.UUID, now time.Time) error {
	return s.q.RevokeAccountSessions(ctx, dbgen.RevokeAccountSessionsParams{AccountID: accountID, RevokedAt: &now})
}

func (s *Store) RevokeOtherSessions(ctx context.Context, accountID, keep uuid.UUID, now time.Time) error {
	return s.q.RevokeOtherAccountSessions(ctx, dbgen.RevokeOtherAccountSessionsParams{AccountID: accountID, ID: keep, RevokedAt: &now})
}

func (s *Store) ListActiveSessions(ctx context.Context, accountID uuid.UUID, now time.Time) ([]account.Session, error) {
	rows, err := s.q.ListActiveSessions(ctx, dbgen.ListActiveSessionsParams{AccountID: accountID, ExpiresAt: now})
	if err != nil {
		return nil, err
	}
	out := make([]account.Session, 0, len(rows))
	for _, row := range rows {
		out = append(out, toSession(row))
	}
	return out, nil
}

// IssueChallengeTx 用事务级 advisory lock 串行化同邮箱、同用途的申请。
func (s *Store) IssueChallengeTx(ctx context.Context, c account.Challenge, resendAfter time.Duration, perHour int) (account.Challenge, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return account.Challenge{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", string(c.Purpose)+":"+c.EmailKey); err != nil {
		return account.Challenge{}, err
	}
	q := dbgen.New(tx)
	latest, err := q.LatestChallengeCreatedAt(ctx, dbgen.LatestChallengeCreatedAtParams{EmailKey: c.EmailKey, Purpose: string(c.Purpose)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return account.Challenge{}, err
	case c.CreatedAt.Sub(latest) < resendAfter:
		return account.Challenge{}, account.ErrChallengeTooSoon
	}
	n, err := q.CountChallengesSince(ctx, dbgen.CountChallengesSinceParams{EmailKey: c.EmailKey, Purpose: string(c.Purpose), CreatedAt: c.CreatedAt.Add(-time.Hour)})
	if err != nil {
		return account.Challenge{}, err
	}
	if int(n) >= perHour {
		return account.Challenge{}, account.ErrChallengeQuotaExceeded
	}
	if err := q.InvalidateChallenges(ctx, dbgen.InvalidateChallengesParams{EmailKey: c.EmailKey, Purpose: string(c.Purpose), InvalidatedAt: &c.CreatedAt}); err != nil {
		return account.Challenge{}, err
	}
	row, err := q.CreateChallenge(ctx, dbgen.CreateChallengeParams{ID: c.ID, Purpose: string(c.Purpose), EmailKey: c.EmailKey, CodeMac: c.CodeMAC, KeyID: c.KeyID, ExpiresAt: c.ExpiresAt, CreatedAt: c.CreatedAt})
	if err != nil {
		return account.Challenge{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return account.Challenge{}, err
	}
	return toChallenge(row), nil
}

// VerifyChallengeTx 在行锁下校验；失败计数与消费在本事务提交，不随外层业务回滚。
func (s *Store) VerifyChallengeTx(ctx context.Context, id uuid.UUID, fn func(account.Challenge) (bool, error)) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	row, err := q.GetChallengeForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("challenge not found")
	}
	if err != nil {
		return err
	}
	consume, verr := fn(toChallenge(row))
	if consume {
		now := time.Now().UTC()
		if err := q.ConsumeChallenge(ctx, dbgen.ConsumeChallengeParams{ID: id, ConsumedAt: &now}); err != nil {
			return err
		}
	} else if row.Attempts < 5 {
		if err := q.IncrementChallengeAttempts(ctx, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return verr
}

func (s *Store) SetChallengeDelivery(ctx context.Context, id uuid.UUID, status string) error {
	return s.q.SetChallengeDelivery(ctx, dbgen.SetChallengeDeliveryParams{ID: id, DeliveryStatus: status})
}
