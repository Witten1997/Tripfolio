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
	if tripID == nil {
		return func() {}, nil
	}
	conn, release, err := pgcore.LockTripObjects(ctx, g.pool, args.AccountID, *tripID)
	if err != nil {
		return nil, err
	}
	var valid bool
	err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets a JOIN trips t ON t.account_id=a.account_id AND t.id=a.trip_id WHERE a.account_id=$1 AND a.id=$2 AND a.trip_id=$3 AND t.purge_requested_at IS NULL)`, args.AccountID, args.AssetID, *tripID).Scan(&valid)
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
