package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	adminpg "tripfolio/server/internal/adapters/postgres/admin"
	syncpg "tripfolio/server/internal/adapters/postgres/sync"
	"tripfolio/server/internal/adapters/queue"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/bootstrap"
	"tripfolio/server/internal/config"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/clock"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/modules/travel/trip"
	"tripfolio/server/internal/transport/httpapi"
	riverjobs "tripfolio/server/internal/transport/river"
)

type snapshotFixture struct {
	*apiFixture
	svc                     *syncmodule.Service
	owner, epoch            uuid.UUID
	a                       actor.Actor
	token, webToken, rawURL string
}

func newSnapshotFixture(t *testing.T) *snapshotFixture {
	t.Helper()
	raw := testDatabaseURL(t)
	u, err := url.Parse(raw)
	if err != nil || (u.Path != "/h07_sync_test" && u.Path != "/tripfolio_test") {
		t.Fatal("snapshot tests require a dedicated test database")
	}
	f := newAPIFixture(t)
	keys, err := security.ParseKeyring("snapshot-test=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	q, err := queue.NewInsertOnlyClient(f.pool, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	svc := syncmodule.NewService(syncpg.NewStore(f.pool), security.NewCursorCodec(keys), clock.Real{}).WithSnapshots(syncpg.NewSnapshotStore(f.pool, q))
	s := f.services
	f.server = httptest.NewServer(httpapi.NewRouter(httpapi.Deps{Logger: quietLogger(), CORSOrigins: []string{f.origin}, Identity: s.Identity, Sessions: s.Sessions, Profile: s.Profile, Trips: s.Trips, Categories: s.Categories, Members: s.Members, Ledger: s.Ledger, Packing: s.Packing, Todos: s.Todos, Itinerary: s.Itinerary, Sync: svc}))
	t.Cleanup(f.server.Close)
	email, password := uniqueEmail(), "correct horse battery"
	reg := f.registerWeb(email, password)
	owner := uuid.MustParse(reg.data()["account"].(map[string]any)["id"].(string))
	login := f.do(request{method: http.MethodPost, path: "/auth/login", body: map[string]any{"email": email, "password": password, "client": map[string]any{"kind": "harmony", "device_id": uuid.NewString(), "device_name": "snapshot test"}}})
	expectStatus(t, login, 200, "")
	var epoch, session uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT sync_epoch FROM account_sync_state WHERE account_id=$1`, owner).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM account_sessions WHERE account_id=$1 AND client_kind='harmony'`, owner).Scan(&session); err != nil {
		t.Fatal(err)
	}
	return &snapshotFixture{apiFixture: f, svc: svc, owner: owner, epoch: epoch, a: actor.Actor{AccountID: owner, SessionID: session, ClientKind: actor.ClientHarmony}, token: login.data()["access_token"].(string), webToken: reg.data()["access_token"].(string), rawURL: raw}
}

func (f *snapshotFixture) sql(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *snapshotFixture) newTrip(name string) uuid.UUID {
	return uuid.MustParse(f.createTrip(f.webToken, map[string]any{"name": name, "start_date": "2026-10-01", "end_date": "2026-10-04"})["id"].(string))
}
func (f *snapshotFixture) createSnapshot(key uuid.UUID, purpose string, ids []uuid.UUID) apiResponse {
	return f.do(request{method: http.MethodPost, path: "/sync/snapshots", token: f.token, headers: map[string]string{"Idempotency-Key": key.String(), "X-Tripfolio-Sync-Version": "2"}, body: syncmodule.SnapshotInput{SyncEpoch: f.epoch, Purpose: purpose, SelectedTripIDs: ids}})
}
func (f *snapshotFixture) snapshot(purpose string, ids []uuid.UUID) syncmodule.Snapshot {
	f.t.Helper()
	r := f.createSnapshot(uuid.New(), purpose, ids)
	expectStatus(f.t, r, 202, "")
	var body struct {
		Data syncmodule.Snapshot `json:"data"`
	}
	if err := json.Unmarshal(r.Raw, &body); err != nil {
		f.t.Fatal(err)
	}
	return body.Data
}
func (f *snapshotFixture) build(id uuid.UUID) {
	f.t.Helper()
	if err := f.svc.BuildSnapshot(context.Background(), id, true); err != nil {
		f.t.Fatal(err)
	}
}
func (f *snapshotFixture) pages(id uuid.UUID) ([]syncmodule.SnapshotItem, syncmodule.SnapshotPage) {
	f.t.Helper()
	var out []syncmodule.SnapshotItem
	cursor := ""
	var page syncmodule.SnapshotPage
	for n := 0; n < 1000; n++ {
		r := f.do(request{method: http.MethodGet, path: "/sync/snapshots/" + id.String() + "/items?limit=3&cursor=" + url.QueryEscape(cursor), token: f.token, headers: map[string]string{"X-Tripfolio-Sync-Version": "2"}})
		expectStatus(f.t, r, 200, "")
		page = syncmodule.SnapshotPage{}
		if err := json.Unmarshal(r.Raw, &page); err != nil {
			f.t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.Ordinal != strconv.Itoa(len(out)+1) {
				f.t.Fatalf("ordinal gap: %+v", item)
			}
			out = append(out, item)
		}
		if !page.HasMore {
			return out, page
		}
		if page.NextCursor == nil || page.BaselineCursor != nil {
			f.t.Fatal("invalid intermediate cursor")
		}
		cursor = *page.NextCursor
	}
	f.t.Fatal("snapshot pagination did not finish")
	return nil, page
}

