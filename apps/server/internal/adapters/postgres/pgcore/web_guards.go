package pgcore

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/types"
)

// WebPolicy separates sticky REST protection from epoch-bound native admission.
// An absent capability row is disabled. A missing table is an error, not legacy mode.
type WebPolicy struct {
	CollectionGuardsRequired bool
	Epoch                    uuid.UUID
	V2EnabledEpoch           *uuid.UUID
}

func (p WebPolicy) V2Enabled() bool {
	return p.CollectionGuardsRequired && p.Epoch != uuid.Nil && p.V2EnabledEpoch != nil && *p.V2EnabledEpoch == p.Epoch
}

// WebPolicy reads under the writer's account lock. It does not activate anything.
func (s *TxScope) WebPolicy(ctx context.Context) (WebPolicy, error) {
	var policy WebPolicy
	err := s.Tx.QueryRow(ctx, `SELECT s.sync_epoch,COALESCE(c.collection_guards_required,false),c.v2_enabled_epoch
FROM account_sync_state s LEFT JOIN account_sync_capabilities c ON c.account_id=s.account_id
WHERE s.account_id=$1`, s.AccountID).Scan(&policy.Epoch, &policy.CollectionGuardsRequired, &policy.V2EnabledEpoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return policy, apperr.NotFound()
	}
	if err != nil {
		return policy, apperr.Internal(err)
	}
	return policy, nil
}

// requireWebGuards is monotonic; deactivation must never downgrade REST protection.
// Activation remains intentionally absent until service-side prerequisites are wired.
func (s *TxScope) requireWebGuards(ctx context.Context) error {
	_, err := s.Tx.Exec(ctx, `INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)
ON CONFLICT(account_id) DO UPDATE SET collection_guards_required=true`, s.AccountID)
	return err
}

func (s *TxScope) disableSyncV2(ctx context.Context) error {
	_, err := s.Tx.Exec(ctx, `UPDATE account_sync_capabilities SET v2_enabled_epoch=NULL,enabled_at=NULL WHERE account_id=$1`, s.AccountID)
	return err
}

// RequireCollections is for REST service callbacks, before any writes or no-op.
// No service calls it yet; merely merging this foundation does not protect endpoints.
func (s *TxScope) RequireCollections(ctx context.Context, required []collectionguard.Scope) error {
	policy, err := s.WebPolicy(ctx)
	if err != nil {
		return err
	}
	return collectionguard.Check(ctx, policy.CollectionGuardsRequired, s.AccountID, collectionguard.Request(ctx), required, s.collectionRevision)
}

// CheckCollections accepts guards resolved by a native adapter, including receipt
// references. It never bypasses a check based on actor.ClientKind or request headers.
func (s *TxScope) CheckCollections(ctx context.Context, guards []collectionguard.Guard, required []collectionguard.Scope) error {
	return collectionguard.Check(ctx, true, s.AccountID, guards, required, s.collectionRevision)
}

func (s *TxScope) collectionRevision(ctx context.Context, scope collectionguard.Scope) (string, error) {
	policy, err := s.WebPolicy(ctx)
	if err != nil {
		return "", err
	}
	parts := strings.Split(scope.ScopeID, "/")
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return "", apperr.Unprocessable("INVALID_REFERENCE", "集合范围无效")
	}
	if scope.Kind == "categories" {
		if id != s.AccountID {
			return "", apperr.NotFound()
		}
	} else {
		var owned bool
		if err := s.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM trips WHERE id=$1 AND account_id=$2 AND deleted_at IS NULL AND purge_requested_at IS NULL)`, id, s.AccountID).Scan(&owned); err != nil {
			return "", apperr.Internal(err)
		}
		if !owned {
			return "", apperr.NotFound()
		}
	}
	if scope.Kind == "photo_day" || scope.Kind == "itinerary_day" {
		if len(parts) != 2 {
			return "", apperr.Unprocessable("INVALID_REFERENCE", "集合日期无效")
		}
		if scope.Kind == "photo_day" {
			r, e := s.PhotoDayRevision(ctx, policy.Epoch, id, types.Date(parts[1]))
			return r.Revision, e
		}
		r, e := s.ItineraryDayRevision(ctx, policy.Epoch, id, types.Date(parts[1]))
		return r.Revision, e
	}
	r, err := s.CollectionRevision(ctx, policy.Epoch, scope.Kind, id)
	return r.Revision, err
}
