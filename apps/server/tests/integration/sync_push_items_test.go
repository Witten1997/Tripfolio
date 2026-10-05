package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"strconv"
	"sync"
	"testing"
	syncmodule "tripfolio/server/internal/modules/sync"
)

func itemOp(kind, action string, trip, id uuid.UUID, base *syncmodule.BaseReference, payload any, deps ...uuid.UUID) syncmodule.Operation {
	raw, _ := json.Marshal(payload)
	op := syncmodule.Operation{OperationID: uuid.New(), Type: kind + "." + action, EntityType: kind, TripID: &trip, EntityID: &id, Base: base, Guards: []syncmodule.GuardReference{}, DependsOn: append([]uuid.UUID{}, deps...), Payload: raw}
	if action == "reorder" {
		op.EntityID = nil
	}
	return op
}
func itemCreate(kind string, trip uuid.UUID, name string) syncmodule.Operation {
	p := map[string]any{"title": name}
	if kind == "packing_item" {
		p = map[string]any{"name": name, "category": "documents"}
	}
	return itemOp(kind, "create", trip, uuid.New(), nil, p)
}
func itemVersion(t *testing.T, r syncmodule.PushResult, id uuid.UUID) string {
	t.Helper()
	if r.Result == nil {
		t.Fatalf("no result: %+v", r)
	}
	for _, ref := range r.Result.References {
		if ref.ID == id && ref.Version != nil {
			return strconv.FormatInt(int64(*ref.Version), 10)
		}
	}
	t.Fatal("missing reference")
	return ""
}
func itemRevision(t *testing.T, r syncmodule.PushResult) string {
	t.Helper()
	if r.Result == nil || len(r.Result.ScopeRevisions) != 1 {
		t.Fatalf("missing revision: %+v", r)
	}
	return r.Result.ScopeRevisions[0].Revision
}
func orderOp(kind string, trip uuid.UUID, ids []uuid.UUID, rev string) syncmodule.Operation {
	op := itemOp(kind, "reorder", trip, uuid.Nil, nil, map[string]any{"ordered_ids": ids})
	guardKind := "packing_order"
	if kind == "todo" {
		guardKind = "todo_order"
	}
	op.Guards = []syncmodule.GuardReference{{Kind: guardKind, ScopeID: trip.String(), Revision: &rev}}
	return op
}
func applied(t *testing.T, r syncmodule.PushResult) {
	t.Helper()
	if r.Status != "applied" {
		t.Fatalf("not applied: %+v", r)
	}
}
func TestSyncPushItemsLifecycle(t *testing.T) {
	for _, kind := range []string{"packing_item", "todo"} {
		t.Run(kind, func(t *testing.T) {
			f := newPushFixture(t)
			trip := f.newTrip(kind)
			a, b := itemCreate(kind, trip, "first"), itemCreate(kind, trip, "second")
			out := f.push(a, b)
			for _, x := range out.Results {
				applied(t, x)
			}
			for i, x := range out.Results {
				if x.Result.Data.(map[string]any)["sort_order"] != float64(i) {
					t.Fatal("append order")
				}
			}
			action, field, target := "set_status", "status", any("packed")
			if kind == "todo" {
				action, field, target = "set_completed", "completed", true
			}
			set := itemOp(kind, action, trip, *a.EntityID, refBase(a.OperationID), map[string]any{field: target}, a.OperationID)
			noop := itemOp(kind, action, trip, *a.EntityID, refBase(set.OperationID), map[string]any{field: target}, set.OperationID)
			states := f.push(set, noop)
			for _, x := range states.Results {
				applied(t, x)
			}
			if itemVersion(t, states.Results[1], *a.EntityID) != "2" || states.Results[1].Result.CommitCursor != nil {
				t.Fatal("state no-op invented write")
			}
			if kind == "todo" && states.Results[1].Result.Data.(map[string]any)["completed_at"] == nil {
				t.Fatal("completed timestamp missing")
			}
			stale := itemOp(kind, action, trip, *a.EntityID, versionBase("1"), map[string]any{field: target})
			if x := f.push(stale).Results[0]; x.Status != "conflict" {
				t.Fatalf("stale same value %+v", x)
			}
			patchField := "name"
			if kind == "todo" {
				patchField = "title"
			}
			up := itemOp(kind, "update", trip, *a.EntityID, refBase(noop.OperationID), map[string]any{patchField: "changed"}, noop.OperationID)
			updated := f.push(up).Results[0]
			applied(t, updated)
			replay := f.push(noop).Results[0]
			if replay.Status != "replayed" || itemVersion(t, replay, *a.EntityID) != "2" || replay.Result.Data.(map[string]any)["version"] != "3" || itemRevision(t, replay) != itemRevision(t, states.Results[1]) {
				t.Fatal("immutable no-op facts changed")
			}
			reorder := orderOp(kind, trip, []uuid.UUID{*b.EntityID, *a.EntityID}, itemRevision(t, updated))
			reordered := f.push(reorder).Results[0]
			applied(t, reordered)
			noOrder := orderOp(kind, trip, []uuid.UUID{*b.EntityID, *a.EntityID}, itemRevision(t, reordered))
			same := f.push(noOrder).Results[0]
			applied(t, same)
			if same.Result.CommitCursor != nil {
				t.Fatal("same order logged")
			}
			dependency := orderOp(kind, trip, []uuid.UUID{*a.EntityID, *b.EntityID}, "")
			dependency.Guards[0].Revision = nil
			dependency.Guards[0].OperationID = &noOrder.OperationID
			dependency.DependsOn = []uuid.UUID{noOrder.OperationID}
			moved := f.push(dependency).Results[0]
			applied(t, moved)
			if x := f.push(noOrder).Results[0]; x.Status != "replayed" || itemRevision(t, x) != itemRevision(t, same) {
				t.Fatal("reorder original revision lost")
			}
			del := itemOp(kind, "delete", trip, *a.EntityID, versionBase(itemVersion(t, moved, *a.EntityID)), map[string]any{})
			applied(t, f.push(del).Results[0])
			reuse := itemCreate(kind, trip, "reused")
			reuse.EntityID = a.EntityID
			if x := f.push(reuse).Results[0]; x.Error == nil || x.Error.Code != "ID_ALREADY_USED" {
				t.Fatalf("reused ID %+v", x)
			}
		})
	}
}
func TestSyncPushItemsConcurrentGuards(t *testing.T) {
	for _, kind := range []string{"packing_item", "todo"} {
		t.Run(kind, func(t *testing.T) {
			f := newPushFixture(t)
			trip := f.newTrip("concurrency")
			a, b := itemCreate(kind, trip, "first"), itemCreate(kind, trip, "second")
			out := f.push(a, b)
			rev := itemRevision(t, out.Results[1])
			ids := []uuid.UUID{*b.EntityID, *a.EntityID}
			for _, bad := range [][]uuid.UUID{{*a.EntityID}, {*a.EntityID, uuid.New()}, {*a.EntityID, *a.EntityID}} {
				if x := f.push(orderOp(kind, trip, bad, rev)).Results[0]; x.Status == "applied" {
					t.Fatal("invalid set accepted")
				}
			}
			missing := orderOp(kind, trip, ids, rev)
			missing.Guards = nil
			missing.Guards = []syncmodule.GuardReference{}
			if x := f.push(missing).Results[0]; x.Error == nil || x.Error.Code != "COLLECTION_BASE_REQUIRED" {
				t.Fatalf("missing guard %+v", x)
			}
			ops := []syncmodule.Operation{orderOp(kind, trip, ids, rev), orderOp(kind, trip, ids, rev)}
			var wg sync.WaitGroup
			results := make(chan syncmodule.PushResult, 2)
			errs := make(chan error, 2)
			for _, op := range ops {
				wg.Add(1)
				go func(op syncmodule.Operation) {
					defer wg.Done()
					o, e := f.svc.Push(context.Background(), f.a, "2", f.input(op))
					if e != nil {
						errs <- e
						return
					}
					results <- o.Results[0]
				}(op)
			}
			wg.Wait()
			close(results)
			close(errs)
			for e := range errs {
				t.Fatal(e)
			}
			counts := map[string]int{}
			for x := range results {
				counts[x.Status]++
			}
			if counts["applied"] != 1 || counts["conflict"] != 1 {
				t.Fatalf("race %+v", counts)
			}
			// A receipt insertion failure must undo all reordered rows and their log sequence.
			current := f.push(orderOp(kind, trip, ids, itemRevisionFromDB(t, f, kind, trip))).Results[0]
			applied(t, current)
			rollback := orderOp(kind, trip, []uuid.UUID{*a.EntityID, *b.EntityID}, itemRevision(t, current))
			var before int64
			_ = f.pool.QueryRow(context.Background(), `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&before)
			f.sql(`CREATE FUNCTION public.h07_item_receipt_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type LIKE 'sync.%reorder' THEN RAISE EXCEPTION 'receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER h07_item_receipt_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION public.h07_item_receipt_fail()`)
			if x := f.push(rollback).Results[0]; x.Status != "failed" {
				t.Fatalf("receipt failure %+v", x)
			}
			f.sql(`DROP TRIGGER h07_item_receipt_fail ON mutation_receipts; DROP FUNCTION public.h07_item_receipt_fail()`)
			var after int64
			_ = f.pool.QueryRow(context.Background(), `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&after)
			if after != before || itemRevisionFromDB(t, f, kind, trip) != itemRevision(t, current) {
				t.Fatal("failed receipt left writes")
			}
			applied(t, f.push(rollback).Results[0])
		})
	}
}

// Compute the contract's revision independently of production CollectionRevision.
func itemRevisionFromDB(t *testing.T, f *pushFixture, kind string, trip uuid.UUID) string {
	t.Helper()
	table, guard := "packing_items", "packing_order"
	if kind == "todo" {
		table, guard = "todo_items", "todo_order"
	}
	var rev string
	err := f.pool.QueryRow(context.Background(), `SELECT 'sha256:'||encode(sha256(convert_to($3::text||chr(10)||$4::text||chr(10)||$2::text||chr(10)||coalesce(string_agg(id::text||':'||version::text||chr(10),'' ORDER BY id),''),'UTF8')),'hex') FROM `+table+` WHERE account_id=$1 AND trip_id=$2::uuid AND deleted_at IS NULL`, f.owner, trip.String(), f.epoch.String(), guard).Scan(&rev)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}
func TestSyncPushItemsWebProjection(t *testing.T) {
	for _, kind := range []string{"packing_item", "todo"} {
		t.Run(kind, func(t *testing.T) {
			f := newPushFixture(t)
			trip := f.newTrip("web")
			path, field, table := "packing-items", "name", "packing_items"
			if kind == "todo" {
				path, field, table = "todos", "title", "todo_items"
			}
			ids := []uuid.UUID{uuid.New(), uuid.New()}
			for i, id := range ids {
				body := map[string]any{"id": id, field: fmt.Sprintf("web%d", i)}
				if kind == "packing_item" {
					body["category"] = "documents"
				}
				x := f.do(request{method: http.MethodPost, path: "/trips/" + trip.String() + "/" + path, token: f.webToken, headers: f.authHeaders(nil), body: body})
				expectStatus(t, x, 201, "")
			}
			baseline := f.snapshot("baseline", []uuid.UUID{trip})
			f.build(baseline.ID)
			_, last := f.pages(baseline.ID)
			patch := request{method: http.MethodPatch, path: "/trips/" + trip.String() + "/" + path + "/" + ids[1].String(), token: f.webToken, headers: f.authHeaders(map[string]string{"If-Match": `"1"`}), body: map[string]any{"notes": "fast patch"}}
			expectStatus(t, f.do(patch), 200, "")
			expectStatus(t, f.do(patch), 200, "")
			delta, err := f.svc.Changes(context.Background(), f.a, "2", *last.BaselineCursor, 100)
			if err != nil || len(delta.Changes) != 1 {
				t.Fatalf("delta %+v %v", delta, err)
			}
			var data map[string]any
			if err = json.Unmarshal(delta.Changes[0].Data, &data); err != nil || data["sort_order"] != float64(1) || data["notes"] != "fast patch" {
				t.Fatalf("legacy projection %+v %v", data, err)
			}
			if kind == "packing_item" {
				body := map[string]any{"items": []any{map[string]any{"id": uuid.New(), "name": "batch1", "category": "other", "quantity": 1}, map[string]any{"id": uuid.New(), "name": "batch2", "category": "other", "quantity": 1}}}
				expectStatus(t, f.do(request{method: http.MethodPost, path: "/trips/" + trip.String() + "/packing-items/batch", token: f.webToken, headers: f.authHeaders(nil), body: body}), 201, "")
				var orders []int32
				if e := f.pool.QueryRow(context.Background(), `SELECT array_agg(sort_order ORDER BY name) FROM packing_items WHERE trip_id=$1 AND name LIKE 'batch%'`, trip).Scan(&orders); e != nil || fmt.Sprint(orders) != "[2 3]" {
					t.Fatalf("batch order %v %v", orders, e)
				}
			}
			snap := f.snapshot("trip_reload", []uuid.UUID{trip})
			f.build(snap.ID)
			items, _ := f.pages(snap.ID)
			found := false
			for _, item := range items {
				if item.EntityID == ids[1] {
					found = true
					var d map[string]any
					_ = json.Unmarshal(item.Data, &d)
					if d["sort_order"] != float64(1) {
						t.Fatal("snapshot order lost")
					}
				}
			}
			if !found {
				t.Fatal("missing snapshot item")
			}
			f.sql(`UPDATE `+table+` SET sort_order=2147483647 WHERE id=$1`, ids[1])
			overflow := itemCreate(kind, trip, "overflow")
			if x := f.push(overflow).Results[0]; x.Status == "applied" {
				t.Fatal("sort overflow accepted")
			}
			var exists bool
			_ = f.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id=$1)`, *overflow.EntityID).Scan(&exists)
			if exists {
				t.Fatal("overflow partially committed")
			}
		})
	}
}
func TestSyncPushItemsIsolationAndMerge(t *testing.T) {
	f := newPushFixture(t)
	trip := f.newTrip("merge")
	a := itemCreate("packing_item", trip, "first")
	applied(t, f.push(a).Results[0])
	id := *a.EntityID
	change := itemOp("packing_item", "update", trip, id, versionBase("1"), map[string]any{"category": "other"})
	applied(t, f.push(change).Results[0])
	staleName := itemOp("packing_item", "update", trip, id, versionBase("1"), map[string]any{"name": "second"})
	if x := f.push(staleName).Results[0]; x.Status != "conflict" {
		t.Fatalf("linked fields %+v", x)
	}
	independent := itemOp("packing_item", "update", trip, id, versionBase("1"), map[string]any{"notes": "independent"})
	applied(t, f.push(independent).Results[0])
	f.sql(`DELETE FROM sync_changes WHERE account_id=$1 AND entity_id=$2 AND entity_version=2`, f.owner, id)
	missing := itemOp("packing_item", "update", trip, id, versionBase("1"), map[string]any{"quantity": 2})
	if x := f.push(missing).Results[0]; x.Error == nil || x.Error.Code != "MERGE_HISTORY_UNAVAILABLE" {
		t.Fatalf("missing history %+v", x)
	}
	other := f.newTrip("other")
	cross := itemOp("packing_item", "update", other, id, versionBase("3"), map[string]any{"notes": "wrong trip"})
	if x := f.push(cross).Results[0]; x.Error == nil || x.Error.HTTPStatus != 404 {
		t.Fatalf("cross trip %+v", x)
	}
	foreign := f.registerWeb(uniqueEmail(), "correct horse battery")
	foreignTrip := uuid.MustParse(f.createTrip(foreign.data()["access_token"].(string), map[string]any{"name": "foreign", "start_date": "2026-10-01", "end_date": "2026-10-04"})["id"].(string))
	if x := f.push(itemCreate("todo", foreignTrip, "forbidden")).Results[0]; x.Error == nil || x.Error.HTTPStatus != 404 {
		t.Fatalf("cross account %+v", x)
	}
	for _, payload := range []map[string]any{{"status": "packed"}, {"sort_order": 1}} {
		bad := itemOp("packing_item", "update", trip, id, versionBase("3"), payload)
		if x := f.push(bad).Results[0]; x.Status != "rejected" {
			t.Fatal("update whitelist")
		}
	}
	f.sql(`UPDATE trips SET deleted_at=now(),purge_after_at=now()+interval '30 days' WHERE id=$1`, trip)
	if x := f.push(itemCreate("todo", trip, "trashed")).Results[0]; x.Error == nil || x.Error.Code != "TRIP_DELETED" {
		t.Fatalf("trashed trip %+v", x)
	}
}
func TestSyncPushItemsBoundaryFacts(t *testing.T) {
	for _, kind := range []string{"packing_item", "todo"} {
		t.Run(kind, func(t *testing.T) {
			f := newPushFixture(t)
			trip := f.newTrip("boundary")
			empty := orderOp(kind, trip, []uuid.UUID{}, itemRevisionFromDB(t, f, kind, trip))
			x := f.push(empty).Results[0]
			applied(t, x)
			if x.Result.CommitCursor != nil || len(x.Result.References) != 0 {
				t.Fatal("empty set fabricated write")
			}
			tomb := uuid.New()
			f.sql(`INSERT INTO entity_tombstones(account_id,entity_type,entity_id,trip_id,last_version,deleted_at,purged_at) VALUES($1,$2,$3,$4,9,now(),now())`, f.owner, kind, tomb, trip)
			reuse := itemCreate(kind, trip, "tomb")
			reuse.EntityID = &tomb
			if x := f.push(reuse).Results[0]; x.Error == nil || x.Error.Code != "ID_ALREADY_USED" {
				t.Fatalf("tombstone %+v", x)
			}
			a := itemCreate(kind, trip, "same")
			applied(t, f.push(a).Results[0])
			id := *a.EntityID
			name := "name"
			action, field, value := "set_status", "status", any("ready")
			if kind == "todo" {
				name = "title"
				action, field, value = "set_completed", "completed", true
			}
			unchanged := itemOp(kind, "update", trip, id, versionBase("1"), map[string]any{name: "same"})
			same := f.push(unchanged).Results[0]
			applied(t, same)
			if same.Result.CommitCursor != nil || itemVersion(t, same, id) != "1" {
				t.Fatal("patch no-op lost facts")
			}
			set := itemOp(kind, action, trip, id, refBase(unchanged.OperationID), map[string]any{field: value}, unchanged.OperationID)
			applied(t, f.push(set).Results[0])
			reset := any("pending")
			if kind == "todo" {
				reset = false
			}
			clear := itemOp(kind, action, trip, id, versionBase("2"), map[string]any{field: reset})
			cleared := f.push(clear).Results[0]
			applied(t, cleared)
			if kind == "todo" && cleared.Result.Data.(map[string]any)["completed_at"] != nil {
				t.Fatal("completion not cleared")
			}
			old := orderOp(kind, trip, []uuid.UUID{id}, itemRevision(t, cleared))
			b := itemCreate(kind, trip, "added after guard")
			applied(t, f.push(b).Results[0])
			if x := f.push(old).Results[0]; x.Error == nil || x.Error.Code != "COLLECTION_CONFLICT" {
				t.Fatal("late insert not detected")
			}
			wrong := orderOp(kind, trip, []uuid.UUID{id, *b.EntityID}, itemRevisionFromDB(t, f, kind, trip))
			wrong.Guards[0].ScopeID = uuid.NewString()
			if x := f.push(wrong).Results[0]; x.Error == nil || x.Error.Code != "INVALID_REFERENCE" {
				t.Fatal("foreign guard range accepted")
			}
			// Two-way name/category conflict grouping, checked without dropping history.
			if kind == "packing_item" {
				change := itemOp(kind, "update", trip, id, versionBase("3"), map[string]any{"name": "new name"})
				applied(t, f.push(change).Results[0])
				other := itemOp(kind, "update", trip, id, versionBase("3"), map[string]any{"category": "other"})
				if x := f.push(other).Results[0]; x.Status != "conflict" {
					t.Fatal("reverse linked fields not protected")
				}
			}
		})
	}
}
