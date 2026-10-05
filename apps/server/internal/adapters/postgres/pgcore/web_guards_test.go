package pgcore

import (
	"context"
	"net/url"
	"os"
	"testing"

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
