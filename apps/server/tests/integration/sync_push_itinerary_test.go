package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	syncpg "tripfolio/server/internal/adapters/postgres/sync"
	"tripfolio/server/internal/adapters/queue"
	"tripfolio/server/internal/foundation/clock"
	syncmodule "tripfolio/server/internal/modules/sync"
)

func newItineraryPushFixture(t *testing.T) *pushFixture {
	f := newPushFixture(t)
	q, err := queue.NewInsertOnlyClient(f.pool, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	f.svc.WithPush(syncpg.NewPushStore(pgcore.NewWriter(f.pool, q, clock.Real{}, quietLogger())))
	return f
}

func itineraryRevision(t *testing.T, f *pushFixture, trip uuid.UUID, date string) string {
	t.Helper()
	var revision string
	err := f.pool.QueryRow(context.Background(), `SELECT 'sha256:'||encode(sha256(convert_to($3::text||chr(10)||'itinerary_day'||chr(10)||$2::text||'/'||$4::text||chr(10)||coalesce(string_agg(id::text||':'||version::text||chr(10),'' ORDER BY id),''),'UTF8')),'hex') FROM itinerary_items WHERE account_id=$1 AND trip_id=$2::uuid AND scheduled_on=$4::date AND deleted_at IS NULL`, f.owner, trip.String(), f.epoch.String(), date).Scan(&revision)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
func itineraryGuard(trip uuid.UUID, date, revision string) syncmodule.GuardReference {
	return syncmodule.GuardReference{Kind: "itinerary_day", ScopeID: trip.String() + "/" + date, Revision: &revision}
}
func itineraryCreate(t *testing.T, f *pushFixture, trip uuid.UUID, date, title string) syncmodule.Operation {
	op := itemOp("itinerary_item", "create", trip, uuid.New(), nil, map[string]any{"title": title, "kind": "other", "scheduled_on": date})
	op.Guards = []syncmodule.GuardReference{itineraryGuard(trip, date, itineraryRevision(t, f, trip, date))}
	return op
}
func itineraryOrder(t *testing.T, f *pushFixture, trip uuid.UUID, dates []string, ids [][]uuid.UUID) syncmodule.Operation {
	days := make([]map[string]any, 0, len(dates))
	guards := make([]syncmodule.GuardReference, 0, len(dates))
	for i, date := range dates {
		days = append(days, map[string]any{"date": date, "ordered_ids": ids[i]})
		guards = append(guards, itineraryGuard(trip, date, itineraryRevision(t, f, trip, date)))
	}
	op := itemOp("itinerary_item", "reorder", trip, uuid.Nil, nil, map[string]any{"days": days})
	op.Guards = guards
	return op
}

func TestSyncPushItineraryLifecycle(t *testing.T) {
	f := newItineraryPushFixture(t)
	trip := f.newTrip("itinerary")
	a := itineraryCreate(t, f, trip, "2026-10-02", "A")
	applied(t, f.push(a).Results[0])
	b := itineraryCreate(t, f, trip, "2026-10-02", "B")
	b.Guards = []syncmodule.GuardReference{{Kind: "itinerary_day", ScopeID: trip.String() + "/2026-10-02", OperationID: &a.OperationID}}
	b.DependsOn = []uuid.UUID{a.OperationID}
	applied(t, f.push(b).Results[0])
	order := itineraryOrder(t, f, trip, []string{"2026-10-02", "2026-10-03"}, [][]uuid.UUID{{*a.EntityID}, {*b.EntityID}})
	r := f.push(order).Results[0]
	applied(t, r)
	if itemVersion(t, r, *a.EntityID) != "1" || itemVersion(t, r, *b.EntityID) != "2" || len(r.Result.ScopeRevisions) != 2 {
		t.Fatal("reorder original facts missing")
	}
	noop := itineraryOrder(t, f, trip, []string{"2026-10-02", "2026-10-03"}, [][]uuid.UUID{{*a.EntityID}, {*b.EntityID}})
	n := f.push(noop).Results[0]
	applied(t, n)
	if n.Result.CommitCursor != nil || len(n.Result.References) != 2 || len(n.Result.ScopeRevisions) != 2 {
		t.Fatal("no-op facts missing")
	}
	patch := itemOp("itinerary_item", "update", trip, *b.EntityID, refBase(noop.OperationID), map[string]any{"notes": "after no-op"}, noop.OperationID)
	applied(t, f.push(patch).Results[0])
	replay := f.push(noop).Results[0]
	if replay.Status != "replayed" || itemVersion(t, replay, *b.EntityID) != "2" {
		t.Fatal("no-op replay drift")
	}
	del := itemOp("itinerary_item", "delete", trip, *b.EntityID, refBase(patch.OperationID), map[string]any{}, patch.OperationID)
	del.Guards = []syncmodule.GuardReference{{Kind: "itinerary_day", ScopeID: trip.String() + "/2026-10-03", OperationID: &patch.OperationID}}
	applied(t, f.push(del).Results[0])
	if x := f.push(patch).Results[0]; x.Status != "replayed" || itemVersion(t, x, *b.EntityID) != "3" {
		t.Fatal("original patch receipt lost after delete")
	}
}

func TestSyncPushItineraryGuardAndBoundary(t *testing.T) {
	f := newItineraryPushFixture(t)
	trip := f.newTrip("guards")
	a := itineraryCreate(t, f, trip, "2026-10-02", "A")
	missing := a
	missing.OperationID = uuid.New()
	missing.Guards = []syncmodule.GuardReference{}
	if x := f.push(missing).Results[0]; x.Error == nil || x.Error.Code != "COLLECTION_BASE_REQUIRED" {
		t.Fatalf("missing guard %+v", x.Error)
	}
	created := f.push(a).Results[0]
	applied(t, created)
	stale := a
	stale.OperationID = uuid.New()
	id := uuid.New()
	stale.EntityID = &id
	if x := f.push(stale).Results[0]; x.Status != "conflict" {
		t.Fatalf("stale guard %+v", x.Error)
	}
	bad := itineraryOrder(t, f, trip, []string{"2026-10-02"}, [][]uuid.UUID{{}})
	if x := f.push(bad).Results[0]; x.Status == "applied" {
		t.Fatal("incomplete day accepted")
	}
	other := f.newTrip("other")
	foreign := itemOp("itinerary_item", "update", other, *a.EntityID, versionBase("1"), map[string]any{"notes": "bad"})
	if x := f.push(foreign).Results[0]; x.Status == "applied" {
		t.Fatal("cross-trip write")
	}
	for _, payload := range []map[string]any{{"scheduled_on": "2026-10-03"}, {"sort_order": 2}, {"planned_start_set": true}} {
		if x := f.push(itemOp("itinerary_item", "update", trip, *a.EntityID, versionBase("1"), payload)).Results[0]; x.Status == "applied" {
			t.Fatal("forbidden patch field")
		}
	}
	// Model the entity stage of tripPurgeRun.removeBatch: persist the original
	// row's tombstone and remove the row atomically. A bare DELETE is not a purge.
	del := itemOp("itinerary_item", "delete", trip, *a.EntityID, versionBase("1"), map[string]any{})
	del.Guards = []syncmodule.GuardReference{itineraryGuard(trip, "2026-10-02", itineraryRevision(t, f, trip, "2026-10-02"))}
	deleted := f.push(del).Results[0]
	applied(t, deleted)
	tag, err := f.pool.Exec(context.Background(), `WITH stones AS (
		INSERT INTO entity_tombstones(account_id,entity_type,entity_id,trip_id,last_version,deleted_at,purged_at)
		SELECT account_id,'itinerary_item',id,trip_id,version,deleted_at,now()
		FROM itinerary_items WHERE account_id=$1 AND trip_id=$2 AND id=$3 AND deleted_at IS NOT NULL
		RETURNING entity_id
	) DELETE FROM itinerary_items i USING stones s WHERE i.account_id=$1 AND i.trip_id=$2 AND i.id=s.entity_id`, f.owner, trip, *a.EntityID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("purge fixture did not replace exactly one row with a tombstone: %v, rows=%d", err, tag.RowsAffected())
	}
	assertPurged := func() {
		t.Helper()
		var stone, row bool
		err := f.pool.QueryRow(context.Background(), `SELECT
			EXISTS(SELECT 1 FROM entity_tombstones WHERE account_id=$1 AND entity_type='itinerary_item' AND entity_id=$3 AND trip_id=$2 AND last_version=2 AND deleted_at IS NOT NULL AND purged_at IS NOT NULL),
			EXISTS(SELECT 1 FROM itinerary_items WHERE id=$3)`, f.owner, trip, *a.EntityID).Scan(&stone, &row)
		if err != nil || !stone || row {
			t.Fatalf("invalid purge state: tombstone=%t physical_row=%t error=%v", stone, row, err)
		}
	}
	assertPurged()
	recreate := itineraryCreate(t, f, trip, "2026-10-02", "again")
	recreate.EntityID = a.EntityID
	if x := f.push(recreate).Results[0]; x.Error == nil || x.Error.Code != "ID_ALREADY_USED" {
		t.Fatalf("tombstone reused: %+v", x.Error)
	}
	for _, original := range []struct {
		op     syncmodule.Operation
		result syncmodule.PushResult
	}{{a, created}, {del, deleted}} {
		replay := f.push(original.op).Results[0]
		if replay.Status != "replayed" || replay.Result == nil || replay.Result.Data != nil {
			t.Fatalf("purged original must replay with no current data: %+v", replay)
		}
		if !reflect.DeepEqual(replay.Result.References, original.result.Result.References) ||
			!reflect.DeepEqual(replay.Result.ScopeRevisions, original.result.Result.ScopeRevisions) ||
			!reflect.DeepEqual(replay.Result.CommitCursor, original.result.Result.CommitCursor) {
			t.Fatalf("purged replay changed original facts: before=%+v after=%+v", original.result.Result, replay.Result)
		}
	}
	assertPurged()
}

func TestSyncPushItineraryConcurrentAndRollback(t *testing.T) {
	f := newItineraryPushFixture(t)
	trip := f.newTrip("race")
	a := itineraryCreate(t, f, trip, "2026-10-02", "A")
	applied(t, f.push(a).Results[0])
	b := itineraryCreate(t, f, trip, "2026-10-02", "B")
	applied(t, f.push(b).Results[0])
	order := itineraryOrder(t, f, trip, []string{"2026-10-02", "2026-10-03"}, [][]uuid.UUID{{*b.EntityID}, {*a.EntityID}})
	other := order
	other.OperationID = uuid.New()
	var wg sync.WaitGroup
	results := make(chan syncmodule.PushResult, 2)
	errs := make(chan error, 2)
	for _, op := range []syncmodule.Operation{order, other} {
		wg.Add(1)
		go func(op syncmodule.Operation) {
			defer wg.Done()
			out, e := f.svc.Push(context.Background(), f.a, "2", f.input(op))
			if e != nil {
				errs <- e
				return
			}
			results <- out.Results[0]
		}(op)
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	counts := map[string]int{}
	for r := range results {
		counts[r.Status]++
	}
	if counts["applied"] != 1 || counts["conflict"] != 1 {
		t.Fatalf("concurrent results: %v", counts)
	}
	back := itineraryOrder(t, f, trip, []string{"2026-10-02", "2026-10-03"}, [][]uuid.UUID{{*a.EntityID, *b.EntityID}, {}})
	var seq int64
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	r1, r2 := itineraryRevision(t, f, trip, "2026-10-02"), itineraryRevision(t, f, trip, "2026-10-03")
	f.sql(`CREATE FUNCTION public.h07_i_receipt_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type='sync.itinerary_item.reorder' THEN RAISE EXCEPTION 'receipt failure'; END IF; RETURN NEW; END $$;CREATE TRIGGER h07_i_receipt_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION public.h07_i_receipt_fail()`)
	t.Cleanup(func() {
		f.sql(`DROP TRIGGER IF EXISTS h07_i_receipt_fail ON mutation_receipts;DROP FUNCTION IF EXISTS public.h07_i_receipt_fail()`)
	})
	if x := f.push(back).Results[0]; x.Status != "failed" {
		t.Fatalf("receipt fault %+v", x.Error)
	}
	var unchanged bool
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seq=$2 AND NOT EXISTS(SELECT 1 FROM mutation_receipts WHERE account_id=$1 AND operation_id=$3) FROM account_sync_state WHERE account_id=$1`, f.owner, seq, back.OperationID).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("partial commit %v", err)
	}
	if r1 != itineraryRevision(t, f, trip, "2026-10-02") || r2 != itineraryRevision(t, f, trip, "2026-10-03") {
		t.Fatal("failed reorder changed rows")
	}
}

func TestSyncPushItineraryWebProjection(t *testing.T) {
	f := newItineraryPushFixture(t)
	trip := f.newTrip("web")
	a := itineraryCreate(t, f, trip, "2026-10-02", "A")
	applied(t, f.push(a).Results[0])
	baseline := f.snapshot("baseline", []uuid.UUID{trip})
	f.build(baseline.ID)
	_, last := f.pages(baseline.ID)
	v := `"1"`
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/trips/" + trip.String() + "/itinerary-items/" + a.EntityID.String(), token: f.webToken, headers: f.authHeaders(map[string]string{"If-Match": v}), body: map[string]any{"notes": "web edit"}}), 200, "")
	delta, err := f.svc.Changes(context.Background(), f.a, "2", *last.BaselineCursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range delta.Changes {
		if c.EntityID == *a.EntityID {
			var data map[string]any
			if json.Unmarshal(c.Data, &data) != nil || data["notes"] != "web edit" {
				t.Fatal("web delta")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing web delta")
	}
	snapshot := f.snapshot("trip_reload", []uuid.UUID{trip})
	f.build(snapshot.ID)
	items, _ := f.pages(snapshot.ID)
	found = false
	for _, item := range items {
		if item.EntityID == *a.EntityID {
			var data map[string]any
			_ = json.Unmarshal(item.Data, &data)
			if data["notes"] != "web edit" {
				t.Fatal("web snapshot")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing snapshot item")
	}
	f.sql(`UPDATE itinerary_items SET sort_order=2147483647 WHERE id=$1`, *a.EntityID)
	overflow := itineraryCreate(t, f, trip, "2026-10-02", "overflow")
	if x := f.push(overflow).Results[0]; x.Status == "applied" {
		t.Fatal("sort overflow accepted")
	}
}

func TestSyncPushItineraryMergeAndRetry(t *testing.T) {
	f := newItineraryPushFixture(t)
	trip := f.newTrip("merge")
	a := itineraryCreate(t, f, trip, "2026-10-02", "A")
	a.Payload = json.RawMessage(`{"title":"A","kind":"other","scheduled_on":"2026-10-02","planned_start_local":"2026-10-02T09:00:00","planned_end_local":"2026-10-02T10:00:00"}`)
	var wg sync.WaitGroup
	results := make(chan syncmodule.PushResult, 3)
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, e := f.svc.Push(context.Background(), f.a, "2", f.input(a))
			if e != nil {
				errs <- e
				return
			}
			results <- out.Results[0]
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	counts := map[string]int{}
	for r := range results {
		counts[r.Status]++
		if itemVersion(t, r, *a.EntityID) != "1" {
			t.Fatal("retry version changed")
		}
	}
	if counts["applied"] != 1 || counts["replayed"] != 2 {
		t.Fatalf("duplicate create %v", counts)
	}
	start := itemOp("itinerary_item", "update", trip, *a.EntityID, versionBase("1"), map[string]any{"planned_start_local": "2026-10-02T09:30:00"})
	applied(t, f.push(start).Results[0])
	end := itemOp("itinerary_item", "update", trip, *a.EntityID, versionBase("1"), map[string]any{"planned_end_local": "2026-10-02T11:00:00"})
	if x := f.push(end).Results[0]; x.Status != "conflict" {
		t.Fatalf("associated time fields merged: %+v", x.Error)
	}
	notes := itemOp("itinerary_item", "update", trip, *a.EntityID, versionBase("1"), map[string]any{"notes": "independent"})
	applied(t, f.push(notes).Results[0])
	noop := itemOp("itinerary_item", "update", trip, *a.EntityID, versionBase("3"), map[string]any{"notes": "independent"})
	n := f.push(noop).Results[0]
	applied(t, n)
	if n.Result.CommitCursor != nil || itemVersion(t, n, *a.EntityID) != "3" {
		t.Fatal("no-op update")
	}
	next := itemOp("itinerary_item", "update", trip, *a.EntityID, refBase(noop.OperationID), map[string]any{"notes": "next"}, noop.OperationID)
	applied(t, f.push(next).Results[0])
	replay := f.push(noop).Results[0]
	if replay.Status != "replayed" || itemVersion(t, replay, *a.EntityID) != "3" || replay.Result.Data.(map[string]any)["version"] != "4" {
		t.Fatal("replay original no-op facts")
	}
	changed := a
	changed.Payload = json.RawMessage(`{"title":"B","kind":"other","scheduled_on":"2026-10-02"}`)
	if x := f.push(changed).Results[0]; x.Error == nil || x.Error.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("fingerprint conflict not enforced")
	}
}
