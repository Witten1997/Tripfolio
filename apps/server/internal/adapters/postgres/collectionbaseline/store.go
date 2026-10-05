package collectionbaseline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	financepg "tripfolio/server/internal/adapters/postgres/finance"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	baseline "tripfolio/server/internal/modules/collectionbaseline"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ baseline.Reader = (*Store)(nil)

type view struct {
	scope *pgcore.TxScope
	actor actor.Actor
}

func (s *Store) Read(ctx context.Context, a actor.Actor, fn func(baseline.View) error) error {
	if s.pool == nil {
		return apperr.New(503, "DEPENDENCY_UNAVAILABLE", "集合读取暂不可用")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return apperr.Internal(err)
	}
	defer tx.Rollback(ctx)
	v := &view{scope: &pgcore.TxScope{Tx: tx, Queries: dbgen.New(tx), AccountID: a.AccountID}, actor: a}
	if err := fn(v); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

func (v *view) State(ctx context.Context) (baseline.State, error) {
	var st baseline.State
	var epoch uuid.NullUUID
	err := v.scope.Tx.QueryRow(ctx, `SELECT a.status, s.sync_epoch,
EXISTS(SELECT 1 FROM account_sessions ss WHERE ss.account_id=a.id AND ss.id=$2
  AND ss.client_kind=$3 AND ss.client_kind IN ('web','android','harmony')
  AND ss.revoked_at IS NULL AND ss.expires_at>CURRENT_TIMESTAMP)
FROM accounts a LEFT JOIN account_sync_state s ON s.account_id=a.id WHERE a.id=$1`,
		v.actor.AccountID, v.actor.SessionID, string(v.actor.ClientKind)).Scan(&st.AccountStatus, &epoch, &st.SessionActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, apperr.Unauthorized("SESSION_EXPIRED", "请重新登录")
	}
	if err != nil {
		return st, apperr.Internal(err)
	}
	st.Epoch = epoch.UUID
	return st, nil
}

func (v *view) trip(ctx context.Context, scope collectionguard.Scope) (uuid.UUID, types.Date, string, error) {
	parts := strings.Split(scope.ScopeID, "/")
	if len(parts) != 2 {
		return uuid.Nil, "", "", apperr.Unprocessable("INVALID_REFERENCE", "集合范围无效")
	}
	id, err := uuid.Parse(parts[0])
	day, dateErr := types.ParseDate(parts[1])
	if err != nil || dateErr != nil || id == uuid.Nil {
		return uuid.Nil, "", "", apperr.Unprocessable("INVALID_REFERENCE", "集合范围无效")
	}
	var currency string
	err = v.scope.Tx.QueryRow(ctx, `SELECT currency_code FROM trips
WHERE account_id=$1 AND id=$2 AND deleted_at IS NULL AND purge_requested_at IS NULL`, v.actor.AccountID, id).Scan(&currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", "", apperr.NotFound()
	}
	if err != nil {
		return uuid.Nil, "", "", apperr.Internal(err)
	}
	return id, day, currency, nil
}

func (v *view) Revision(ctx context.Context, epoch uuid.UUID, scope collectionguard.Scope) (write.ScopeRevision, error) {
	if scope.Kind == "categories" {
		if scope.ScopeID != v.actor.AccountID.String() {
			return write.ScopeRevision{}, apperr.NotFound()
		}
		return v.scope.CollectionRevision(ctx, epoch, scope.Kind, v.actor.AccountID)
	}
	if scope.Kind != "itinerary_day" && scope.Kind != "photo_day" {
		return write.ScopeRevision{}, apperr.Unprocessable("INVALID_REFERENCE", "集合范围无效")
	}
	id, day, _, err := v.trip(ctx, scope)
	if err != nil {
		return write.ScopeRevision{}, err
	}
	if scope.Kind == "itinerary_day" {
		return v.scope.ItineraryDayRevision(ctx, epoch, id, day)
	}
	return v.scope.PhotoDayRevision(ctx, epoch, id, day)
}

func (v *view) Entries(ctx context.Context, scope collectionguard.Scope, after uuid.UUID, limit int) ([]baseline.Entry, error) {
	if limit < 1 || limit > 101 {
		return nil, apperr.Unprocessable("INVALID_REFERENCE", "集合页大小无效")
	}
	if scope.Kind == "categories" {
		if scope.ScopeID != v.actor.AccountID.String() {
			return nil, apperr.NotFound()
		}
		rows, err := v.scope.Tx.Query(ctx, `SELECT * FROM expense_categories
WHERE account_id=$1 AND deleted_at IS NULL AND id>$2 ORDER BY id LIMIT $3`, v.actor.AccountID, after, limit)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		return collect(rows, func(row dbgen.ExpenseCategory) (baseline.Entry, error) {
			return entry(row.ID, row.Version, financepg.SyncCategory(row))
		})
	}
	if scope.Kind != "itinerary_day" && scope.Kind != "photo_day" {
		return nil, apperr.Unprocessable("INVALID_REFERENCE", "集合范围无效")
	}
	id, day, currency, err := v.trip(ctx, scope)
	if err != nil {
		return nil, err
	}
	if scope.Kind == "itinerary_day" {
		rows, err := v.scope.Tx.Query(ctx, `SELECT * FROM itinerary_items
WHERE account_id=$1 AND trip_id=$2 AND scheduled_on=$3 AND deleted_at IS NULL AND id>$4 ORDER BY id LIMIT $5`, v.actor.AccountID, id, day.Time(), after, limit)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		return collect(rows, func(row dbgen.ItineraryItem) (baseline.Entry, error) {
			resource, err := travelpg.ToItineraryResource(row, currency)
			if err != nil {
				return baseline.Entry{}, err
			}
			return entry(row.ID, row.Version, resource)
		})
	}
	rows, err := v.scope.Tx.Query(ctx, `SELECT * FROM photos
WHERE account_id=$1 AND trip_id=$2 AND recorded_on=$3 AND deleted_at IS NULL AND id>$4 ORDER BY id LIMIT $5`, v.actor.AccountID, id, day.Time(), after, limit)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return collect(rows, func(row dbgen.Photo) (baseline.Entry, error) {
		resource, err := travelpg.SyncPhoto(row)
		if err != nil {
			return baseline.Entry{}, err
		}
		return entry(row.ID, row.Version, resource)
	})
}

func collect[T any](rows pgx.Rows, project func(T) (baseline.Entry, error)) ([]baseline.Entry, error) {
	values, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]baseline.Entry, 0, len(values))
	for _, value := range values {
		e, err := project(value)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, e)
	}
	return out, nil
}

func entry(id uuid.UUID, version int64, resource any) (baseline.Entry, error) {
	data, err := json.Marshal(resource)
	return baseline.Entry{ID: id, Version: version, Data: data}, err
}
