package pgcore

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/collectionguard"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestWebPolicyAdmission(t *testing.T) {
	epoch, old := uuid.New(), uuid.New()
	for _, test := range []struct {
		p    WebPolicy
		want bool
	}{
		{WebPolicy{}, false}, {WebPolicy{true, epoch, nil}, false},
		{WebPolicy{true, epoch, &old}, false}, {WebPolicy{false, epoch, &epoch}, false},
		{WebPolicy{true, epoch, &epoch}, true},
	} {
		if test.p.V2Enabled() != test.want {
			t.Fatal(test)
		}
	}
}

func TestSyncAdmissionPolicy(t *testing.T) {
	epoch, old := uuid.New(), uuid.New()
	for _, test := range []struct {
		name   string
		policy WebPolicy
		allow  bool
	}{
		{"absent", WebPolicy{}, false},
		{"false", WebPolicy{false, epoch, &epoch}, false},
		{"null", WebPolicy{true, epoch, nil}, false},
		{"old activation", WebPolicy{true, epoch, &old}, false},
		{"old policy", WebPolicy{true, old, &old}, false},
		{"zero epoch", WebPolicy{true, uuid.Nil, &uuid.Nil}, false},
		{"enabled", WebPolicy{true, epoch, &epoch}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (SyncWriteOptions{Epoch: epoch}).admitNew(test.policy)
			if test.allow {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			e, ok := apperr.As(err)
			if !ok || e.Code != "SYNC_NOT_READY" || e.Status != 409 {
				t.Fatalf("admission: %v", err)
			}
		})
	}
}

func TestWebPolicyPersistence(t *testing.T) {
	raw := os.Getenv("TRIPFOLIO_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("dedicated database not configured")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Path != "/h07_sync_test" && u.Path != "/tripfolio_test") {
		t.Fatal("dedicated policy database required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `SET LOCAL search_path=pg_temp;
CREATE TEMP TABLE account_sync_state(account_id uuid,sync_epoch uuid);
CREATE TEMP TABLE account_sync_capabilities(account_id uuid PRIMARY KEY,collection_guards_required bool NOT NULL DEFAULT false,v2_enabled_epoch uuid,enabled_at timestamptz);`)
	if err != nil {
		t.Fatal(err)
	}
	account, epoch := uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO account_sync_state VALUES($1,$2)`, account, epoch); err != nil {
		t.Fatal(err)
	}
	s := &TxScope{Tx: tx, AccountID: account}
	p, err := s.WebPolicy(ctx)
	if err != nil || p.CollectionGuardsRequired || p.V2Enabled() {
		t.Fatalf("%+v %v", p, err)
	}
	if err = s.requireWebGuards(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.requireWebGuards(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE account_sync_capabilities SET v2_enabled_epoch=$1,enabled_at=now()`, epoch); err != nil {
		t.Fatal(err)
	}
	p, err = s.WebPolicy(ctx)
	if err != nil || !p.V2Enabled() {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE account_sync_state SET sync_epoch=$1`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	p, err = s.WebPolicy(ctx)
	if err != nil || p.V2Enabled() || !p.CollectionGuardsRequired {
		t.Fatalf("%+v %v", p, err)
	}
	if err = s.disableSyncV2(ctx); err != nil {
		t.Fatal(err)
	}
	p, err = s.WebPolicy(ctx)
	if err != nil || p.V2EnabledEpoch != nil || !p.CollectionGuardsRequired {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err = tx.Exec(ctx, `DROP TABLE account_sync_capabilities`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.WebPolicy(ctx); err == nil {
		t.Fatal("missing table silently downgraded protection")
	}
}

type proofTestTx struct{ pgx.Tx }

func TestCollectionProofAtomicAndTransactionBound(t *testing.T) {
	ctx := context.Background()
	account := uuid.New()
	tx := &proofTestTx{}
	s := &TxScope{Tx: tx, AccountID: account}
	first := collectionguard.Scope{Kind: "members", ScopeID: uuid.NewString()}
	second := collectionguard.Scope{Kind: "members", ScopeID: uuid.NewString()}
	revision := "sha256:" + strings.Repeat("a", 64)
	guards := []collectionguard.Guard{{Kind: first.Kind, ScopeID: first.ScopeID, Revision: revision}, {Kind: second.Kind, ScopeID: second.ScopeID, Revision: revision}}
	resolver := func(_ context.Context, scope collectionguard.Scope) (string, error) {
		if scope == second {
			return "", errors.New("resolver failed")
		}
		return revision, nil
	}
	if err := s.checkCollections(ctx, guards, []collectionguard.Scope{first, second}, resolver); err == nil || len(s.collectionProof) != 0 || len(s.collectionScopes) != 0 {
		t.Fatalf("partial proof: %v", err)
	}
	if err := s.checkCollections(ctx, guards[:1], []collectionguard.Scope{first}, resolver); err != nil {
		t.Fatal(err)
	}
	guards[0].Revision = "changed"
	got, ok := s.nativeCollectionGuards([]collectionguard.Scope{first})
	if !ok || len(got) != 1 || got[0].Revision != revision {
		t.Fatalf("proof changed: %+v", got)
	}
	got[0].Revision = "changed again"
	got, _ = s.nativeCollectionGuards([]collectionguard.Scope{first})
	if got[0].Revision != revision {
		t.Fatal("proof aliased")
	}
	unrelated, _ := s.nativeCollectionGuards([]collectionguard.Scope{second})
	if len(unrelated) != 0 {
		t.Fatal("unrelated scope got proof")
	}
	s.Tx = &proofTestTx{}
	if _, ok := s.nativeCollectionGuards([]collectionguard.Scope{first}); ok {
		t.Fatal("proof crossed transaction")
	}
	s.Tx = tx
	s.AccountID = uuid.New()
	if _, ok := s.nativeCollectionGuards([]collectionguard.Scope{first}); ok {
		t.Fatal("proof crossed account")
	}
}
