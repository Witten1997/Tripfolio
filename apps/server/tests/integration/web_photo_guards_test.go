package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/write"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/modules/travel/album"
	"tripfolio/server/internal/transport/httpapi"
)

func photoWebFixture(t *testing.T, protected bool) (*pushFixture, uuid.UUID, uuid.UUID) {
	t.Helper()
	f := newPushFixture(t)
	s := f.services
	server := httptest.NewServer(httpapi.NewRouter(httpapi.Deps{Logger: quietLogger(), CORSOrigins: []string{f.origin}, Identity: s.Identity, Sessions: s.Sessions, Profile: s.Profile, Trips: s.Trips, Photos: s.Photos, Sync: f.svc}))
	t.Cleanup(server.Close)
	f.server = server
	trip := f.newTrip("photo guards")
	asset := contentAsset(f, trip, "image/png")
	if protected {
		f.sql(`INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, f.owner)
	}
	return f, trip, asset
}

func photoWebGuards(t *testing.T, f *pushFixture, trip uuid.UUID, days ...string) []collectionguard.Guard {
	t.Helper()
	guards := make([]collectionguard.Guard, 0, len(days))
	for _, day := range days {
		guards = append(guards, collectionguard.Guard{Kind: "photo_day", ScopeID: trip.String() + "/" + day, Revision: mediaDayRevision(t, f, trip, day)})
	}
	return guards
}

func photoWebRequest(f *pushFixture, trip, id, key uuid.UUID, method, version string, body any, guards []collectionguard.Guard) apiResponse {
	headers := f.authHeaders(map[string]string{"Idempotency-Key": key.String()})
	path := "/trips/" + trip.String() + "/photos"
	if id != uuid.Nil {
		path += "/" + id.String()
	}
	if version != "" {
		headers["If-Match"] = `"` + version + `"`
	}
	if guards != nil {
		raw, _ := json.Marshal(guards)
		headers["X-Collection-Guards"] = string(raw)
	}
	return f.do(request{method: method, path: path, token: f.webToken, headers: headers, body: body})
}

func photoWebResult(t *testing.T, res apiResponse, status int) write.Result {
	t.Helper()
	expectStatus(t, res, status, "")
	var body struct {
		Data write.Result `json:"data"`
	}
	if err := json.Unmarshal(res.Raw, &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func photoWebCreate(t *testing.T, f *pushFixture, trip, asset uuid.UUID, day string, order *int32) (uuid.UUID, write.Result) {
	t.Helper()
	id := uuid.New()
	body := map[string]any{"id": id, "asset_id": asset, "recorded_on": day}
	if order != nil {
		body["sort_order"] = *order
	}
	return id, photoWebResult(t, photoWebRequest(f, trip, uuid.Nil, uuid.New(), http.MethodPost, "", body, nil), 201)
}

func photoWebFacts(t *testing.T, f *pushFixture, result write.Result, trip uuid.UUID, days ...string) {
	t.Helper()
	if len(result.ScopeRevisions) != len(days) {
		t.Fatalf("scope facts %+v want %v", result.ScopeRevisions, days)
	}
	for _, day := range days {
		want := write.ScopeRevision{Kind: "photo_day", ScopeID: trip.String() + "/" + day, Revision: mediaDayRevision(t, f, trip, day)}
		found := false
		for _, fact := range result.ScopeRevisions {
			found = found || fact == want
		}
		if !found {
			t.Fatalf("missing final fact %+v in %+v", want, result.ScopeRevisions)
		}
	}
}

func TestWebPhotoGuardsFinalDatesAndNoop(t *testing.T) {
	f, trip, asset := photoWebFixture(t, false)
	id, _ := photoWebCreate(t, f, trip, asset, "2026-10-05", nil)
	photoWebResult(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "1", map[string]any{"sort_order": 4}, nil), 200)
	f.sql(`INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, f.owner)
	id, _ = photoWebCreate(t, f, trip, asset, "2026-10-05", nil)
	for _, patch := range []map[string]any{{"recorded_on": "2026-10-05"}, {"taken_at_local": nil}, {"sort_order": 5}} {
		expectStatus(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "1", patch, nil), 428, "COLLECTION_BASE_REQUIRED")
		noop := photoWebResult(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "1", patch, photoWebGuards(t, f, trip, "2026-10-05")), 200)
		if noop.Primary == nil || *noop.Primary.Version != 1 || noop.CommitCursor != nil {
			t.Fatalf("same value changed %+v", noop)
		}
		photoWebFacts(t, f, noop, trip, "2026-10-05")
	}
	patch := map[string]any{"taken_at_local": "2026-10-06T08:00:00"}
	guards := photoWebGuards(t, f, trip, "2026-10-05", "2026-10-06")
	expectStatus(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "1", patch, guards[:1]), 428, "COLLECTION_BASE_REQUIRED")
	bad := append([]collectionguard.Guard(nil), guards...)
	bad[1].Revision = "sha256:" + strings.Repeat("0", 64)
	expectStatus(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "1", patch, bad), 412, "COLLECTION_CONFLICT")
	bad[1] = collectionguard.Guard{Kind: "photo_day", ScopeID: uuid.NewString() + "/2026-10-06", Revision: guards[1].Revision}
	expectStatus(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "1", patch, bad), 422, "INVALID_REFERENCE")
	expectStatus(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "1", map[string]any{"caption": "new"}, guards[:1]), 422, "INVALID_REFERENCE")
	caption := photoWebResult(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "1", map[string]any{"caption": "new"}, nil), 200)
	photoWebFacts(t, f, caption, trip, "2026-10-05")
	moved := photoWebResult(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "2", patch, photoWebGuards(t, f, trip, "2026-10-05", "2026-10-06")), 200)
	photoWebFacts(t, f, moved, trip, "2026-10-05", "2026-10-06")
	if moved.Data.(map[string]any)["recorded_on"] != "2026-10-06" {
		t.Fatal("implicit date not applied")
	}
	explicit := photoWebResult(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "3", map[string]any{"taken_at_local": "2026-10-08T08:00:00", "recorded_on": "2026-10-06"}, photoWebGuards(t, f, trip, "2026-10-06")), 200)
	photoWebFacts(t, f, explicit, trip, "2026-10-06")
	cleared := photoWebResult(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "4", map[string]any{"taken_at_local": nil}, photoWebGuards(t, f, trip, "2026-10-06")), 200)
	if cleared.Data.(map[string]any)["recorded_on"] != "2026-10-06" {
		t.Fatal("clearing time changed date")
	}
	// Calling the shared service with a native actor does not itself establish proof.
	zero := int32(0)
	_, err := f.services.Photos.Update(actor.WithActor(context.Background(), f.a), f.a, uuid.New(), trip, id, 5, album.Patch{SortOrder: &zero})
	policyError(t, err, 428, "COLLECTION_BASE_REQUIRED")
}

