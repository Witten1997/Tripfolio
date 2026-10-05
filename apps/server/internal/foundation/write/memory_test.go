package write_test

import (
	"context"
	"github.com/google/uuid"
	"strings"
	"testing"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/write"
)

type collectionMemoryRepo struct{ value int }

func (r *collectionMemoryRepo) Snapshot() func() { old := r.value; return func() { r.value = old } }
func TestMemoryCollectionSafetyAndRollback(t *testing.T) {
	account := uuid.New()
	target := collectionguard.Scope{Kind: "categories", ScopeID: account.String()}
	for _, record := range []bool{false, true} {
		repo := &collectionMemoryRepo{}
		u := write.NewMemoryUnitOfWork(repo)
		call := write.RequireCollections
		if record {
			call = write.RecordCollections
		}
		run := func(ctx context.Context, scopes []collectionguard.Scope) (write.Result, error) {
			return u.Run(ctx, write.Request{AccountID: account, OperationID: uuid.New()}, func(ctx context.Context, s write.Scope, r *collectionMemoryRepo) error {
				r.value++
				return call(ctx, s, scopes)
			}, nil)
		}
		result, err := run(context.Background(), []collectionguard.Scope{target})
		if err != nil || repo.value != 1 || len(result.ScopeRevisions) != 0 {
			t.Fatalf("memory compatibility: %+v %v", result, err)
		}
		for _, guards := range [][]collectionguard.Guard{{}, {{Kind: target.Kind, ScopeID: target.ScopeID, Revision: "sha256:" + strings.Repeat("a", 64)}}} {
			ctx, err := collectionguard.WithRequest(context.Background(), guards)
			if err != nil {
				t.Fatal(err)
			}
			_, err = run(ctx, []collectionguard.Scope{target})
			e, ok := apperr.As(err)
			if !ok || e.Code != "DEPENDENCY_UNAVAILABLE" || repo.value != 1 {
				t.Fatalf("guard bypass or rollback: %v value=%d", err, repo.value)
			}
		}
		for _, bad := range []collectionguard.Scope{{Kind: "categories", ScopeID: uuid.NewString()}, {Kind: "photo_day", ScopeID: uuid.NewString() + "/2026-02-30"}, {Kind: "unknown", ScopeID: account.String()}} {
			_, err := run(context.Background(), []collectionguard.Scope{bad})
			if err == nil || repo.value != 1 {
				t.Fatalf("invalid scope accepted %+v %v", bad, err)
			}
		}
	}
}
