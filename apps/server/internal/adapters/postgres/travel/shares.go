package travelpg

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/account"
	"tripfolio/server/internal/modules/travel/share"
)

// ShareStore 实现 share.Store。分享不进入同步写事务，直接以连接池执行（设计 2.3）。
type ShareStore struct {
	q    *dbgen.Queries
	pool *pgxpool.Pool
}

// NewShareStore 创建分享仓储。
func NewShareStore(pool *pgxpool.Pool) *ShareStore {
	return &ShareStore{q: dbgen.New(pool), pool: pool}
}

var (
	_ share.Store           = (*ShareStore)(nil)
	_ share.TripSource      = (*TripReader)(nil)
	_ share.ItinerarySource = (*ItineraryReader)(nil)
)

func toShareRecord(row dbgen.TripShare) share.Record {
	return share.Record{
		ID: row.ID, AccountID: row.AccountID, TripID: row.TripID, Token: row.Token,
		CreatedAt: pgcore.UTC(row.CreatedAt), RotatedAt: pgcore.UTCPtr(row.RotatedAt), LastViewedAt: pgcore.UTCPtr(row.LastViewedAt), ViewCount: row.ViewCount,
	}
}

func (s *ShareStore) GetByTrip(ctx context.Context, accountID, tripID uuid.UUID) (share.Record, bool, error) {
	tx, err := s.lockSharing(ctx, accountID, tripID)
	if errors.Is(err, pgx.ErrNoRows) {
		return share.Record{}, false, nil
	}
	if err != nil {
		return share.Record{}, false, err
	}
	defer tx.Rollback(ctx)
	row, err := dbgen.New(tx).GetTripShareByTrip(ctx, dbgen.GetTripShareByTripParams{AccountID: accountID, TripID: tripID})
	if errors.Is(err, pgx.ErrNoRows) {
		return share.Record{}, false, nil
	}
	if err != nil {
		return share.Record{}, false, err
	}
	return toShareRecord(row), true, tx.Commit(ctx)
}

func (s *ShareStore) ResolveToken(ctx context.Context, token string) (share.Resolved, bool, error) {
	row, err := s.q.ResolveTripShareToken(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return share.Resolved{}, false, nil
	}
	if err != nil {
		return share.Resolved{}, false, err
	}
	rec := toShareRecord(dbgen.TripShare{
		ID: row.ID, AccountID: row.AccountID, TripID: row.TripID, Token: row.Token,
		CreatedAt: row.CreatedAt, RotatedAt: row.RotatedAt, LastViewedAt: row.LastViewedAt, ViewCount: row.ViewCount,
	})
	return share.Resolved{Record: rec, OwnerStatus: row.OwnerStatus, TripDeletedAt: pgcore.UTCPtr(row.TripDeletedAt)}, true, nil
}

func (s *ShareStore) Insert(ctx context.Context, r share.Record) (share.Record, bool, error) {
	tx, err := s.lockSharing(ctx, r.AccountID, r.TripID)
	if err != nil {
		return share.Record{}, false, err
	}
	defer tx.Rollback(ctx)
	row, err := dbgen.New(tx).InsertTripShare(ctx, dbgen.InsertTripShareParams{ID: r.ID, AccountID: r.AccountID, TripID: r.TripID, Token: r.Token, CreatedAt: r.CreatedAt})
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT DO NOTHING 时 RETURNING 无行：并发开启，由调用方重读。
		return share.Record{}, false, nil
	}
	if err != nil {
		return share.Record{}, false, err
	}
	return toShareRecord(row), true, tx.Commit(ctx)
}

func (s *ShareStore) Rotate(ctx context.Context, accountID, tripID uuid.UUID, token string, now time.Time) (share.Record, bool, error) {
	tx, err := s.lockSharing(ctx, accountID, tripID)
	if errors.Is(err, pgx.ErrNoRows) {
		return share.Record{}, false, nil
	}
	if err != nil {
		return share.Record{}, false, err
	}
	defer tx.Rollback(ctx)
	at := now
	row, err := dbgen.New(tx).RotateTripShare(ctx, dbgen.RotateTripShareParams{Token: token, RotatedAt: &at, AccountID: accountID, TripID: tripID})
	if errors.Is(err, pgx.ErrNoRows) {
		return share.Record{}, false, nil
	}
	if err != nil {
		return share.Record{}, false, err
	}
	return toShareRecord(row), true, tx.Commit(ctx)
}

func (s *ShareStore) Delete(ctx context.Context, accountID, tripID uuid.UUID) error {
	_, err := s.q.DeleteTripShare(ctx, dbgen.DeleteTripShareParams{AccountID: accountID, TripID: tripID})
	return err
}

func (s *ShareStore) RecordView(ctx context.Context, id uuid.UUID, now time.Time) error {
	at := now
	return s.q.RecordTripShareView(ctx, dbgen.RecordTripShareViewParams{ViewedAt: &at, ID: id})
}

// 账号锁与后台控制互斥；旅行锁把限制/解除和分享创建/轮换排成确定顺序。
func (s *ShareStore) lockSharing(ctx context.Context, accountID, tripID uuid.UUID) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (pgx.Tx, error) { _ = tx.Rollback(ctx); return nil, err }
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM accounts WHERE id=$1 FOR SHARE`, accountID).Scan(&status); err != nil {
		return fail(err)
	}
	if status != "active" {
		return fail(account.StatusError(status))
	}
	if a, ok := actor.FromContext(ctx); ok {
		var valid bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_sessions WHERE id=$1 AND account_id=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp())`, a.SessionID, accountID).Scan(&valid)
		if err != nil {
			return fail(err)
		}
		if !valid || a.AccountID != accountID {
			return fail(apperr.Unauthorized("SESSION_EXPIRED", "登录已失效，请重新登录"))
		}
	}
	var deleted *time.Time
	err = tx.QueryRow(ctx, `SELECT deleted_at FROM trips WHERE account_id=$1 AND id=$2 FOR UPDATE`, accountID, tripID).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return fail(pgx.ErrNoRows)
	}
	if err != nil {
		return fail(err)
	}
	if deleted != nil {
		return fail(apperr.Gone("TRIP_DELETED", "旅行已在回收站中"))
	}
	var restricted bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM trip_sharing_restrictions WHERE account_id=$1 AND trip_id=$2 AND restricted)`, accountID, tripID).Scan(&restricted)
	if err != nil {
		return fail(err)
	}
	if restricted {
		return fail(apperr.Forbidden("SHARING_RESTRICTED", "该旅行的分享已被管理员限制"))
	}
	return tx, nil
}
