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
	"tripfolio/server/internal/modules/travel/share"
)

// ShareStore 实现 share.Store。分享不进入同步写事务，直接以连接池执行（设计 2.3）。
type ShareStore struct {
	q *dbgen.Queries
}

// NewShareStore 创建分享仓储。
func NewShareStore(pool *pgxpool.Pool) *ShareStore { return &ShareStore{q: dbgen.New(pool)} }

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
	row, err := s.q.GetTripShareByTrip(ctx, dbgen.GetTripShareByTripParams{AccountID: accountID, TripID: tripID})
	if errors.Is(err, pgx.ErrNoRows) {
		return share.Record{}, false, nil
	}
	if err != nil {
		return share.Record{}, false, err
	}
	return toShareRecord(row), true, nil
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
	row, err := s.q.InsertTripShare(ctx, dbgen.InsertTripShareParams{ID: r.ID, AccountID: r.AccountID, TripID: r.TripID, Token: r.Token, CreatedAt: r.CreatedAt})
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT DO NOTHING 时 RETURNING 无行：并发开启，由调用方重读。
		return share.Record{}, false, nil
	}
	if err != nil {
		return share.Record{}, false, err
	}
	return toShareRecord(row), true, nil
}

func (s *ShareStore) Rotate(ctx context.Context, accountID, tripID uuid.UUID, token string, now time.Time) (share.Record, bool, error) {
	at := now
	row, err := s.q.RotateTripShare(ctx, dbgen.RotateTripShareParams{Token: token, RotatedAt: &at, AccountID: accountID, TripID: tripID})
	if errors.Is(err, pgx.ErrNoRows) {
		return share.Record{}, false, nil
	}
	if err != nil {
		return share.Record{}, false, err
	}
	return toShareRecord(row), true, nil
}

func (s *ShareStore) Delete(ctx context.Context, accountID, tripID uuid.UUID) error {
	_, err := s.q.DeleteTripShare(ctx, dbgen.DeleteTripShareParams{AccountID: accountID, TripID: tripID})
	return err
}

func (s *ShareStore) RecordView(ctx context.Context, id uuid.UUID, now time.Time) error {
	at := now
	return s.q.RecordTripShareView(ctx, dbgen.RecordTripShareViewParams{ViewedAt: &at, ID: id})
}
