package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/transport/httpapi"
)

func newContentFixture(t *testing.T) *pushFixture {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	s := f.services
	server := httptest.NewServer(httpapi.NewRouter(httpapi.Deps{Logger: quietLogger(), CORSOrigins: []string{f.origin}, Identity: s.Identity, Sessions: s.Sessions, Profile: s.Profile, Trips: s.Trips, Reservations: s.Reservations, Documents: s.Documents, Sync: f.svc}))
	t.Cleanup(server.Close)
	f.server = server
	return f
}
func contentAsset(f *pushFixture, trip uuid.UUID, media string) uuid.UUID {
	id := uuid.New()
	f.sql(`INSERT INTO assets(id,account_id,trip_id,scope,original_name,status,declared_media_type,staging_object_key,expected_size,upload_expires_at) VALUES($1,$2,$3,'trip','pending file','uploading',$4,'staging/'||$1::uuid::text,1024,now()+interval '1 hour')`, id, f.owner, trip, media)
	return id
}
func reservationOp(trip uuid.UUID, title string) syncmodule.Operation {
	return itemOp("reservation", "create", trip, uuid.New(), nil, map[string]any{"kind": "transport", "title": title, "start_local": "2026-10-01T08:00:00", "end_local": "2026-10-01T12:00:00", "origin": "A", "destination": "B"})
}
func documentOp(trip, asset, reservation uuid.UUID, deps ...uuid.UUID) syncmodule.Operation {
	p := map[string]any{"title": "ticket", "asset_id": asset}
	if reservation != uuid.Nil {
		p["reservation_id"] = reservation
	}
	return itemOp("document", "create", trip, uuid.New(), nil, p, deps...)
}
func TestSyncPushContentLifecycle(t *testing.T) {
	f := newContentFixture(t)
	trip := f.newTrip("content")
	asset := contentAsset(f, trip, "application/pdf")
	res := reservationOp(trip, "rail")
	doc := documentOp(trip, asset, *res.EntityID, res.OperationID)
	out := f.push(res, doc)
	for _, x := range out.Results {
		applied(t, x)
	}
	var status string
	_ = f.pool.QueryRow(context.Background(), `SELECT status FROM assets WHERE id=$1`, asset).Scan(&status)
	if status != "uploading" {
		t.Fatal("document pretended upload ready")
	}
	noop := itemOp("document", "update", trip, *doc.EntityID, refBase(doc.OperationID), map[string]any{"title": "ticket"}, doc.OperationID)
	n := f.push(noop).Results[0]
	applied(t, n)
	if n.Result.CommitCursor != nil || itemVersion(t, n, *doc.EntityID) != "1" {
		t.Fatal("document no-op")
	}
	rnoop := itemOp("reservation", "update", trip, *res.EntityID, versionBase("1"), map[string]any{"title": "rail"})
	r := f.push(rnoop).Results[0]
	applied(t, r)
	if r.Result.CommitCursor != nil || itemVersion(t, r, *res.EntityID) != "1" {
		t.Fatal("reservation no-op")
	}
	update := itemOp("reservation", "update", trip, *res.EntityID, versionBase("1"), map[string]any{"kind": "lodging", "transport_number": nil, "origin": nil, "destination": nil})
	applied(t, f.push(update).Results[0])
	baseline := f.snapshot("baseline", []uuid.UUID{trip})
	f.build(baseline.ID)
	_, last := f.pages(baseline.ID)
	del := itemOp("reservation", "delete", trip, *res.EntityID, versionBase("2"), map[string]any{})
	after := itemOp("document", "update", trip, *doc.EntityID, refBase(del.OperationID), map[string]any{"notes": "after unlink"}, del.OperationID)
	chain := f.push(del, after)
	for _, x := range chain.Results {
		applied(t, x)
	}
	if itemVersion(t, chain.Results[0], *doc.EntityID) != "2" || itemVersion(t, chain.Results[1], *doc.EntityID) != "3" {
		t.Fatal("unlink original reference absent")
	}
	delta, err := f.svc.Changes(context.Background(), f.a, "2", *last.BaselineCursor, 1)
	if err != nil || len(delta.Changes) != 1 || delta.Changes[0].BatchEndSeq == delta.Changes[0].Seq {
		t.Fatalf("unlink batch first page %+v %v", delta, err)
	}
	page, err := f.svc.Changes(context.Background(), f.a, "2", delta.NextCursor, 100)
	if err != nil || len(page.Changes) != 2 || page.Changes[0].BatchID != delta.Changes[0].BatchID {
		t.Fatalf("unlink batch pages %+v %v", page, err)
	}
	replay := f.push(noop).Results[0]
	if replay.Status != "replayed" || itemVersion(t, replay, *doc.EntityID) != "1" || replay.Result.Data.(map[string]any)["version"] != "3" {
		t.Fatal("no-op original facts mutated")
	}
	gone := itemOp("document", "delete", trip, *doc.EntityID, versionBase("3"), map[string]any{})
	applied(t, f.push(gone).Results[0])
	f.sql(`DELETE FROM documents WHERE id=$1`, *doc.EntityID)
	f.sql(`INSERT INTO entity_tombstones(account_id,entity_type,entity_id,trip_id,last_version,deleted_at,purged_at) VALUES($1,'document',$2,$3,4,now(),now())`, f.owner, *doc.EntityID, trip)
	if x := f.push(doc).Results[0]; x.Status != "replayed" || x.Result.Data != nil || itemVersion(t, x, *doc.EntityID) != "1" {
		t.Fatalf("purged replay %+v", x)
	}
	reuse := documentOp(trip, asset, uuid.Nil)
	reuse.EntityID = doc.EntityID
	if x := f.push(reuse).Results[0]; x.Error == nil || x.Error.Code != "ID_ALREADY_USED" {
		t.Fatal("tombstone reused")
	}
}
func TestSyncPushContentReceiptRollback(t *testing.T) {
	f := newContentFixture(t)
	trip := f.newTrip("rollback")
	asset := contentAsset(f, trip, "image/png")
	res := reservationOp(trip, "rail")
	doc := documentOp(trip, asset, *res.EntityID, res.OperationID)
	doc2 := documentOp(trip, asset, *res.EntityID, res.OperationID)
	for _, x := range f.push(res, doc, doc2).Results {
		applied(t, x)
	}
	del := itemOp("reservation", "delete", trip, *res.EntityID, versionBase("1"), map[string]any{})
	var before int64
	_ = f.pool.QueryRow(context.Background(), `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&before)
	f.sql(`CREATE FUNCTION public.h07_content_receipt_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type='sync.reservation.delete' THEN RAISE EXCEPTION 'receipt failure'; END IF; RETURN NEW; END $$;CREATE TRIGGER h07_content_receipt_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION public.h07_content_receipt_fail()`)
	if x := f.push(del).Results[0]; x.Status != "failed" {
		t.Fatalf("fault %+v", x)
	}
	var unchanged bool
	err := f.pool.QueryRow(context.Background(), `SELECT (SELECT last_seq=$2 FROM account_sync_state WHERE account_id=$1) AND (SELECT version=1 AND deleted_at IS NULL FROM reservations WHERE id=$3) AND (SELECT count(*)=2 FROM documents WHERE reservation_id=$3 AND version=1) AND NOT EXISTS(SELECT 1 FROM mutation_receipts WHERE account_id=$1 AND operation_id=$4)`, f.owner, before, *res.EntityID, del.OperationID).Scan(&unchanged)
	if err != nil || !unchanged {
		t.Fatalf("partial commit: %v %t", err, unchanged)
	}
	f.sql(`DROP TRIGGER h07_content_receipt_fail ON mutation_receipts;DROP FUNCTION public.h07_content_receipt_fail()`)
	// Concurrent exact retries share one durable receipt and one unlink batch.
	var wg sync.WaitGroup
	results := make(chan syncmodule.PushResult, 5)
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, e := f.svc.Push(context.Background(), f.a, "2", f.input(del))
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
	for x := range results {
		counts[x.Status]++
		if itemVersion(t, x, *doc.EntityID) != "2" || itemVersion(t, x, *doc2.EntityID) != "2" {
			t.Fatal("unlink refs not immutable")
		}
	}
	if counts["applied"] != 1 || counts["replayed"] != 4 {
		t.Fatalf("retries %v", counts)
	}
	var count int
	_ = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM sync_changes WHERE account_id=$1 AND batch_id=$2`, f.owner, del.OperationID).Scan(&count)
	if count != 3 {
		t.Fatalf("unlink log count %d", count)
	}
}
func TestSyncPushContentConflictsAndReferences(t *testing.T) {
	f := newContentFixture(t)
	trip := f.newTrip("conflicts")
	asset := contentAsset(f, trip, "application/pdf")
	res := reservationOp(trip, "rail")
	applied(t, f.push(res).Results[0])
	id := *res.EntityID
	up := itemOp("reservation", "update", trip, id, versionBase("1"), map[string]any{"start_local": "2026-10-01T09:00:00"})
	applied(t, f.push(up).Results[0])
	stale := itemOp("reservation", "update", trip, id, versionBase("1"), map[string]any{"end_local": "2026-10-01T13:00:00"})
	if x := f.push(stale).Results[0]; x.Status != "conflict" {
		t.Fatal("time fields not linked")
	}
	notes := itemOp("reservation", "update", trip, id, versionBase("1"), map[string]any{"notes": "independent"})
	applied(t, f.push(notes).Results[0])
	badType := itemOp("reservation", "update", trip, id, versionBase("3"), map[string]any{"kind": "lodging"})
	if x := f.push(badType).Results[0]; x.Status != "rejected" {
		t.Fatal("implicit type clear")
	}
	move := itemOp("reservation", "update", trip, id, versionBase("3"), map[string]any{"origin": "new"})
	applied(t, f.push(move).Results[0])
	old := itemOp("reservation", "update", trip, id, versionBase("3"), map[string]any{"kind": "lodging", "origin": nil, "destination": nil, "transport_number": nil})
	if x := f.push(old).Results[0]; x.Status != "conflict" {
		t.Fatal("type group not linked")
	}
	f.sql(`DELETE FROM sync_changes WHERE account_id=$1 AND entity_id=$2 AND entity_version=2`, f.owner, id)
	history := itemOp("reservation", "update", trip, id, versionBase("1"), map[string]any{"title": "rail"})
	if x := f.push(history).Results[0]; x.Error == nil || x.Error.Code != "MERGE_HISTORY_UNAVAILABLE" {
		t.Fatal("no-op bypassed history")
	}
	doc := documentOp(trip, asset, id)
	applied(t, f.push(doc).Results[0])
	otherTrip := f.newTrip("other")
	badAsset := contentAsset(f, otherTrip, "application/pdf")
	bad := documentOp(trip, badAsset, id)
	if x := f.push(bad).Results[0]; x.Error == nil || x.Error.HTTPStatus != 404 {
		t.Fatal("cross-trip asset")
	}
	invalid := contentAsset(f, trip, "text/plain")
	if x := f.push(documentOp(trip, invalid, id)).Results[0]; x.Status != "rejected" {
		t.Fatal("unsupported asset")
	}
	otherRes := reservationOp(otherTrip, "other")
	applied(t, f.push(otherRes).Results[0])
	if x := f.push(documentOp(trip, asset, *otherRes.EntityID)).Results[0]; x.Error == nil || x.Error.HTTPStatus != 404 {
		t.Fatal("cross-trip reservation")
	}
	clear := itemOp("document", "update", trip, *doc.EntityID, versionBase("1"), map[string]any{"reservation_id": nil})
	applied(t, f.push(clear).Results[0])
	x := f.push(itemOp("document", "update", trip, *doc.EntityID, versionBase("2"), map[string]any{"notes": "keep null"})).Results[0]
	applied(t, x)
	if x.Result.Data.(map[string]any)["reservation_id"] != nil {
		t.Fatal("missing vs null mismatch")
	}
	f.sql(`UPDATE assets SET deleted_at=now() WHERE id=$1`, asset)
	noop := itemOp("document", "update", trip, *doc.EntityID, versionBase("3"), map[string]any{"title": "ticket"})
	if x := f.push(noop).Results[0]; x.Status != "rejected" {
		t.Fatal("no-op bypassed final reference validation")
	}
	f.sql(`UPDATE trips SET deleted_at=now(),purge_after_at=now()+interval '30 days' WHERE id=$1`, trip)
	if x := f.push(reservationOp(trip, "trash")).Results[0]; x.Error == nil || x.Error.Code != "TRIP_DELETED" {
		t.Fatal("trashed trip")
	}
}
func TestSyncPushContentLegacyAndIsolation(t *testing.T) {
	f := newContentFixture(t)
	trip := f.newTrip("legacy")
	asset := contentAsset(f, trip, "image/png")
	id := uuid.New()
	path := "/trips/" + trip.String() + "/reservations"
	body := map[string]any{"id": id, "kind": "transport", "title": "legacy", "origin": "A", "destination": "B"}
	headers := f.authHeaders(nil)
	expectStatus(t, f.do(request{method: http.MethodPost, path: path, token: f.webToken, headers: headers, body: body}), 201, "")
	expectStatus(t, f.do(request{method: http.MethodPost, path: path, token: f.webToken, headers: headers, body: body}), 201, "")
	doc := documentOp(trip, asset, id)
	applied(t, f.push(doc).Results[0])
	snap := f.snapshot("baseline", []uuid.UUID{trip})
	f.build(snap.ID)
	items, last := f.pages(snap.ID)
	found := 0
	for _, item := range items {
		if item.EntityType == "document" || item.EntityType == "reservation" {
			found++
		}
	}
	if found != 2 {
		t.Fatal("snapshot content")
	}
	expectStatus(t, f.do(request{method: http.MethodPatch, path: path + "/" + id.String(), token: f.webToken, headers: f.authHeaders(map[string]string{"If-Match": `"1"`}), body: map[string]any{"notes": "web edit"}}), 200, "")
	delta, err := f.svc.Changes(context.Background(), f.a, "2", *last.BaselineCursor, 100)
	if err != nil || len(delta.Changes) != 1 {
		t.Fatalf("web delta %+v %v", delta, err)
	}
	var data map[string]any
	_ = json.Unmarshal(delta.Changes[0].Data, &data)
	if data["notes"] != "web edit" {
		t.Fatal("legacy log projection")
	}
	foreign := f.registerWeb(uniqueEmail(), "correct horse battery")
	foreignTrip := uuid.MustParse(f.createTrip(foreign.data()["access_token"].(string), map[string]any{"name": "foreign", "start_date": "2026-10-01", "end_date": "2026-10-04"})["id"].(string))
	if x := f.push(reservationOp(foreignTrip, "denied")).Results[0]; x.Error == nil || x.Error.HTTPStatus != 404 {
		t.Fatal("cross-account create")
	}
	foreignOwner := uuid.MustParse(foreign.data()["account"].(map[string]any)["id"].(string))
	foreignAsset := uuid.New()
	f.sql(`INSERT INTO assets(id,account_id,trip_id,scope,original_name,status,declared_media_type,staging_object_key,expected_size,upload_expires_at) VALUES($1,$2,$3,'trip','foreign','uploading','image/png','staging/'||$1::uuid::text,1024,now()+interval '1 hour')`, foreignAsset, foreignOwner, foreignTrip)
	if x := f.push(documentOp(trip, foreignAsset, id)).Results[0]; x.Error == nil || x.Error.HTTPStatus != 404 {
		t.Fatal("cross-account reference")
	}
	guarded := itemOp("reservation", "update", trip, id, versionBase("2"), map[string]any{"notes": "guard"})
	rev := "sha256:" + strings.Repeat("0", 64)
	guarded.Guards = []syncmodule.GuardReference{{Kind: "members", ScopeID: trip.String(), Revision: &rev}}
	if x := f.push(guarded).Results[0]; x.Error == nil || x.Error.Code != "INVALID_REFERENCE" {
		t.Fatal("unrelated guard accepted")
	}
	failed := reservationOp(trip, "")
	child := documentOp(trip, asset, *failed.EntityID, failed.OperationID)
	valid := reservationOp(trip, "independent")
	out := f.push(failed, child, valid)
	if out.Results[0].Status != "rejected" || out.Results[1].Error.Code != "DEPENDENCY_REJECTED" || out.Results[2].Status != "applied" {
		t.Fatalf("failure isolation %s", fmt.Sprint(out))
	}
}
