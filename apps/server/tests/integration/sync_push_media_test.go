package integration

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	syncmodule "tripfolio/server/internal/modules/sync"
)

func mediaRegistration(trip uuid.UUID) syncmodule.Operation {
	return itemOp("asset", "register", trip, uuid.New(), nil, map[string]any{"scope": "trip", "trip_id": trip, "original_name": "pending.png", "expected_size": 1024, "declared_media_type": "image/png"})
}
func mediaPhoto(trip, asset uuid.UUID, day string, order int, deps ...uuid.UUID) syncmodule.Operation {
	return itemOp("photo", "create", trip, uuid.New(), nil, map[string]any{"asset_id": asset, "recorded_on": day, "sort_order": order}, deps...)
}
func mediaDayRevision(t *testing.T, f *pushFixture, trip uuid.UUID, day string) string {
	t.Helper()
	var rev string
	err := f.pool.QueryRow(context.Background(), `SELECT 'sha256:'||encode(sha256(convert_to($3::text||chr(10)||'photo_day'||chr(10)||$2::text||'/'||$4::text||chr(10)||coalesce(string_agg(id::text||':'||version::text||chr(10),'' ORDER BY id),''),'UTF8')),'hex') FROM photos WHERE account_id=$1 AND trip_id=$2::uuid AND recorded_on=$4::date AND deleted_at IS NULL`, f.owner, trip.String(), f.epoch.String(), day).Scan(&rev)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}
func mediaGuard(trip uuid.UUID, day, rev string) syncmodule.GuardReference {
	return syncmodule.GuardReference{Kind: "photo_day", ScopeID: trip.String() + "/" + day, Revision: &rev}
}
func mediaApplied(t *testing.T, r syncmodule.PushResult) {
	t.Helper()
	if r.Status != "applied" {
		t.Fatalf("media status=%s error=%+v", r.Status, r.Error)
	}
}
func mediaError(t *testing.T, r syncmodule.PushResult, code string) {
	t.Helper()
	if r.Error == nil || r.Error.Code != code {
		t.Fatalf("want %s status=%s error=%+v", code, r.Status, r.Error)
	}
}

func TestSyncPushMediaLifecycleAndImmutableFacts(t *testing.T) {
	f := newPushFixture(t)
	trip := f.newTrip("media")
	reg := mediaRegistration(trip)
	photo := mediaPhoto(trip, *reg.EntityID, "2026-10-05", 0, reg.OperationID)
	doc := documentOp(trip, *reg.EntityID, uuid.Nil, reg.OperationID)
	for _, r := range f.push(reg, photo, doc).Results {
		mediaApplied(t, r)
	}
	var status string
	if err := f.pool.QueryRow(context.Background(), `SELECT status FROM assets WHERE id=$1`, *reg.EntityID).Scan(&status); err != nil || status != "uploading" {
		t.Fatalf("registration status %s %v", status, err)
	}
	second := mediaPhoto(trip, *reg.EntityID, "2026-10-05", 1)
	mediaApplied(t, f.push(second).Results[0])
	noop := itemOp("photo", "update", trip, *photo.EntityID, versionBase("1"), map[string]any{"caption": ""})
	n := f.push(noop).Results[0]
	mediaApplied(t, n)
	if n.Result.CommitCursor != nil || itemVersion(t, n, *photo.EntityID) != "1" {
		t.Fatal("no-op incremented")
	}
	originalRevision := itemRevision(t, n)
	move := itemOp("photo", "update", trip, *photo.EntityID, refBase(noop.OperationID), map[string]any{"taken_at_local": "2026-10-06T08:00:00"}, noop.OperationID)
	mediaError(t, f.push(move).Results[0], "COLLECTION_BASE_REQUIRED")
	move.OperationID = uuid.New()
	move.Guards = []syncmodule.GuardReference{{Kind: "photo_day", ScopeID: trip.String() + "/2026-10-05", OperationID: &noop.OperationID}, mediaGuard(trip, "2026-10-06", mediaDayRevision(t, f, trip, "2026-10-06"))}
	r := f.push(move).Results[0]
	mediaApplied(t, r)
	if len(r.Result.ScopeRevisions) != 2 || r.Result.Data.(map[string]any)["recorded_on"] != "2026-10-06" {
		t.Fatal("implicit date guards/revisions missing")
	}
	replay := f.push(noop).Results[0]
	if replay.Status != "replayed" || itemVersion(t, replay, *photo.EntityID) != "1" || replay.Result.Data.(map[string]any)["version"] != "2" || itemRevision(t, replay) != originalRevision {
		t.Fatal("original no-op facts drifted")
	}
	order := itemOp("photo", "reorder", trip, uuid.Nil, nil, map[string]any{"recorded_on": "2026-10-05", "ordered_ids": []uuid.UUID{*second.EntityID}})
	order.Guards = []syncmodule.GuardReference{mediaGuard(trip, "2026-10-05", mediaDayRevision(t, f, trip, "2026-10-05"))}
	mediaApplied(t, f.push(order).Results[0])
	del := itemOp("photo", "delete", trip, *photo.EntityID, versionBase("2"), map[string]any{})
	deleted := f.push(del).Results[0]
	mediaApplied(t, deleted)
	if itemVersion(t, deleted, *photo.EntityID) != "3" || len(deleted.Result.ScopeRevisions) != 1 {
		t.Fatal("delete facts")
	}
	raw, _ := json.Marshal(f.push(reg).Results[0])
	if strings.Contains(string(raw), "https://") || strings.Contains(string(raw), "object_key") || strings.Contains(string(raw), "upload_authorization") {
		t.Fatal("private storage details in receipt")
	}
}

