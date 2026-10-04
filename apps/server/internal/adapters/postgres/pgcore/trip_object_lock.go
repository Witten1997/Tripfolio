package pgcore

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

// Object I/O must be serialized with cleanup, including the verifier's final copy and thumbnail.
func LockTripObjects(ctx context.Context, pool *pgxpool.Pool, accountID, tripID uuid.UUID) (*pgxpool.Conn, func(), error) {
	return lockObjects(ctx, pool, accountID, &tripID)
}

// LockAccountObjects excludes every trip and avatar verifier while account cleanup does object I/O.
func LockAccountObjects(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID) (*pgxpool.Conn, func(), error) {
	return lockObjects(ctx, pool, accountID, nil)
}

func lockObjects(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID, tripID *uuid.UUID) (*pgxpool.Conn, func(), error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	accountKey := "account-objects:" + accountID.String()
	lock, unlock := "pg_advisory_lock", "pg_advisory_unlock"
	if tripID != nil {
		lock += "_shared"
		unlock += "_shared"
	}
	if _, err = conn.Exec(ctx, `SELECT `+lock+`(hashtextextended($1,0))`, accountKey); err != nil {
		_ = conn.Hijack().Close(context.WithoutCancel(ctx))
		return nil, nil, err
	}
	key := ""
	if tripID != nil {
		key = "trip-objects:" + accountID.String() + ":" + tripID.String()
		if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key); err != nil {
			_ = conn.Hijack().Close(context.WithoutCancel(ctx))
			return nil, nil, err
		}
	}
	release := func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if key != "" {
			if _, err := conn.Exec(c, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key); err != nil {
				_ = conn.Hijack().Close(c)
				return
			}
		}
		if _, err := conn.Exec(c, `SELECT `+unlock+`(hashtextextended($1,0))`, accountKey); err != nil {
			_ = conn.Hijack().Close(c)
			return
		}
		conn.Release()
	}
	return conn, release, nil
}
