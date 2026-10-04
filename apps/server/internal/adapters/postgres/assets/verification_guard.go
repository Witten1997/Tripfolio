package assetspg

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/modules/assets"
)

type VerificationGuard struct{ pool *pgxpool.Pool }

func NewVerificationGuard(pool *pgxpool.Pool) *VerificationGuard {
	return &VerificationGuard{pool: pool}
}
func (g *VerificationGuard) Acquire(ctx context.Context, args assets.VerifyJobArgs) (func(), error) {
	var tripID *uuid.UUID
	err := g.pool.QueryRow(ctx, `SELECT trip_id FROM assets WHERE account_id=$1 AND id=$2`, args.AccountID, args.AssetID).Scan(&tripID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, assets.ErrStaleJob
	}
	if err != nil {
		return nil, err
	}
	lockID := uuid.Nil
	if tripID != nil {
		lockID = *tripID
	}
	conn, release, err := pgcore.LockTripObjects(ctx, g.pool, args.AccountID, lockID)
	if err != nil {
		return nil, err
	}
	var valid bool
	err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets a JOIN accounts owner ON owner.id=a.account_id LEFT JOIN trips t ON t.account_id=a.account_id AND t.id=a.trip_id WHERE a.account_id=$1 AND a.id=$2 AND a.trip_id IS NOT DISTINCT FROM $3::uuid AND owner.status='active' AND (a.trip_id IS NULL OR t.purge_requested_at IS NULL))`, args.AccountID, args.AssetID, tripID).Scan(&valid)
	if err != nil {
		release()
		return nil, err
	}
	if !valid {
		release()
		return nil, assets.ErrStaleJob
	}
	return release, nil
}
