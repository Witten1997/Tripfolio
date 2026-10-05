package integration

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/write"
)

func TestSyncWebCollectionsNativeProofAndOriginalFacts(t *testing.T) {
	f := newSnapshotFixture(t)
	owner, trip := f.owner, f.newTrip("native collection")
	ctx := actor.WithActor(context.Background(), f.a)
	w := pgcore.NewWriter(f.pool, nil, clock.Real{}, quietLogger())
	target := collectionguard.Scope{Kind: "members", ScopeID: trip.String()}
	var epoch uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT sync_epoch FROM account_sync_state WHERE account_id=$1`, owner).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, owner); err != nil {
		t.Fatal(err)
	}
	req := write.Request{AccountID: owner, OperationID: uuid.New(), OperationType: "native.collection.test", Fingerprint: sha256.Sum256([]byte("native"))}
	var before string
	result, err := w.RunSync(ctx, req, pgcore.SyncWriteOptions{Epoch: epoch}, func(ctx context.Context, s *pgcore.TxScope) error {
		revision, e := s.MembersRevision(ctx, epoch, trip)
		if e != nil {
			return e
		}
		before = revision.Revision
		if e = s.CheckCollections(ctx, []collectionguard.Guard{{Kind: target.Kind, ScopeID: target.ScopeID, Revision: before}}, []collectionguard.Scope{target}); e != nil {
			return e
		}
		if e = write.RequireCollections(ctx, s, []collectionguard.Scope{target}); e != nil {
			return e
		}
		// Simulate an earlier native fact; the final result must replace its stale value.
		if e = s.RecordMembersRevision(ctx, epoch, trip); e != nil {
			return e
		}
		_, e = s.Tx.Exec(ctx, `UPDATE trip_members SET version=version+1 WHERE account_id=$1 AND trip_id=$2`, owner, trip)
		return e
	}, nil)
	if err != nil || result.Sync == nil || len(result.ScopeRevisions) != 1 || result.ScopeRevisions[0].Revision == before || !reflect.DeepEqual(result.ScopeRevisions, result.Sync.ScopeRevisions) {
		t.Fatalf("final facts: %+v %v", result, err)
	}
	original := append([]write.ScopeRevision(nil), result.ScopeRevisions...)
	if _, err = f.pool.Exec(ctx, `UPDATE trip_members SET version=version+1 WHERE account_id=$1 AND trip_id=$2`, owner, trip); err != nil {
		t.Fatal(err)
	}
	replay, err := w.RunSync(ctx, req, pgcore.SyncWriteOptions{Epoch: epoch}, func(context.Context, *pgcore.TxScope) error { t.Fatal("replay executed writes"); return nil }, nil)
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.ScopeRevisions, original) || !reflect.DeepEqual(replay.Sync.ScopeRevisions, original) {
		t.Fatalf("replay facts: %+v %v", replay, err)
	}
	req.OperationID = uuid.New()
	_, err = w.Run(ctx, req, func(ctx context.Context, s *pgcore.TxScope) error {
		return write.RequireCollections(ctx, s, []collectionguard.Scope{target})
	}, nil)
	policyError(t, err, 428, "COLLECTION_BASE_REQUIRED")
}

func TestSyncWebCollectionsAffectedAndNoOp(t *testing.T) {
	f, owner, trip := policyFixture(t)
	ctx := context.Background()
	w := pgcore.NewWriter(f.pool, nil, clock.Real{}, quietLogger())
	target := collectionguard.Scope{Kind: "members", ScopeID: trip.String()}
	makeRequest := func() write.Request {
		return write.Request{AccountID: owner, OperationID: uuid.New(), OperationType: "affected.collection.test", Fingerprint: sha256.Sum256([]byte("affected"))}
	}
	var before string
	result, err := w.Run(ctx, makeRequest(), func(ctx context.Context, s *pgcore.TxScope) error {
		policy, e := s.WebPolicy(ctx)
		if e != nil {
			return e
		}
		revision, e := s.MembersRevision(ctx, policy.Epoch, trip)
		if e != nil {
			return e
		}
		before = revision.Revision
		if e = write.RecordCollections(ctx, s, []collectionguard.Scope{target}); e != nil {
			return e
		}
		if e = write.RecordCollections(ctx, s, []collectionguard.Scope{target}); e != nil {
			return e
		}
		_, e = s.Tx.Exec(ctx, `UPDATE trip_members SET version=version+1 WHERE account_id=$1 AND trip_id=$2`, owner, trip)
		return e
	}, nil)
	if err != nil || len(result.ScopeRevisions) != 1 || result.ScopeRevisions[0].Revision == before {
		t.Fatalf("affected facts: %+v %v", result, err)
	}
	req := makeRequest()
	noop, err := w.Run(ctx, req, func(ctx context.Context, s *pgcore.TxScope) error {
		return write.RequireCollections(ctx, s, []collectionguard.Scope{target})
	}, nil)
	if err != nil || !reflect.DeepEqual(noop.ScopeRevisions, result.ScopeRevisions) {
		t.Fatalf("no-op facts: %+v %v", noop, err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE trip_members SET version=version+1 WHERE account_id=$1 AND trip_id=$2`, owner, trip); err != nil {
		t.Fatal(err)
	}
	replay, err := w.Run(ctx, req, func(context.Context, *pgcore.TxScope) error { t.Fatal("replayed no-op callback"); return nil }, nil)
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.ScopeRevisions, noop.ScopeRevisions) {
		t.Fatalf("no-op replay: %+v %v", replay, err)
	}
}

