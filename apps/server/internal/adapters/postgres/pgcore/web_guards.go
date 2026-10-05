package pgcore

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
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
// Consumer services must call this with their final scopes before writing.
func (s *TxScope) RequireCollections(ctx context.Context, required []collectionguard.Scope) error {
	policy, err := s.WebPolicy(ctx)
	if err != nil {
		return err
	}
	provided := collectionguard.Request(ctx)
	enabled := policy.CollectionGuardsRequired
	if native, ok := s.nativeCollectionGuards(required); provided == nil && ok {
		provided, enabled = native, true
	}
	if err := collectionguard.Check(ctx, enabled, s.AccountID, provided, required, s.collectionRevision); err != nil {
		return err
	}
	return s.RecordCollections(ctx, required)
}

// CheckCollections accepts only fully resolved native guards, then stores proof
// bound to this transaction and account. No caller flag can establish proof.
func (s *TxScope) CheckCollections(ctx context.Context, guards []collectionguard.Guard, required []collectionguard.Scope) error {
	return s.checkCollections(ctx, guards, required, s.collectionRevision)
}

func (s *TxScope) checkCollections(ctx context.Context, guards []collectionguard.Guard, required []collectionguard.Scope, resolve func(context.Context, collectionguard.Scope) (string, error)) error {
	if s.Tx == nil {
		return apperr.New(503, "DEPENDENCY_UNAVAILABLE", "缺少集合校验事务")
	}
	if err := collectionguard.Check(ctx, true, s.AccountID, guards, required, resolve); err != nil {
		return err
	}
	proof := make(map[collectionguard.Scope]string)
	if s.collectionProofTx == s.Tx && s.collectionProofAccount == s.AccountID {
		for key, value := range s.collectionProof {
			proof[key] = value
		}
	}
	for _, guard := range guards {
		proof[collectionguard.Scope{Kind: guard.Kind, ScopeID: guard.ScopeID}] = guard.Revision
	}
	s.collectionProof, s.collectionProofTx, s.collectionProofAccount = proof, s.Tx, s.AccountID
	s.addCollectionScopes(required)
	return nil
}

// RecordCollections validates ownership without reading a premature revision.
func (s *TxScope) RecordCollections(ctx context.Context, affected []collectionguard.Scope) error {
	if err := collectionguard.Check(ctx, false, s.AccountID, nil, affected, nil); err != nil {
		return err
	}
	for _, scope := range affected {
		if err := s.collectionOwnership(ctx, scope); err != nil {
			return err
		}
	}
	s.addCollectionScopes(affected)
	return nil
}

func (s *TxScope) addCollectionScopes(scopes []collectionguard.Scope) {
	if s.collectionScopes == nil {
		s.collectionScopes = make(map[collectionguard.Scope]struct{})
	}
	for _, scope := range scopes {
		s.collectionScopes[scope] = struct{}{}
	}
}

func (s *TxScope) finalCollectionRevisions(ctx context.Context) ([]write.ScopeRevision, error) {
	if len(s.collectionScopes) == 0 {
		return nil, nil
	}
	scopes := make([]collectionguard.Scope, 0, len(s.collectionScopes))
	for scope := range s.collectionScopes {
		scopes = append(scopes, scope)
	}
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].Kind != scopes[j].Kind {
			return scopes[i].Kind < scopes[j].Kind
		}
		return scopes[i].ScopeID < scopes[j].ScopeID
	})
	out := make([]write.ScopeRevision, 0, len(scopes))
	for _, scope := range scopes {
		revision, err := s.collectionRevision(ctx, scope)
		if err != nil {
			return nil, err
		}
		out = append(out, write.ScopeRevision{Kind: scope.Kind, ScopeID: scope.ScopeID, Revision: revision})
	}
	// Preserve other native facts while replacing overlapping scopes with final facts.
	native := append([]write.ScopeRevision(nil), s.revisions...)
	for _, final := range out {
		found := false
		for i := range native {
			if native[i].Kind == final.Kind && native[i].ScopeID == final.ScopeID {
				native[i] = final
				found = true
			}
		}
		if !found {
			native = append(native, final)
		}
	}
	s.revisions = native
	return out, nil
}

func (s *TxScope) collectionOwnership(ctx context.Context, scope collectionguard.Scope) error {
	parts := strings.Split(scope.ScopeID, "/")
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return apperr.Unprocessable("INVALID_REFERENCE", "集合范围无效")
	}
	if scope.Kind == "categories" {
		if id != s.AccountID {
			return apperr.NotFound()
		}
		return nil
	}
	var deleted, purging bool
	err = s.Tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL,purge_requested_at IS NOT NULL FROM trips WHERE id=$1 AND account_id=$2`, id, s.AccountID).Scan(&deleted, &purging)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound()
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if deleted {
		return apperr.Gone("TRIP_DELETED", "旅行已在回收站中")
	}
	if purging {
		return apperr.NotFound()
	}
	return nil
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
	if err := s.collectionOwnership(ctx, scope); err != nil {
		return "", err
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

func (s *TxScope) nativeCollectionGuards(required []collectionguard.Scope) ([]collectionguard.Guard, bool) {
	if s.Tx == nil || s.collectionProofTx != s.Tx || s.collectionProofAccount != s.AccountID || len(s.collectionProof) == 0 {
		return nil, false
	}
	guards := []collectionguard.Guard{}
	for _, scope := range required {
		if revision, ok := s.collectionProof[scope]; ok {
			guards = append(guards, collectionguard.Guard{Kind: scope.Kind, ScopeID: scope.ScopeID, Revision: revision})
		}
	}
	return guards, true
}