func TestSyncPushMediaCompleteOrderAndConcurrency(t *testing.T) {
	f := newPushFixture(t)
	trip := f.newTrip("order")
	reg := mediaRegistration(trip)
	mediaApplied(t, f.push(reg).Results[0])
	a := mediaPhoto(trip, *reg.EntityID, "2026-10-05", 0)
	b := mediaPhoto(trip, *reg.EntityID, "2026-10-05", 1)
	for _, r := range f.push(a, b).Results {
		mediaApplied(t, r)
	}
	order := func(ids []uuid.UUID) syncmodule.Operation {
		op := itemOp("photo", "reorder", trip, uuid.Nil, nil, map[string]any{"recorded_on": "2026-10-05", "ordered_ids": ids})
		op.Guards = []syncmodule.GuardReference{mediaGuard(trip, "2026-10-05", mediaDayRevision(t, f, trip, "2026-10-05"))}
		return op
	}
	bad := order([]uuid.UUID{*a.EntityID})
	mediaError(t, f.push(bad).Results[0], "VALIDATION_FAILED")
	x := order([]uuid.UUID{*b.EntityID, *a.EntityID})
	y := x
	y.OperationID = uuid.New()
	var wg sync.WaitGroup
	results := make(chan syncmodule.PushResult, 2)
	errs := make(chan error, 2)
	for _, op := range []syncmodule.Operation{x, y} {
		wg.Add(1)
		go func(op syncmodule.Operation) {
			defer wg.Done()
			out, err := f.svc.Push(context.Background(), f.a, "2", f.input(op))
			if err != nil {
				errs <- err
				return
			}
			results <- out.Results[0]
		}(op)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for r := range results {
		counts[r.Status]++
	}
	if counts["applied"] != 1 || counts["conflict"] != 1 {
		t.Fatalf("concurrent order %v", counts)
	}
}

func TestSyncPushMediaReceiptRollback(t *testing.T) {
	f := newPushFixture(t)
	trip := f.newTrip("rollback")
	reg := mediaRegistration(trip)
	mediaApplied(t, f.push(reg).Results[0])
	a := mediaPhoto(trip, *reg.EntityID, "2026-10-05", 0)
	b := mediaPhoto(trip, *reg.EntityID, "2026-10-05", 1)
	for _, r := range f.push(a, b).Results {
		mediaApplied(t, r)
	}
	op := itemOp("photo", "reorder", trip, uuid.Nil, nil, map[string]any{"recorded_on": "2026-10-05", "ordered_ids": []uuid.UUID{*b.EntityID, *a.EntityID}})
	op.Guards = []syncmodule.GuardReference{mediaGuard(trip, "2026-10-05", mediaDayRevision(t, f, trip, "2026-10-05"))}
	var seq int64
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	f.sql(`CREATE FUNCTION public.h07_media_receipt_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type IN ('sync.photo.reorder','sync.asset.register') THEN RAISE EXCEPTION 'receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER h07_media_receipt_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION public.h07_media_receipt_fail()`)
	t.Cleanup(func() {
		f.sql(`DROP TRIGGER IF EXISTS h07_media_receipt_fail ON mutation_receipts; DROP FUNCTION IF EXISTS public.h07_media_receipt_fail()`)
	})
	if r := f.push(op).Results[0]; r.Status != "failed" {
		t.Fatalf("injected failure %+v", r)
	}
	reg2 := mediaRegistration(trip)
	if r := f.push(reg2).Results[0]; r.Status != "failed" {
		t.Fatalf("registration failure %+v", r)
	}
	var intact bool
	err := f.pool.QueryRow(context.Background(), `SELECT (SELECT last_seq=$2 FROM account_sync_state WHERE account_id=$1) AND (SELECT count(*)=2 FROM photos WHERE trip_id=$3 AND version=1) AND NOT EXISTS(SELECT 1 FROM assets WHERE id=$4) AND NOT EXISTS(SELECT 1 FROM mutation_receipts WHERE operation_id=$5 OR operation_id=$6)`, f.owner, seq, trip, *reg2.EntityID, op.OperationID, reg2.OperationID).Scan(&intact)
	if err != nil || !intact {
		t.Fatalf("partial write %v", err)
	}
}

func TestSyncPushMediaReferencesConflictsAndTombstones(t *testing.T) {
	f := newPushFixture(t)
	trip := f.newTrip("refs")
	other := f.newTrip("other")
	reg := mediaRegistration(trip)
	mediaApplied(t, f.push(reg).Results[0])
	wrong := mediaPhoto(other, *reg.EntityID, "2026-10-05", 0)
	mediaError(t, f.push(wrong).Results[0], "RESOURCE_NOT_FOUND")
	pdf := mediaRegistration(trip)
	pdf.Payload, _ = json.Marshal(map[string]any{"scope": "trip", "trip_id": trip, "original_name": "a.pdf", "expected_size": 100, "declared_media_type": "application/pdf"})
	mediaApplied(t, f.push(pdf).Results[0])
	mediaError(t, f.push(mediaPhoto(trip, *pdf.EntityID, "2026-10-05", 0)).Results[0], "VALIDATION_FAILED")
	photo := mediaPhoto(trip, *reg.EntityID, "2026-10-05", 0)
	mediaApplied(t, f.push(photo).Results[0])
	place := itemOp("photo", "update", trip, *photo.EntityID, versionBase("1"), map[string]any{"place_name": "new"})
	mediaApplied(t, f.push(place).Results[0])
	stale := itemOp("photo", "update", trip, *photo.EntityID, versionBase("1"), map[string]any{"address": "old-base"})
	mediaError(t, f.push(stale).Results[0], "VERSION_CONFLICT")
	caption := itemOp("photo", "update", trip, *photo.EntityID, versionBase("1"), map[string]any{"caption": "independent"})
	mediaApplied(t, f.push(caption).Results[0])
	f.sql(`DELETE FROM sync_changes WHERE account_id=$1 AND entity_id=$2 AND entity_version=2`, f.owner, *photo.EntityID)
	history := itemOp("photo", "update", trip, *photo.EntityID, versionBase("1"), map[string]any{"caption": "independent"})
	mediaError(t, f.push(history).Results[0], "MERGE_HISTORY_UNAVAILABLE")
	del := itemOp("photo", "delete", trip, *photo.EntityID, versionBase("3"), map[string]any{})
	mediaApplied(t, f.push(del).Results[0])
	f.sql(`DELETE FROM photos WHERE id=$1`, *photo.EntityID)
	f.sql(`INSERT INTO entity_tombstones(account_id,entity_type,entity_id,trip_id,last_version,deleted_at,purged_at) VALUES($1,'photo',$2,$3,4,now(),now())`, f.owner, *photo.EntityID, trip)
	if r := f.push(photo).Results[0]; r.Status != "replayed" || r.Result.Data != nil {
		t.Fatal("purged replay")
	}
	reuse := photo
	reuse.OperationID = uuid.New()
	mediaError(t, f.push(reuse).Results[0], "ID_ALREADY_USED")
}