func TestWebPhotoGuardsNativeProofAndOriginalFacts(t *testing.T) {
	f, trip, asset := photoWebFixture(t, true)
	create := mediaPhoto(trip, asset, "2026-10-05", 0)
	mediaApplied(t, f.push(create).Results[0])
	id := *create.EntityID
	caption := itemOp("photo", "update", trip, id, versionBase("1"), map[string]any{"caption": "native"})
	caption.Guards = []syncmodule.GuardReference{mediaGuard(trip, "2026-10-05", mediaDayRevision(t, f, trip, "2026-10-05"))}
	mediaApplied(t, f.push(caption).Results[0])
	move := itemOp("photo", "update", trip, id, refBase(caption.OperationID), map[string]any{"taken_at_local": "2026-10-06T08:00:00"}, caption.OperationID)
	move.Guards = []syncmodule.GuardReference{{Kind: "photo_day", ScopeID: trip.String() + "/2026-10-05", OperationID: &caption.OperationID}, mediaGuard(trip, "2026-10-06", mediaDayRevision(t, f, trip, "2026-10-06"))}
	first := f.push(move).Results[0]
	mediaApplied(t, first)
	if len(first.Result.ScopeRevisions) != 2 {
		t.Fatalf("native facts %+v", first)
	}
	var stored write.Result
	var raw []byte
	if err := f.pool.QueryRow(context.Background(), `SELECT result FROM mutation_receipts WHERE account_id=$1 AND operation_id=$2`, f.owner, move.OperationID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Sync == nil || !reflect.DeepEqual(stored.ScopeRevisions, stored.Sync.ScopeRevisions) || !reflect.DeepEqual(stored.ScopeRevisions, first.Result.ScopeRevisions) {
		t.Fatalf("divergent REST/native facts %+v", stored)
	}
	photoWebResult(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "3", map[string]any{"caption": "after"}, nil), 200)
	replay := f.push(move).Results[0]
	if replay.Status != "replayed" || !reflect.DeepEqual(replay.Result.ScopeRevisions, first.Result.ScopeRevisions) || replay.Result.Data.(map[string]any)["version"] != "4" {
		t.Fatalf("native replay drift %+v", replay)
	}
	stale := itemOp("photo", "update", trip, id, versionBase("4"), map[string]any{"recorded_on": "2026-10-05"})
	stale.Guards = move.Guards
	stale.DependsOn = []uuid.UUID{caption.OperationID}
	mediaError(t, f.push(stale).Results[0], "COLLECTION_CONFLICT")
	order := itemOp("photo", "reorder", trip, uuid.Nil, nil, map[string]any{"recorded_on": "2026-10-06", "ordered_ids": []uuid.UUID{id}})
	order.Guards = []syncmodule.GuardReference{mediaGuard(trip, "2026-10-06", mediaDayRevision(t, f, trip, "2026-10-06"))}
	mediaApplied(t, f.push(order).Results[0])
	forbidden := itemOp("photo", "update", trip, id, versionBase("4"), map[string]any{"sort_order": 0})
	if r := f.push(forbidden).Results[0]; r.Error == nil {
		t.Fatal("native PATCH accepted sorting")
	}
}

