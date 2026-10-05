package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	baselinepg "tripfolio/server/internal/adapters/postgres/collectionbaseline"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/collectionguard"
	baseline "tripfolio/server/internal/modules/collectionbaseline"
)

type collectionFixture struct {
	*snapshotFixture
	reader      *baselinepg.Store
	service     *baseline.Service
	web         actor.Actor
	trip, asset uuid.UUID
}

func newCollectionFixture(t *testing.T) *collectionFixture {
	t.Helper()
	f := newSnapshotFixture(t)
	var session uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM account_sessions WHERE account_id=$1 AND client_kind='web' AND revoked_at IS NULL`, f.owner).Scan(&session); err != nil {
		t.Fatal(err)
	}
	reader := baselinepg.NewStore(f.pool)
	out := &collectionFixture{snapshotFixture: f, reader: reader, web: actor.Actor{AccountID: f.owner, SessionID: session, ClientKind: actor.ClientWeb}, trip: f.newTrip("collection"), asset: uuid.New()}
	out.service = collectionService(t, reader)
	f.sql(`INSERT INTO assets(id,account_id,trip_id,scope,original_name,status,object_key,media_type,byte_size,sha256) VALUES($1,$2,$3,'trip','photo.jpg','ready','test/'||$1::uuid::text,'image/jpeg',1024,decode(repeat('00',32),'hex'))`, out.asset, f.owner, out.trip)
	// No ledger exists in this dedicated fixture; hide registration presets so an empty account scope can be tested.
	f.sql(`UPDATE expense_categories SET deleted_at=now(),version=version+1 WHERE account_id=$1`, f.owner)
	return out
}
func collectionService(t *testing.T, reader baseline.Reader) *baseline.Service {
	t.Helper()
	keys, err := security.ParseKeyring("collection-test=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("c", 32))))
	if err != nil {
		t.Fatal(err)
	}
	return baseline.NewService(reader, security.NewCursorCodec(keys), clock.Real{})
}
func (f *collectionFixture) query(kind string) baseline.Query {
	scope := f.owner.String()
	if kind != "categories" {
		scope = f.trip.String() + "/2026-09-30"
	}
	return baseline.Query{Kind: kind, ScopeID: scope, Limit: 2}
}
func (f *collectionFixture) insert(kind string) uuid.UUID {
	id := uuid.New()
	switch kind {
	case "categories":
		f.sql(`INSERT INTO expense_categories(id,account_id,name) VALUES($1,$2,$1::uuid::text)`, id, f.owner)
	case "itinerary_day":
		f.sql(`INSERT INTO itinerary_items(id,account_id,trip_id,title,kind,scheduled_on) VALUES($1,$2,$3,'outside dates','other','2026-09-30')`, id, f.owner, f.trip)
	case "photo_day":
		f.sql(`INSERT INTO photos(id,account_id,trip_id,asset_id,recorded_on) VALUES($1,$2,$3,$4,'2026-09-30')`, id, f.owner, f.trip, f.asset)
	}
	return id
}
func assertCollectionCode(t *testing.T, page baseline.Page, err error, code string, status int) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code || e.Status != status {
		t.Fatalf("want %s/%d, got %v", code, status, err)
	}
	if page.Items != nil || page.Revision != "" || page.NextCursor != nil {
		t.Fatal("partial page leaked on error")
	}
}
func verifyCollectionDigest(t *testing.T, pages []baseline.Page, expected int) {
	t.Helper()
	var source strings.Builder
	first := pages[0]
	fmt.Fprintf(&source, "%s\n%s\n%s\n", first.SyncEpoch, first.Kind, first.ScopeID)
	count, previous := 0, ""
	for i, page := range pages {
		if page.Revision != first.Revision || page.SyncEpoch != first.SyncEpoch || page.ExpiresAt != first.ExpiresAt {
			t.Fatal("mixed page metadata")
		}
		if (i == len(pages)-1) != (page.NextCursor == nil) {
			t.Fatal("wrong terminal marker")
		}
		for _, raw := range page.Items {
			var resource struct {
				ID, Version string
				DeletedAt   any `json:"deleted_at"`
			}
			if err := json.Unmarshal(raw, &resource); err != nil {
				t.Fatal(err)
			}
			if resource.ID <= previous || resource.Version == "" || resource.DeletedAt != nil {
				t.Fatalf("invalid resource %s", raw)
			}
			previous = resource.ID
			fmt.Fprintf(&source, "%s:%s\n", resource.ID, resource.Version)
			count++
		}
	}
	if count != expected || first.Revision != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(source.String()))) {
		t.Fatalf("incomplete collection count=%d expected=%d", count, expected)
	}
}

func TestCollectionBaselinesWebPagesAndEmptyScopes(t *testing.T) {
	f := newCollectionFixture(t)
	ctx := context.Background()
	for _, kind := range []string{"categories", "itinerary_day", "photo_day"} {
		t.Run(kind, func(t *testing.T) {
			q := f.query(kind)
			empty, err := f.service.List(ctx, f.web, q)
			if err != nil {
				t.Fatal(err)
			}
			verifyCollectionDigest(t, []baseline.Page{empty}, 0)
			if empty.Items == nil {
				t.Fatal("empty collection must serialize []")
			}
			for range 3 {
				f.insert(kind)
			}
			var pages []baseline.Page
			for {
				page, err := f.service.List(ctx, f.web, q)
				if err != nil {
					t.Fatal(err)
				}
				pages = append(pages, page)
				if page.NextCursor == nil {
					break
				}
				q.Cursor = *page.NextCursor
				if len(pages) > 2 {
					t.Fatal("pagination did not terminate")
				}
			}
			verifyCollectionDigest(t, pages, 3)
		})
	}
	// A full 100-item boundary must not accidentally imply completion.
	f.sql(`INSERT INTO expense_categories(id,account_id,name) SELECT md5($1::uuid::text||n::text)::uuid,$1, 'bulk-'||n::text FROM generate_series(1,102) n`, f.owner)
	q := f.query("categories")
	q.Limit = 100
	first, err := f.service.List(ctx, f.web, q)
	if err != nil || first.NextCursor == nil || len(first.Items) != 100 {
		t.Fatalf("boundary %v", err)
	}
	q.Cursor = *first.NextCursor
	last, err := f.service.List(ctx, f.web, q)
	if err != nil {
		t.Fatal(err)
	}
	verifyCollectionDigest(t, []baseline.Page{first, last}, 105)
}

func TestCollectionBaselinesRejectPageChanges(t *testing.T) {
	for _, kind := range []string{"categories", "itinerary_day", "photo_day"} {
		t.Run(kind, func(t *testing.T) {
			f := newCollectionFixture(t)
			ctx := context.Background()
			q := f.query(kind)
			q.Limit = 1
			for range 3 {
				f.insert(kind)
			}
			for _, change := range []string{"insert", "version", "delete", "move", "epoch"} {
				if kind == "categories" && change == "move" {
					continue
				}
				t.Run(change, func(t *testing.T) {
					id := f.insert(kind)
					first, err := f.service.List(ctx, f.web, q)
					if err != nil || first.NextCursor == nil {
						t.Fatalf("first %v", err)
					}
					next := q
					next.Cursor = *first.NextCursor
					table := map[string]string{"categories": "expense_categories", "itinerary_day": "itinerary_items", "photo_day": "photos"}[kind]
					switch change {
					case "insert":
						f.insert(kind)
					case "version":
						f.sql(`UPDATE `+table+` SET version=version+1 WHERE account_id=$1 AND id=$2`, f.owner, id)
					case "delete":
						f.sql(`UPDATE `+table+` SET deleted_at=now(),version=version+1 WHERE account_id=$1 AND id=$2`, f.owner, id)
					case "move":
						column := "scheduled_on"
						if kind == "photo_day" {
							column = "recorded_on"
						}
						f.sql(`UPDATE `+table+` SET `+column+`='2026-10-01',version=version+1 WHERE account_id=$1 AND id=$2`, f.owner, id)
					case "epoch":
						f.sql(`UPDATE account_sync_state SET sync_epoch=$2 WHERE account_id=$1`, f.owner, uuid.New())
					}
					page, err := f.service.List(ctx, f.web, next)
					assertCollectionCode(t, page, err, "COLLECTION_BASELINE_CHANGED", 412)
				})
			}
		})
	}
}

type interleavedCollectionReader struct {
	baseline.Reader
	mutate func()
}
type interleavedCollectionView struct {
	baseline.View
	mutate func()
}

func (r interleavedCollectionReader) Read(ctx context.Context, a actor.Actor, fn func(baseline.View) error) error {
	return r.Reader.Read(ctx, a, func(v baseline.View) error { return fn(interleavedCollectionView{v, r.mutate}) })
}
func (v interleavedCollectionView) Entries(ctx context.Context, scope collectionguard.Scope, after uuid.UUID, limit int) ([]baseline.Entry, error) {
	v.mutate() // a separate connection commits after the revision was captured
	return v.View.Entries(ctx, scope, after, limit)
}
func TestCollectionBaselinesRepeatableReadProjection(t *testing.T) {
	for _, kind := range []string{"categories", "itinerary_day", "photo_day"} {
		t.Run(kind, func(t *testing.T) {
			f := newCollectionFixture(t)
			ctx := context.Background()
			id := f.insert(kind)
			q := f.query(kind)
			before, err := f.service.List(ctx, f.web, q)
			if err != nil {
				t.Fatal(err)
			}
			table := map[string]string{"categories": "expense_categories", "itinerary_day": "itinerary_items", "photo_day": "photos"}[kind]
			reader := interleavedCollectionReader{f.reader, func() { f.sql(`UPDATE `+table+` SET version=version+1 WHERE account_id=$1 AND id=$2`, f.owner, id) }}
			page, err := collectionService(t, reader).List(ctx, f.web, q)
			if err != nil {
				t.Fatal(err)
			}
			verifyCollectionDigest(t, []baseline.Page{page}, 1)
			if page.Revision != before.Revision || string(page.Items[0]) != string(before.Items[0]) {
				t.Fatal("mixed repeatable-read snapshot")
			}
			after, err := f.service.List(ctx, f.web, q)
			if err != nil {
				t.Fatal(err)
			}
			if after.Revision == before.Revision {
				t.Fatal("separate mutation was not committed")
			}
		})
	}
}

func TestCollectionBaselinesIdentityAndOwnership(t *testing.T) {
	f := newCollectionFixture(t)
	ctx := context.Background()
	q := f.query("categories")
	for range 3 {
		f.insert("categories")
	}
	for _, a := range []actor.Actor{f.web, f.a} {
		if _, err := f.service.List(ctx, a, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []actor.Actor{{}, {AccountID: f.owner, SessionID: f.web.SessionID, ClientKind: "share"}, {AccountID: f.owner, SessionID: f.web.SessionID, ClientKind: actor.ClientAndroid}} {
		page, err := f.service.List(ctx, a, q)
		assertCollectionCode(t, page, err, "SESSION_EXPIRED", 401)
	}
	other := q
	other.ScopeID = uuid.NewString()
	page, err := f.service.List(ctx, f.web, other)
	assertCollectionCode(t, page, err, "RESOURCE_NOT_FOUND", 404)
	other = f.query("photo_day")
	other.ScopeID = uuid.NewString() + "/2026-09-30"
	page, err = f.service.List(ctx, f.web, other)
	assertCollectionCode(t, page, err, "RESOURCE_NOT_FOUND", 404)
	// An archived trip still has editable collection baselines; deleted/purging trips do not.
	f.sql(`UPDATE trips SET archived_at=now() WHERE account_id=$1 AND id=$2`, f.owner, f.trip)
	if _, err := f.service.List(ctx, f.web, f.query("itinerary_day")); err != nil {
		t.Fatal(err)
	}
	f.sql(`UPDATE trips SET deleted_at=now(),purge_after_at=now()+interval '30 days' WHERE account_id=$1 AND id=$2`, f.owner, f.trip)
	page, err = f.service.List(ctx, f.web, f.query("itinerary_day"))
	assertCollectionCode(t, page, err, "RESOURCE_NOT_FOUND", 404)
	first, err := f.service.List(ctx, f.web, q)
	if err != nil || first.NextCursor == nil {
		t.Fatalf("first %v", err)
	}
	f.sql(`UPDATE account_sessions SET revoked_at=now() WHERE account_id=$1 AND id=$2`, f.owner, f.web.SessionID)
	q.Cursor = *first.NextCursor
	page, err = f.service.List(ctx, f.web, q)
	assertCollectionCode(t, page, err, "SESSION_EXPIRED", 401)
}
