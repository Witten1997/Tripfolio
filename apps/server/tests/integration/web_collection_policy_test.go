package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"tripfolio/server/db"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/write"
)

func policyFixture(t *testing.T) (*apiFixture, uuid.UUID, uuid.UUID) {
	t.Helper()
	u, err := url.Parse(testDatabaseURL(t))
	if err != nil || (u.Path != "/h07_sync_test" && u.Path != "/tripfolio_test") {
		t.Fatal("dedicated policy database required")
	}
	f := newAPIFixture(t)
	reg := f.registerWeb(uniqueEmail(), "correct horse battery")
	owner := uuid.MustParse(reg.data()["account"].(map[string]any)["id"].(string))
	token := reg.data()["access_token"].(string)
	trip := f.createTrip(token, map[string]any{"name": "policy test", "start_date": "2026-10-01", "end_date": "2026-10-03"})
	return f, owner, uuid.MustParse(trip["id"].(string))
}

func policyError(t *testing.T, err error, status int, code string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Status != status || e.Code != code {
		t.Fatalf("got %v want %d %s", err, status, code)
	}
}

func TestWebCollectionPolicyWriter(t *testing.T) {
	f, owner, trip := policyFixture(t)
	ctx := context.Background()
	w := pgcore.NewWriter(f.pool, nil, clock.Real{}, quietLogger())
	scope := collectionguard.Scope{Kind: "members", ScopeID: trip.String()}
	fp := sha256.Sum256([]byte("policy-fixture"))
	req := func(id uuid.UUID) write.Request {
		return write.Request{AccountID: owner, OperationID: id, OperationType: "web.guard.test", Fingerprint: fp}
	}
	check := func(ctx context.Context, s *pgcore.TxScope) error {
		return s.RequireCollections(ctx, []collectionguard.Scope{scope})
	}
	op := uuid.New()
	if _, err := w.Run(ctx, req(op), check, nil); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := f.pool.QueryRow(ctx, `SELECT request_hash FROM mutation_receipts WHERE account_id=$1 AND operation_id=$2`, owner, op).Scan(&stored); err != nil || !bytes.Equal(stored, fp[:]) {
		t.Fatal("legacy fingerprint", err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Run(ctx, req(uuid.New()), check, nil); err == nil {
		t.Fatal("missing guard accepted")
	} else {
		policyError(t, err, 428, "COLLECTION_BASE_REQUIRED")
	}
	var epoch uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT sync_epoch FROM account_sync_state WHERE account_id=$1`, owner).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := &pgcore.TxScope{Tx: tx, AccountID: owner}
	revision, err := s.MembersRevision(ctx, epoch, trip)
	tx.Rollback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	guard := collectionguard.Guard{Kind: scope.Kind, ScopeID: scope.ScopeID, Revision: revision.Revision}
	gctx, err := collectionguard.WithRequest(ctx, []collectionguard.Guard{guard})
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err := f.pool.QueryRow(ctx, `SELECT nickname FROM accounts WHERE id=$1`, owner).Scan(&before); err != nil {
		t.Fatal(err)
	}
	op = uuid.New()
	mutate := func(ctx context.Context, s *pgcore.TxScope) error {
		if err := check(ctx, s); err != nil {
			return err
		}
		_, err := s.Tx.Exec(ctx, `UPDATE trip_members SET version=version+1 WHERE account_id=$1 AND trip_id=$2`, owner, trip)
		return err
	}
	result, err := w.Run(gctx, req(op), mutate, nil)
	if err != nil || result.Replayed {
		t.Fatalf("%+v %v", result, err)
	}
	want := collectionguard.Fingerprint(gctx, fp)
	if err := f.pool.QueryRow(ctx, `SELECT request_hash FROM mutation_receipts WHERE account_id=$1 AND operation_id=$2`, owner, op).Scan(&stored); err != nil || !bytes.Equal(stored, want[:]) {
		t.Fatal("guard fingerprint", err)
	}
	result, err = w.Run(gctx, req(op), func(context.Context, *pgcore.TxScope) error {
		t.Fatal("successful replay executed callback")
		return nil
	}, nil)
	if err != nil || !result.Replayed {
		t.Fatalf("replay: %+v %v", result, err)
	}
	_, err = w.Run(gctx, req(uuid.New()), check, nil)
	policyError(t, err, 412, "COLLECTION_CONFLICT")
	guard.Revision = "sha256:" + strings.Repeat("b", 64)
	changed, _ := collectionguard.WithRequest(ctx, []collectionguard.Guard{guard})
	_, err = w.Run(changed, req(op), check, nil)
	policyError(t, err, 409, "IDEMPOTENCY_CONFLICT")
	failed := uuid.New()
	_, err = w.Run(gctx, req(failed), func(ctx context.Context, s *pgcore.TxScope) error {
		if _, e := s.Tx.Exec(ctx, `UPDATE accounts SET nickname='should-rollback' WHERE id=$1`, owner); e != nil {
			return e
		}
		return check(ctx, s)
	}, nil)
	policyError(t, err, 412, "COLLECTION_CONFLICT")
	var after string
	var receipts int
	if err := f.pool.QueryRow(ctx, `SELECT nickname,(SELECT count(*) FROM mutation_receipts WHERE operation_id=$2) FROM accounts WHERE id=$1`, owner, failed).Scan(&after, &receipts); err != nil || after != before || receipts != 0 {
		t.Fatalf("rollback %q %q receipts=%d %v", before, after, receipts, err)
	}
	var session uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM account_sessions WHERE account_id=$1 LIMIT 1`, owner).Scan(&session); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE account_sessions SET revoked_at=now() WHERE id=$1`, session); err != nil {
		t.Fatal(err)
	}
	revoked := actor.WithActor(gctx, actor.Actor{AccountID: owner, SessionID: session, ClientKind: actor.ClientWeb})
	_, err = w.Run(revoked, req(op), check, nil)
	policyError(t, err, 401, "SESSION_EXPIRED")
}

func TestWebCollectionPolicyScopesAndConcurrency(t *testing.T) {
	f, owner, trip := policyFixture(t)
	ctx := context.Background()
	w := pgcore.NewWriter(f.pool, nil, clock.Real{}, quietLogger())
	if _, err := f.pool.Exec(ctx, `INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, owner); err != nil {
		t.Fatal(err)
	}
	var epoch uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT sync_epoch FROM account_sync_state WHERE account_id=$1`, owner).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	req := func() write.Request {
		return write.Request{AccountID: owner, OperationID: uuid.New(), OperationType: "web.policy.scope", Fingerprint: sha256.Sum256([]byte("scope"))}
	}
	for _, kind := range []string{"categories", "members", "packing_order", "todo_order", "photo_day", "itinerary_day"} {
		t.Run(kind, func(t *testing.T) {
			scopeID := trip.String()
			if kind == "categories" {
				scopeID = owner.String()
			}
			if strings.HasSuffix(kind, "_day") {
				scopeID += "/2026-10-01"
			}
			// A valid empty or populated scope reaches the shared resolver; a fake digest conflicts.
			g := collectionguard.Guard{Kind: kind, ScopeID: scopeID, Revision: "sha256:" + strings.Repeat("0", 64)}
			_, err := w.Run(ctx, req(), func(ctx context.Context, s *pgcore.TxScope) error {
				return s.CheckCollections(ctx, []collectionguard.Guard{g}, []collectionguard.Scope{{Kind: kind, ScopeID: scopeID}})
			}, nil)
			policyError(t, err, 412, "COLLECTION_CONFLICT")
		})
	}
	foreign := f.registerWeb(uniqueEmail(), "correct horse battery")
	foreignTrip := f.createTrip(foreign.data()["access_token"].(string), map[string]any{"name": "foreign policy scope", "start_date": "2026-10-01", "end_date": "2026-10-03"})
	other := foreignTrip["id"].(string)
	g := collectionguard.Guard{Kind: "members", ScopeID: other, Revision: "sha256:" + strings.Repeat("0", 64)}
	_, err := w.Run(ctx, req(), func(ctx context.Context, s *pgcore.TxScope) error {
		return s.CheckCollections(ctx, []collectionguard.Guard{g}, []collectionguard.Scope{{Kind: g.Kind, ScopeID: g.ScopeID}})
	}, nil)
	policyError(t, err, 404, "RESOURCE_NOT_FOUND")
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := &pgcore.TxScope{Tx: tx, AccountID: owner}
	r, err := s.MembersRevision(ctx, epoch, trip)
	tx.Rollback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	g = collectionguard.Guard{Kind: "members", ScopeID: trip.String(), Revision: r.Revision}
	gctx, err := collectionguard.WithRequest(ctx, []collectionguard.Guard{g})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := w.Run(gctx, req(), func(ctx context.Context, s *pgcore.TxScope) error {
				if e := s.RequireCollections(ctx, []collectionguard.Scope{{Kind: g.Kind, ScopeID: g.ScopeID}}); e != nil {
					return e
				}
				_, e := s.Tx.Exec(ctx, `UPDATE trip_members SET version=version+1 WHERE account_id=$1 AND trip_id=$2`, owner, trip)
				return e
			}, nil)
			out <- e
		}()
	}
	wg.Wait()
	close(out)
	ok, conflicts := 0, 0
	for e := range out {
		if e == nil {
			ok++
		} else {
			policyError(t, e, 412, "COLLECTION_CONFLICT")
			conflicts++
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", ok, conflicts)
	}
}

func TestWebCollectionPolicyMigrationDown(t *testing.T) {
	f, owner, _ := policyFixture(t)
	ctx := context.Background()
	source, err := db.Migrations.ReadFile("migrations/00029_sync_web_policy.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing down")
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT protected_down`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, down); err == nil || !strings.Contains(err.Error(), "SYNC_GUARD_DOWNGRADE_FORBIDDEN") {
		t.Fatal("unsafe downgrade", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT protected_down`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM account_sync_capabilities`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, down); err != nil {
		t.Fatal("empty down", err)
	}
	if _, err = tx.Exec(ctx, up); err != nil {
		t.Fatal("up after down", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO account_sync_capabilities(account_id,v2_enabled_epoch) VALUES($1,$2)`, owner, uuid.New()); err == nil {
		t.Fatal("partial activation allowed")
	}
}