func TestHTTPSyncSnapshots(t *testing.T) {
	f := newSnapshotFixture(t)
	ctx := context.Background()
	selected := f.newTrip("snapshot original")
	foreign := f.registerWeb(uniqueEmail(), "correct horse battery")
	foreignToken := foreign.data()["access_token"].(string)
	foreignTrip := uuid.MustParse(f.createTrip(foreignToken, map[string]any{"name": "foreign private trip", "start_date": "2026-10-01", "end_date": "2026-10-04"})["id"].(string))
	other := f.newTrip("unselected")
	trashed := f.newTrip("trashed")
	f.sql(`UPDATE trips SET deleted_at=now(),purge_after_at=now()+interval '30 days' WHERE id=$1`, trashed)
	for _, c := range []struct {
		path string
		body map[string]any
	}{
		{"packing-items", map[string]any{"id": uuid.NewString(), "name": "Passport", "category": "documents"}},
		{"todos", map[string]any{"id": uuid.NewString(), "title": "book hotel"}},
		{"itinerary-items", map[string]any{"id": uuid.NewString(), "title": "visit", "kind": "attraction", "scheduled_on": "2026-10-02"}},
	} {
		expectStatus(t, f.do(request{method: http.MethodPost, path: "/trips/" + selected.String() + "/" + c.path, token: f.webToken, headers: f.authHeaders(nil), body: c.body}), 201, "")
	}
	// Nonzero persisted order and >2^53 versions must survive the JSON projection.
	f.sql(`UPDATE packing_items SET sort_order=17,version=9007199254740993 WHERE trip_id=$1`, selected)
	f.sql(`UPDATE todo_items SET sort_order=23 WHERE trip_id=$1`, selected)
	asset, reservation, document, photo := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.sql(`INSERT INTO assets(id,account_id,trip_id,scope,original_name,status,object_key,media_type,byte_size,sha256) VALUES($1,$2,$3,'trip','photo.jpg','ready','private-never-send/'||$1::uuid::text,'image/jpeg',1024,decode(repeat('00',32),'hex'))`, asset, f.owner, selected)
	f.sql(`INSERT INTO reservations(id,account_id,trip_id,kind,title) VALUES($1,$2,$3,'lodging','hotel')`, reservation, f.owner, selected)
	f.sql(`INSERT INTO documents(id,account_id,trip_id,title,asset_id,reservation_id) VALUES($1,$2,$3,'ticket',$4,$5)`, document, f.owner, selected, asset, reservation)
	f.sql(`INSERT INTO photos(id,account_id,trip_id,asset_id,recorded_on) VALUES($1,$2,$3,$4,'2026-10-02')`, photo, f.owner, selected, asset)
	var category uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM expense_categories WHERE account_id=$1 AND deleted_at IS NULL ORDER BY id LIMIT 1`, f.owner).Scan(&category); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/trips/" + selected.String() + "/ledger-entries", token: f.webToken, headers: f.authHeaders(nil), body: map[string]any{"id": uuid.NewString(), "kind": "expense", "amount": "120.50", "category_id": category, "attachment_asset_ids": []uuid.UUID{asset}}}), 201, "")
	key := uuid.New()
	r := f.createSnapshot(key, "baseline", []uuid.UUID{selected, selected})
	expectStatus(t, r, 202, "")
	var wrapped struct {
		Data syncmodule.Snapshot `json:"data"`
	}
	if err := json.Unmarshal(r.Raw, &wrapped); err != nil {
		t.Fatal(err)
	}
	meta := wrapped.Data
	expectStatus(t, f.createSnapshot(uuid.New(), "baseline", []uuid.UUID{foreignTrip}), 404, "RESOURCE_NOT_FOUND")
	foreignLoginEmail := foreign.data()["account"].(map[string]any)["email"].(string)
	foreignLogin := f.do(request{method: http.MethodPost, path: "/auth/login", body: map[string]any{"email": foreignLoginEmail, "password": "correct horse battery", "client": map[string]any{"kind": "harmony", "device_id": uuid.NewString(), "device_name": "foreign"}}})
	expectStatus(t, foreignLogin, 200, "")
	for _, suffix := range []string{"", "/items"} {
		expectStatus(t, f.do(request{method: http.MethodGet, path: "/sync/snapshots/" + meta.ID.String() + suffix, token: foreignLogin.data()["access_token"].(string), headers: map[string]string{"X-Tripfolio-Sync-Version": "2"}}), 404, "RESOURCE_NOT_FOUND")
	}
	r2 := f.createSnapshot(key, "baseline", []uuid.UUID{selected})
	expectStatus(t, r2, 202, "")
	var replay struct {
		Data syncmodule.Snapshot `json:"data"`
	}
	_ = json.Unmarshal(r2.Raw, &replay)
	if replay.Data.ID != meta.ID || meta.Status != "queued" || meta.HighWaterSeq != nil || meta.ItemCount != "0" {
		t.Fatal("create idempotency/state mismatch")
	}
	expectStatus(t, f.createSnapshot(key, "baseline", []uuid.UUID{}), 409, "IDEMPOTENCY_CONFLICT")
	var jobs int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='sync_snapshot' AND args->>'id'=$1`, meta.ID.String()).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("transactional job count %d: %v", jobs, err)
	}
	// Run the actual registered River worker, then replay it without extra items.
	workers := river.NewWorkers()
	riverjobs.RegisterWorkers(workers, riverjobs.Deps{Sync: f.svc, Logger: quietLogger()})
	client, err := queue.NewWorkerClient(f.pool, quietLogger(), workers, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Stop(end)
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		meta, err = f.svc.GetSnapshot(ctx, f.a, "2", meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if meta.Status == "ready" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if meta.Status != "ready" {
		t.Fatalf("River build status=%s", meta.Status)
	}
	if err = client.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	f.build(meta.ID)
	all, last := f.pages(meta.ID)
	if last.BaselineCursor == nil || last.ItemCount != strconv.Itoa(len(all)) {
		t.Fatal("baseline did not finish")
	}
	kinds := map[string]bool{}
	tripIDs := map[uuid.UUID]bool{}
	for _, item := range all {
		kinds[item.EntityType] = true
		if bytes.Contains(item.Data, []byte("private-never-send")) || bytes.Contains(item.Data, []byte("password_hash")) {
			t.Fatal("private field escaped projection")
		}
		if item.EntityType == "trip" {
			if item.EntityID == foreignTrip {
				t.Fatal("foreign account data leaked")
			}
			tripIDs[item.EntityID] = true
		} else if item.TripID != nil && *item.TripID != selected {
			t.Fatalf("unselected detail: %+v", item)
		}
		if item.EntityType == "packing_item" && (item.Version != "9007199254740993" || !bytes.Contains(item.Data, []byte(`"sort_order":17`))) {
			t.Fatalf("packing precision/order: %s", item.Data)
		}
	}
	if len(kinds) != 11 || !tripIDs[selected] || !tripIDs[other] || !tripIDs[trashed] {
		t.Fatalf("incomplete snapshot: types=%v trips=%v", kinds, tripIDs)
	}
	var refs int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM snapshot_asset_refs WHERE snapshot_id=$1`, meta.ID).Scan(&refs); err != nil || refs != 1 {
		t.Fatalf("asset references %d: %v", refs, err)
	}
	got := f.do(request{method: http.MethodGet, path: "/trips/" + selected.String(), token: f.webToken})
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/trips/" + selected.String(), token: f.webToken, headers: f.authHeaders(map[string]string{"If-Match": got.Header.Get("ETag")}), body: map[string]any{"name": "newer than snapshot"}}), 200, "")
	again, _ := f.pages(meta.ID)
	aJSON, _ := json.Marshal(all)
	bJSON, _ := json.Marshal(again)
	if !bytes.Equal(aJSON, bJSON) {
		t.Fatal("published items mutated")
	}
	delta, err := f.svc.Changes(ctx, f.a, "2", *last.BaselineCursor, 500)
	if err != nil || len(delta.Changes) != 1 {
		t.Fatalf("snapshot checkpoint failed: %+v %v", delta, err)
	}
	reload := f.snapshot("trip_reload", []uuid.UUID{selected})
	f.build(reload.ID)
	reloadItems, reloadLast := f.pages(reload.ID)
	if reloadLast.BaselineCursor != nil {
		t.Fatal("reload advanced global checkpoint")
	}
	catCount := 0
	for _, item := range reloadItems {
		if item.EntityType == "expense_category" {
			catCount++
		}
		if item.TripID != nil && *item.TripID != selected {
			t.Fatal("reload escaped selected scope")
		}
	}
	if catCount != 1 {
		t.Fatalf("reload dependency categories: %d", catCount)
	}
	empty := f.snapshot("baseline", []uuid.UUID{})
	f.build(empty.ID)
	baseItems, _ := f.pages(empty.ID)
	for _, item := range baseItems {
		if item.EntityType != "trip" && item.EntityType != "expense_category" {
			t.Fatal("empty selection contains detail")
		}
	}
	expectStatus(t, f.createSnapshot(uuid.New(), "baseline", []uuid.UUID{uuid.New()}), 404, "RESOURCE_NOT_FOUND")
	expectStatus(t, f.createSnapshot(uuid.New(), "trip_reload", []uuid.UUID{trashed}), 410, "TRIP_DELETED")
	f.sql(`UPDATE data_snapshots SET expires_at=now()-interval '1 second' WHERE id=$1`, reload.ID)
	_, err = f.svc.SnapshotItems(ctx, f.a, "2", reload.ID, "", 3)
	if err == nil {
		t.Fatal("expired snapshot readable")
	}
	f.sql(`UPDATE account_sync_state SET retained_after_seq=last_seq WHERE account_id=$1`, f.owner)
	_, err = f.svc.SnapshotItems(ctx, f.a, "2", meta.ID, "", 3)
	if err == nil {
		t.Fatal("unrecoverable snapshot readable")
	}
	f.sql(`UPDATE account_sync_state SET sync_epoch=gen_random_uuid() WHERE account_id=$1`, f.owner)
	_, err = f.svc.GetSnapshot(ctx, f.a, "2", empty.ID)
	if err == nil {
		t.Fatal("old epoch snapshot readable")
	}
}

type snapshotTraceKey struct{}
type snapshotBarrier struct {
	captured, release chan struct{}
	stage             bool
	once              sync.Once
}

func (b *snapshotBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, snapshotTraceKey{}, d.SQL)
}
func (b *snapshotBarrier) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	sql, _ := ctx.Value(snapshotTraceKey{}).(string)
	if d.Err != nil {
		return
	}
	if strings.HasPrefix(sql, "CREATE TEMP TABLE tripfolio_snapshot_stage") {
		b.stage = true
	}
	if b.stage && strings.EqualFold(sql, "commit") {
		b.once.Do(func() {
			close(b.captured)
			select {
			case <-b.release:
			case <-ctx.Done():
			}
		})
	}
}

type noSnapshotObjects struct{}

func (noSnapshotObjects) ListKeys(context.Context, string) ([]string, error) { return nil, nil }
func (noSnapshotObjects) PurgeKey(context.Context, string) error             { return nil }

func (f *snapshotFixture) purge(id uuid.UUID) {
	f.t.Helper()
	job := uuid.New()
	f.sql(`UPDATE trips SET deleted_at=now(),purge_after_at=now(),purge_requested_at=now() WHERE id=$1`, id)
	f.sql(`INSERT INTO deletion_jobs(id,owner_account_id,scope,target_trip_id) VALUES($1,$2,'trip',$3)`, job, f.owner, id)
	p := trip.NewPurger(adminpg.NewTripPurgeStore(f.pool), noSnapshotObjects{})
	if err := p.Purge(context.Background(), trip.PurgeJobArgs{JobID: job, AccountID: f.owner, TripID: id}); err != nil {
		f.t.Fatal(err)
	}
}

func TestSyncSnapshotCaptureCleanupAndPurge(t *testing.T) {
	for _, scenario := range []string{"queued", "building", "ready", "cancel", "concurrent_write"} {
		t.Run(scenario, func(t *testing.T) {
			f := newSnapshotFixture(t)
			tripID := f.newTrip("captured old value")
			meta := f.snapshot("baseline", []uuid.UUID{})
			barrier := &snapshotBarrier{captured: make(chan struct{}), release: make(chan struct{})}
			cfg, err := pgxpool.ParseConfig(f.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			cfg.MaxConns = 1
			cfg.MinConns = 0
			cfg.ConnConfig.Tracer = barrier
			pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			store := syncpg.NewSnapshotStore(pool, nil)
			if scenario == "queued" {
				f.purge(tripID)
				if err = store.Build(context.Background(), meta.ID, true); err != nil {
					t.Fatal(err)
				}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- store.Build(ctx, meta.ID, true) }()
				select {
				case <-barrier.captured:
				case err := <-done:
					t.Fatalf("build exited before capture: %v", err)
				case <-ctx.Done():
					t.Fatal("capture did not arrive")
				}
				switch scenario {
				case "building":
					f.purge(tripID)
				case "cancel":
					cancel()
				case "concurrent_write":
					f.sql(`UPDATE trips SET name='new after capture',version=version+1 WHERE id=$1`, tripID)
				}
				close(barrier.release)
				err = <-done
				if scenario == "cancel" {
					if err == nil {
						t.Fatal("cancelled build succeeded")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if scenario == "ready" {
					f.purge(tripID)
				}
				if scenario == "concurrent_write" {
					all, _ := f.pages(meta.ID)
					found := false
					for _, item := range all {
						if item.EntityID == tripID {
							found = bytes.Contains(item.Data, []byte("captured old value"))
						}
					}
					if !found {
						t.Fatal("capture mixed later transaction")
					}
				}
			}
			var temp int
			if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_class WHERE relnamespace=pg_my_temp_schema() AND relname='tripfolio_snapshot_stage'`).Scan(&temp); err != nil || temp != 0 {
				t.Fatalf("pool retained staging table: %d %v", temp, err)
			}
			if scenario != "concurrent_write" {
				var published int
				if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM snapshot_items WHERE snapshot_id=$1`, meta.ID).Scan(&published); err != nil || published != 0 {
					t.Fatalf("stale/partial items survived: %d %v", published, err)
				}
				var ready bool
				if err := f.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM data_snapshots WHERE id=$1 AND status='ready')`, meta.ID).Scan(&ready); err != nil || ready {
					t.Fatal("invalidated or cancelled snapshot published")
				}
			}
		})
	}
}

