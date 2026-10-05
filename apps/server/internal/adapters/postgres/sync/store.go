package sync

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	syncmodule "tripfolio/server/internal/modules/sync"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type view struct {
	q     *dbgen.Queries
	actor actor.Actor
}

func (s *Store) Read(ctx context.Context, a actor.Actor, fn func(syncmodule.View) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return apperr.Internal(err)
	}
	defer tx.Rollback(ctx)
	if err := fn(&view{q: dbgen.New(tx), actor: a}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

func (v *view) State(ctx context.Context) (syncmodule.State, error) {
	r, err := v.q.GetSyncReadState(ctx, dbgen.GetSyncReadStateParams{AccountID: v.actor.AccountID, ID: v.actor.SessionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return syncmodule.State{}, apperr.Unauthorized("SESSION_EXPIRED", "请重新登录")
	}
	if err != nil {
		return syncmodule.State{}, apperr.Internal(err)
	}
	return syncmodule.State{Epoch: r.SyncEpoch, LastSeq: r.LastSeq, RetainedAfterSeq: r.RetainedAfterSeq, AccountStatus: r.Status, SessionActive: r.SessionActive}, nil
}

func (v *view) Changes(ctx context.Context, after, through int64, limit int) ([]syncmodule.Event, error) {
	rows, err := v.q.ListSyncChanges(ctx, dbgen.ListSyncChangesParams{AccountID: v.actor.AccountID, Seq: after, Seq_2: through, Limit: int32(limit)})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	result := make([]syncmodule.Event, 0, len(rows))
	for _, row := range rows {
		e := syncmodule.Event{Seq: row.Seq, BatchEndSeq: row.BatchEndSeq, Version: row.EntityVersion, BatchID: row.BatchID, EntityID: row.EntityID,
			EntityType: row.EntityType, Kind: row.ChangeKind, SchemaVersion: int(row.SchemaVersion), RequiresSnapshot: row.RequiresSnapshot,
			ChangedFields: row.ChangedFields, Data: row.Snapshot}
		if row.TripID.Valid {
			id := row.TripID.UUID
			e.TripID = &id
		}
		result = append(result, e)
	}
	return result, nil
}