func TestWebPhotoGuardsCreateDeleteAndReplay(t *testing.T) {
	f, trip, asset := photoWebFixture(t, true)
	seven := int32(7)
	photoWebCreate(t, f, trip, asset, "2026-10-05", &seven)
	id, key := uuid.New(), uuid.New()
	body := map[string]any{"id": id, "asset_id": asset, "recorded_on": "2026-10-05"}
	created := photoWebResult(t, photoWebRequest(f, trip, uuid.Nil, key, http.MethodPost, "", body, nil), 201)
	if created.Data.(map[string]any)["sort_order"] != float64(8) {
		t.Fatalf("append %+v", created)
	}
	photoWebFacts(t, f, created, trip, "2026-10-05")
	zero := int32(0)
	_, explicit := photoWebCreate(t, f, trip, asset, "2026-10-05", &zero)
	if explicit.Data.(map[string]any)["sort_order"] != float64(0) {
		t.Fatal("explicit zero omitted")
	}
	replay := photoWebResult(t, photoWebRequest(f, trip, uuid.Nil, key, http.MethodPost, "", body, nil), 201)
	if !replay.Replayed || !reflect.DeepEqual(created.ScopeRevisions, replay.ScopeRevisions) {
		t.Fatal("create facts drifted")
	}
	body["sort_order"] = 0
	expectStatus(t, photoWebRequest(f, trip, uuid.Nil, key, http.MethodPost, "", body, nil), 409, "IDEMPOTENCY_CONFLICT")
	deleteKey := uuid.New()
	deleted := photoWebResult(t, photoWebRequest(f, trip, id, deleteKey, http.MethodDelete, "1", nil, nil), 200)
	photoWebFacts(t, f, deleted, trip, "2026-10-05")
	photoWebCreate(t, f, trip, asset, "2026-10-05", nil)
	replay = photoWebResult(t, photoWebRequest(f, trip, id, deleteKey, http.MethodDelete, "1", nil, nil), 200)
	if !replay.Replayed || !reflect.DeepEqual(deleted.ScopeRevisions, replay.ScopeRevisions) {
		t.Fatal("delete facts drifted")
	}
	// A successful sensitive no-op receipt remains valid after its guard grows stale.
	other, _ := photoWebCreate(t, f, trip, asset, "2026-10-06", nil)
	noopKey := uuid.New()
	guards := photoWebGuards(t, f, trip, "2026-10-06")
	patch := map[string]any{"recorded_on": "2026-10-06"}
	first := photoWebResult(t, photoWebRequest(f, trip, other, noopKey, http.MethodPatch, "1", patch, guards), 200)
	photoWebResult(t, photoWebRequest(f, trip, other, uuid.New(), http.MethodPatch, "1", map[string]any{"caption": "later"}, nil), 200)
	replay = photoWebResult(t, photoWebRequest(f, trip, other, noopKey, http.MethodPatch, "1", patch, guards), 200)
	if !replay.Replayed || !reflect.DeepEqual(first.ScopeRevisions, replay.ScopeRevisions) || *replay.Primary.Version != 1 || replay.Data.(map[string]any)["version"] != "2" {
		t.Fatalf("no-op original facts %+v", replay)
	}
	expectStatus(t, photoWebRequest(f, trip, other, noopKey, http.MethodPatch, "1", patch, photoWebGuards(t, f, trip, "2026-10-06")), 409, "IDEMPOTENCY_CONFLICT")
}