func TestSyncSnapshotRetryAndSortMigration(t *testing.T) {
	f := newSnapshotFixture(t)
	ctx := context.Background()
	tripID := f.newTrip("migration")
	otherTrip := f.newTrip("other migration partition")
	foreign := f.registerWeb(uniqueEmail(), "correct horse battery")
	foreignOwner := uuid.MustParse(foreign.data()["account"].(map[string]any)["id"].(string))
	foreignTrip := uuid.MustParse(f.createTrip(foreign.data()["access_token"].(string), map[string]any{"name": "foreign migration partition", "start_date": "2026-10-01", "end_date": "2026-10-04"})["id"].(string))
	for range 2 {
		if err := bootstrap.RunMigrate(ctx, config.Config{DatabaseURL: f.rawURL}, quietLogger(), []string{"down"}); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		_ = bootstrap.RunMigrate(context.Background(), config.Config{DatabaseURL: f.rawURL}, quietLogger(), []string{"up"})
	}()
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, id := range ids {
		f.sql(`INSERT INTO packing_items(id,account_id,trip_id,name,category,created_at,updated_at,deleted_at) VALUES($1,$2,$3,$4,'documents',now()+$5*interval '1 second',now()+$5*interval '1 second',CASE WHEN $5=1 THEN now() END)`, id, f.owner, tripID, fmt.Sprintf("item%d", i), i)
		f.sql(`INSERT INTO todo_items(id,account_id,trip_id,title,due_on,deleted_at) VALUES($1,$2,$3,'todo',date '2026-10-01'+$4::int,CASE WHEN $4=1 THEN now() END)`, uuid.New(), f.owner, tripID, i)
	}
	type partition struct {
		owner, trip uuid.UUID
		ids         []uuid.UUID
	}
	partitions := []partition{{f.owner, otherTrip, nil}, {foreignOwner, foreignTrip, nil}}
	for n := range partitions {
		p := &partitions[n]
		p.ids = []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
		sort.Slice(p.ids, func(i, j int) bool { return p.ids[i].String() < p.ids[j].String() })
		// Equal legacy sort keys force UUID ordering, independent of insertion order.
		for i := len(p.ids) - 1; i >= 0; i-- {
			id := p.ids[i]
			f.sql(`INSERT INTO packing_items(id,account_id,trip_id,name,category,created_at,updated_at) VALUES($1,$2,$3,$4,'documents','2026-01-01','2026-01-01')`, id, p.owner, p.trip, fmt.Sprintf("tie%d", i))
			f.sql(`INSERT INTO todo_items(id,account_id,trip_id,title) VALUES($1,$2,$3,'tie without due date')`, id, p.owner, p.trip)
		}
	}
	if err := bootstrap.RunMigrate(ctx, config.Config{DatabaseURL: f.rawURL}, quietLogger(), []string{"up"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range partitions {
		for i, id := range p.ids {
			var packingOrder, todoOrder int
			if err := f.pool.QueryRow(ctx, `SELECT p.sort_order,t.sort_order FROM packing_items p JOIN todo_items t ON t.id=p.id WHERE p.id=$1`, id).Scan(&packingOrder, &todoOrder); err != nil || packingOrder != i || todoOrder != i {
				t.Fatalf("partition/tie order: packing=%d todo=%d want=%d: %v", packingOrder, todoOrder, i, err)
			}
		}
	}
	for _, resource := range []struct{ path, field string }{{"packing-items", "name"}, {"todos", "title"}} {
		id := uuid.New()
		body := map[string]any{"id": id, resource.field: "legacy insert"}
		if resource.path == "packing-items" {
			body["category"] = "documents"
		}
		expectStatus(t, f.do(request{method: http.MethodPost, path: "/trips/" + otherTrip.String() + "/" + resource.path, token: f.webToken, headers: f.authHeaders(nil), body: body}), 201, "")
		var order int
		if err := f.pool.QueryRow(ctx, `SELECT sort_order FROM packing_items WHERE id=$1 UNION ALL SELECT sort_order FROM todo_items WHERE id=$1`, id).Scan(&order); err != nil || order != 3 {
			t.Fatalf("legacy insert appended: %d %v", order, err)
		}
	}
	for i, id := range ids {
		var order int
		if err := f.pool.QueryRow(ctx, `SELECT sort_order FROM packing_items WHERE id=$1`, id).Scan(&order); err != nil || order != i {
			t.Fatalf("packing migration order=%d want=%d: %v", order, i, err)
		}
	}
	var orders []int32
	if err := f.pool.QueryRow(ctx, `SELECT array_agg(sort_order ORDER BY due_on,id) FROM todo_items WHERE trip_id=$1`, tripID).Scan(&orders); err != nil || fmt.Sprint(orders) != "[0 1 2]" {
		t.Fatalf("todo migration order=%v: %v", orders, err)
	}
	meta := f.snapshot("baseline", []uuid.UUID{tripID})
	f.sql(`CREATE FUNCTION public.h07_fail_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='ready' THEN RAISE EXCEPTION 'H07 publication failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER h07_fail_snapshot BEFORE UPDATE ON data_snapshots FOR EACH ROW EXECUTE FUNCTION public.h07_fail_snapshot()`)
	if err := f.svc.BuildSnapshot(ctx, meta.ID, false); err == nil {
		t.Fatal("fault injection did not fail publication")
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM snapshot_items WHERE snapshot_id=$1`, meta.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed publication leaked items")
	}
	f.sql(`DROP TRIGGER h07_fail_snapshot ON data_snapshots; DROP FUNCTION public.h07_fail_snapshot()`)
	f.build(meta.ID)
	f.build(meta.ID)
	all, _ := f.pages(meta.ID)
	seen := 0
	for _, item := range all {
		if item.EntityType == "packing_item" || item.EntityType == "todo" {
			seen++
		}
	}
	if seen != 4 {
		t.Fatalf("soft-deleted rows leaked: %d", seen)
	}
	var generation int
	if err := f.pool.QueryRow(ctx, `SELECT capture_generation FROM data_snapshots WHERE id=$1`, meta.ID).Scan(&generation); err != nil || generation != 2 {
		t.Fatalf("retry generation=%d: %v", generation, err)
	}
}