func TestSyncWebCollectionsProofFailureAndRollback(t *testing.T) {
	f, owner, trip := policyFixture(t)
	ctx := context.Background()
	w := pgcore.NewWriter(f.pool, nil, clock.Real{}, quietLogger())
	target := collectionguard.Scope{Kind: "members", ScopeID: trip.String()}
	if _, err := f.pool.Exec(ctx, `INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, owner); err != nil {
		t.Fatal(err)
	}
	req := write.Request{AccountID: owner, OperationID: uuid.New(), OperationType: "failed.collection.test", Fingerprint: sha256.Sum256([]byte("failed"))}
	var before string
	if err := f.pool.QueryRow(ctx, `SELECT nickname FROM accounts WHERE id=$1`, owner).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err := w.Run(ctx, req, func(ctx context.Context, s *pgcore.TxScope) error {
		if _, e := s.Tx.Exec(ctx, `UPDATE accounts SET nickname='rollback-collection' WHERE id=$1`, owner); e != nil {
			return e
		}
		bad := []collectionguard.Guard{{Kind: target.Kind, ScopeID: target.ScopeID, Revision: "sha256:" + strings.Repeat("0", 64)}}
		e := s.CheckCollections(ctx, bad, []collectionguard.Scope{target})
		policyError(t, e, 412, "COLLECTION_CONFLICT")
		return write.RequireCollections(ctx, s, []collectionguard.Scope{target})
	}, nil)
	policyError(t, err, 428, "COLLECTION_BASE_REQUIRED")
	var after string
	var receipts int
	if err = f.pool.QueryRow(ctx, `SELECT nickname,(SELECT count(*) FROM mutation_receipts WHERE account_id=$1 AND operation_id=$2) FROM accounts WHERE id=$1`, owner, req.OperationID).Scan(&after, &receipts); err != nil || after != before || receipts != 0 {
		t.Fatalf("rollback: %q %q %d %v", before, after, receipts, err)
	}
	req.OperationID = uuid.New()
	_, err = w.Run(ctx, req, func(ctx context.Context, s *pgcore.TxScope) error {
		policy, e := s.WebPolicy(ctx)
		if e != nil {
			return e
		}
		revision, e := s.MembersRevision(ctx, policy.Epoch, trip)
		if e != nil {
			return e
		}
		if e = s.CheckCollections(ctx, []collectionguard.Guard{{Kind: target.Kind, ScopeID: target.ScopeID, Revision: revision.Revision}}, []collectionguard.Scope{target}); e != nil {
			return e
		}
		if _, e = s.Tx.Exec(ctx, `UPDATE trip_members SET version=version+1 WHERE account_id=$1 AND trip_id=$2`, owner, trip); e != nil {
			return e
		}
		return write.RequireCollections(ctx, s, []collectionguard.Scope{target})
	}, nil)
	policyError(t, err, 412, "COLLECTION_CONFLICT")
	req.OperationID = uuid.New()
	_, err = w.Run(ctx, req, func(ctx context.Context, s *pgcore.TxScope) error {
		if e := write.RecordCollections(ctx, s, []collectionguard.Scope{target}); e != nil {
			return e
		}
		return errors.New("receipt must not be stored")
	}, nil)
	if err == nil {
		t.Fatal("callback failure accepted")
	}
}

func TestSyncWebCollectionsScopeIsolation(t *testing.T) {
	f, owner, trip := policyFixture(t)
	ctx := context.Background()
	w := pgcore.NewWriter(f.pool, nil, clock.Real{}, quietLogger())
	scope := collectionguard.Scope{Kind: "members", ScopeID: trip.String()}
	req := func() write.Request {
		return write.Request{AccountID: owner, OperationID: uuid.New(), OperationType: "collection.isolation", Fingerprint: sha256.Sum256([]byte("isolation"))}
	}
	provided, _ := collectionguard.WithRequest(ctx, []collectionguard.Guard{{Kind: scope.Kind, ScopeID: scope.ScopeID, Revision: "sha256:" + strings.Repeat("a", 64)}})
	_, err := w.Run(provided, req(), func(ctx context.Context, s *pgcore.TxScope) error { return write.RequireCollections(ctx, s, nil) }, nil)
	policyError(t, err, 422, "INVALID_REFERENCE")
	for _, bad := range []collectionguard.Scope{{Kind: "members", ScopeID: uuid.NewString()}, {Kind: "photo_day", ScopeID: trip.String() + "/2026-02-30"}, {Kind: "categories", ScopeID: uuid.NewString()}} {
		if _, err := w.Run(ctx, req(), func(ctx context.Context, s *pgcore.TxScope) error {
			return write.RecordCollections(ctx, s, []collectionguard.Scope{bad})
		}, nil); err == nil {
			t.Fatalf("invalid affected scope %+v accepted", bad)
		}
	}
}

func TestSyncWebCollectionsDeletedTripBoundary(t *testing.T) {
	f, owner, trip := policyFixture(t)
	ctx := context.Background()
	w := pgcore.NewWriter(f.pool, nil, clock.Real{}, quietLogger())
	target := collectionguard.Scope{Kind: "members", ScopeID: trip.String()}
	if _, err := f.pool.Exec(ctx, `UPDATE trips SET deleted_at=now(),purge_after_at=now()+interval '30 days' WHERE id=$1`, trip); err != nil {
		t.Fatal(err)
	}
	for _, record := range []bool{false, true} {
		req := write.Request{AccountID: owner, OperationID: uuid.New(), OperationType: "collection.deleted", Fingerprint: sha256.Sum256([]byte("deleted"))}
		_, err := w.Run(ctx, req, func(ctx context.Context, s *pgcore.TxScope) error {
			if record {
				return write.RecordCollections(ctx, s, []collectionguard.Scope{target})
			}
			return s.CheckCollections(ctx, []collectionguard.Guard{{Kind: target.Kind, ScopeID: target.ScopeID, Revision: "sha256:" + strings.Repeat("a", 64)}}, []collectionguard.Scope{target})
		}, nil)
		policyError(t, err, 410, "TRIP_DELETED")
	}
}