func TestWebPhotoGuardsConcurrentScopeAndReceiptRollback(t *testing.T) {
	f, trip, asset := photoWebFixture(t, true)
	one, _ := photoWebCreate(t, f, trip, asset, "2026-10-05", nil)
	two, _ := photoWebCreate(t, f, trip, asset, "2026-10-05", nil)
	guards := photoWebGuards(t, f, trip, "2026-10-05")
	var responses [2]apiResponse
	var wg sync.WaitGroup
	for i, id := range []uuid.UUID{one, two} {
		wg.Add(1)
		go func(i int, id uuid.UUID) {
			defer wg.Done()
			responses[i] = photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "1", map[string]any{"sort_order": 8 + i}, guards)
		}(i, id)
	}
	wg.Wait()
	success, conflict := 0, 0
	for _, r := range responses {
		if r.Status == 200 {
			success++
		} else {
			expectStatus(t, r, 412, "COLLECTION_CONFLICT")
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("scope race %d/%d", success, conflict)
	}
	id, _ := photoWebCreate(t, f, trip, asset, "2026-10-07", nil)
	key := uuid.New()
	beforeOld, beforeNew := mediaDayRevision(t, f, trip, "2026-10-07"), mediaDayRevision(t, f, trip, "2026-10-08")
	var seq int64
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	f.sql(`CREATE FUNCTION public.h07_web_photo_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_id='` + key.String() + `'::uuid THEN RAISE EXCEPTION 'receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER h07_web_photo_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION public.h07_web_photo_fail()`)
	t.Cleanup(func() {
		f.sql(`DROP TRIGGER IF EXISTS h07_web_photo_fail ON mutation_receipts; DROP FUNCTION IF EXISTS public.h07_web_photo_fail()`)
	})
	r := photoWebRequest(f, trip, id, key, http.MethodPatch, "1", map[string]any{"recorded_on": "2026-10-08"}, photoWebGuards(t, f, trip, "2026-10-07", "2026-10-08"))
	if r.Status < 500 {
		t.Fatalf("receipt failure accepted %d %s", r.Status, r.Raw)
	}
	var intact bool
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT last_seq=$2 FROM account_sync_state WHERE account_id=$1) AND (SELECT version=1 AND recorded_on='2026-10-07' FROM photos WHERE id=$3) AND NOT EXISTS(SELECT 1 FROM mutation_receipts WHERE account_id=$1 AND operation_id=$4) AND NOT EXISTS(SELECT 1 FROM sync_changes WHERE account_id=$1 AND seq>$2)`, f.owner, seq, id, key).Scan(&intact); err != nil || !intact {
		t.Fatalf("rollback %t %v", intact, err)
	}
	if mediaDayRevision(t, f, trip, "2026-10-07") != beforeOld || mediaDayRevision(t, f, trip, "2026-10-08") != beforeNew {
		t.Fatal("failed move changed scope")
	}
}

func TestWebPhotoGuardsRESTChangesReachSync(t *testing.T) {
	f, trip, asset := photoWebFixture(t, true)
	id, _ := photoWebCreate(t, f, trip, asset, "2026-10-05", nil)
	base := f.snapshot("baseline", []uuid.UUID{trip})
	f.build(base.ID)
	_, last := f.pages(base.ID)
	photoWebResult(t, photoWebRequest(f, trip, id, uuid.New(), http.MethodPatch, "1", map[string]any{"recorded_on": "2026-10-06"}, photoWebGuards(t, f, trip, "2026-10-05", "2026-10-06")), 200)
	delta, err := f.svc.Changes(context.Background(), f.a, "2", *last.BaselineCursor, 100)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(delta.Changes)
	if !strings.Contains(string(raw), id.String()) || !strings.Contains(string(raw), "2026-10-06") {
		t.Fatalf("missing REST delta %s", raw)
	}
	after := f.snapshot("trip_reload", []uuid.UUID{trip})
	f.build(after.ID)
	pages, _ := f.pages(after.ID)
	raw, _ = json.Marshal(pages)
	if !strings.Contains(string(raw), id.String()) || !strings.Contains(string(raw), "2026-10-06") {
		t.Fatal(fmt.Sprintf("missing updated photo snapshot %s", raw))
	}
}
