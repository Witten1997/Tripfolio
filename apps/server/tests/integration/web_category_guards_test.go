package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/google/uuid"
	baselinepg "tripfolio/server/internal/adapters/postgres/collectionbaseline"
	"tripfolio/server/internal/foundation/write"
	baseline "tripfolio/server/internal/modules/collectionbaseline"
	"tripfolio/server/internal/modules/finance"
	syncmodule "tripfolio/server/internal/modules/sync"
)

// Read items and the revision from the same complete baseline, as the editor does.
func categoryWebBaseline(t *testing.T, f *pushFixture) ([]finance.CategoryResource, []write.ScopeRevision) {
	t.Helper()
	svc := collectionService(t, baselinepg.NewStore(f.pool))
	q := baseline.Query{Kind: "categories", ScopeID: f.owner.String(), Limit: 2}
	items := []finance.CategoryResource{}
	pages := []baseline.Page{}
	for {
		page, err := svc.List(context.Background(), f.a, q)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, page)
		for _, raw := range page.Items {
			var item finance.CategoryResource
			if err := json.Unmarshal(raw, &item); err != nil {
				t.Fatal(err)
			}
			items = append(items, item)
		}
		if page.NextCursor == nil {
			break
		}
		q.Cursor = *page.NextCursor
	}
	verifyCollectionDigest(t, pages, len(items))
	return items, []write.ScopeRevision{{Kind: "categories", ScopeID: f.owner.String(), Revision: pages[0].Revision}}
}

func categoryWebRequest(f *pushFixture, method string, id, key uuid.UUID, version string, body any, guard string) apiResponse {
	path := "/expense-categories"
	if id != uuid.Nil {
		path += "/" + id.String()
	}
	h := map[string]string{"Idempotency-Key": key.String()}
	if version != "" {
		h["If-Match"] = `"` + version + `"`
	}
	if guard != "" {
		h["X-Collection-Guards"] = guard
	}
	return f.do(request{method: method, path: path, token: f.webToken, headers: h, body: body})
}

func categoryWebCreate(t *testing.T, f *pushFixture, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	r := categoryWebRequest(f, http.MethodPost, uuid.Nil, uuid.New(), "", map[string]any{"id": id, "name": name, "sort_order": 0}, "")
	expectStatus(t, r, 201, "")
	categoryWebFacts(t, f, mfResult(t, r))
	return id
}

func categoryWebFacts(t *testing.T, f *pushFixture, result write.Result) {
	t.Helper()
	_, want := categoryWebBaseline(t, f)
	if !reflect.DeepEqual(result.ScopeRevisions, want) {
		t.Fatalf("final facts=%+v want=%+v", result.ScopeRevisions, want)
	}
}

func TestWebCategoryGuardsPolicyAndNoop(t *testing.T) {
	f := newPushFixture(t)
	id := categoryWebCreate(t, f, "category-policy")
	body := map[string]any{"sort_order": 0}
	legacyKey := uuid.New()
	legacy := categoryWebRequest(f, "PATCH", id, legacyKey, "1", body, "")
	expectStatus(t, legacy, 200, "")
	categoryWebFacts(t, f, mfResult(t, legacy))
	// Sticky REST protection must work even without a v2-enabled epoch.
	f.sql(`INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, f.owner)
	expectStatus(t, categoryWebRequest(f, "PATCH", id, uuid.New(), "1", body, ""), 428, "COLLECTION_BASE_REQUIRED")
	_, before := categoryWebBaseline(t, f)
	guard := mfHeader(t, before)
	key := uuid.New()
	noop := categoryWebRequest(f, "PATCH", id, key, "1", body, guard)
	expectStatus(t, noop, 200, "")
	if r := mfResult(t, noop); r.CommitCursor != nil || !reflect.DeepEqual(r.ScopeRevisions, before) {
		t.Fatal("no-op changed entity or lost facts")
	}
	changed := categoryWebRequest(f, "PATCH", id, uuid.New(), "1", map[string]any{"name": "category-renamed"}, "")
	expectStatus(t, changed, 200, "")
	categoryWebFacts(t, f, mfResult(t, changed))
	// Even a stale entity base must fail collection validation before merge resolution.
	expectStatus(t, categoryWebRequest(f, "PATCH", id, uuid.New(), "1", body, guard), 412, "COLLECTION_CONFLICT")
	for _, entry := range []struct {
		key   uuid.UUID
		guard string
		facts []write.ScopeRevision
	}{{key, guard, before}, {legacyKey, "", mfResult(t, legacy).ScopeRevisions}} {
		r := categoryWebRequest(f, "PATCH", id, entry.key, "1", body, entry.guard)
		expectStatus(t, r, 200, "")
		result := mfResult(t, r)
		if !result.Replayed || !reflect.DeepEqual(result.ScopeRevisions, entry.facts) {
			t.Fatal("successful receipt facts drifted")
		}
		if data := result.Data.(map[string]any); data["name"] != "category-renamed" {
			t.Fatal("replay should reload current data separately")
		}
	}
	// Historical receipts lacking collection facts must not acquire current facts.
	f.sql(`UPDATE mutation_receipts SET result=result-'scope_revisions' WHERE account_id=$1 AND operation_id=$2`, f.owner, legacyKey)
	r := categoryWebRequest(f, "PATCH", id, legacyKey, "1", body, "")
	expectStatus(t, r, 200, "")
	if result := mfResult(t, r); !result.Replayed || len(result.ScopeRevisions) != 0 {
		t.Fatal("fabricated historical facts")
	}
}

func TestWebCategoryGuardsCRUDInvalidatesBaseline(t *testing.T) {
	f := newPushFixture(t)
	anchor := categoryWebCreate(t, f, "anchor")
	f.sql(`INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, f.owner)
	var target uuid.UUID
	for _, action := range []string{"create", "name", "icon", "delete"} {
		t.Run(action, func(t *testing.T) {
			_, before := categoryWebBaseline(t, f)
			guard := mfHeader(t, before)
			method, id, version, status := "PATCH", target, "1", 200
			var body any
			switch action {
			case "create":
				target = uuid.New()
				method = "POST"
				id = uuid.Nil
				version = ""
				status = 201
				body = map[string]any{"id": target, "name": "temporary"}
			case "name":
				body = map[string]any{"name": "temporary-renamed"}
			case "icon":
				version = "2"
				body = map[string]any{"icon": "ticket"}
			case "delete":
				method = "DELETE"
				version = "3"
			}
			expectStatus(t, categoryWebRequest(f, method, id, uuid.New(), version, body, guard), 422, "INVALID_REFERENCE")
			r := categoryWebRequest(f, method, id, uuid.New(), version, body, "")
			expectStatus(t, r, status, "")
			categoryWebFacts(t, f, mfResult(t, r))
			if reflect.DeepEqual(before, mfResult(t, r).ScopeRevisions) {
				t.Fatal("CRUD did not invalidate baseline")
			}
			expectStatus(t, categoryWebRequest(f, "PATCH", anchor, uuid.New(), "1", map[string]any{"sort_order": 0}, guard), 412, "COLLECTION_CONFLICT")
		})
	}
	_, latest := categoryWebBaseline(t, f)
	r := categoryWebRequest(f, "PATCH", anchor, uuid.New(), "1", map[string]any{"sort_order": 0}, mfHeader(t, latest))
	expectStatus(t, r, 200, "")
	categoryWebFacts(t, f, mfResult(t, r))
}

func TestWebCategoryGuardsNativeProofAndDependencyReplay(t *testing.T) {
	f := newPushFixture(t)
	f.sql(`INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, f.owner)
	enablePushTestAccount(f)
	items, before := categoryWebBaseline(t, f)
	slices.SortFunc(items, func(a, b finance.CategoryResource) int { return int(a.SortOrder - b.SortOrder) })
	ids := make([]uuid.UUID, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	slices.Reverse(ids)
	op := withFinanceGuard(financeOp("expense_category", "reorder", uuid.Nil, uuid.Nil, nil, map[string]any{"ordered_ids": ids}), financeGuard(f, "categories", f.owner))
	dependent := financeOp("expense_category", "reorder", uuid.Nil, uuid.Nil, nil, map[string]any{"ordered_ids": ids}, op.OperationID)
	dependent.Guards = []syncmodule.GuardReference{{Kind: "categories", ScopeID: f.owner.String(), OperationID: &op.OperationID}}
	out := f.push(op, dependent)
	for _, r := range out.Results {
		financeApplied(t, r)
	}
	_, after := categoryWebBaseline(t, f)
	if reflect.DeepEqual(before, after) || !reflect.DeepEqual(out.Results[0].Result.ScopeRevisions, after) || !reflect.DeepEqual(out.Results[1].Result.ScopeRevisions, after) || out.Results[1].Result.CommitCursor != nil {
		t.Fatal("native proof/dependency final facts")
	}
	categoryWebCreate(t, f, "after-native-reorder")
	replay := f.push(op, dependent)
	for i, r := range replay.Results {
		if r.Status != "replayed" || !reflect.DeepEqual(r.Result.ScopeRevisions, out.Results[i].Result.ScopeRevisions) {
			t.Fatal("native original facts drifted")
		}
	}
	stale := dependent
	stale.OperationID = uuid.New()
	financeCode(t, f.push(op, stale).Results[1], "COLLECTION_CONFLICT")
}

func TestWebCategoryGuardsConcurrentAndReceiptRollback(t *testing.T) {
	f := newPushFixture(t)
	one, two := categoryWebCreate(t, f, "race-one"), categoryWebCreate(t, f, "race-two")
	f.sql(`INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, f.owner)
	_, base := categoryWebBaseline(t, f)
	guard := mfHeader(t, base)
	var responses [2]apiResponse
	var wg sync.WaitGroup
	for i, id := range []uuid.UUID{one, two} {
		wg.Add(1)
		go func(i int, id uuid.UUID) {
			defer wg.Done()
			responses[i] = categoryWebRequest(f, "PATCH", id, uuid.New(), "1", map[string]any{"sort_order": 20 + i}, guard)
		}(i, id)
	}
	wg.Wait()
	wins := 0
	for _, r := range responses {
		if r.Status == 200 {
			wins++
		} else {
			expectStatus(t, r, 412, "COLLECTION_CONFLICT")
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent successes=%d", wins)
	}
	id := categoryWebCreate(t, f, "rollback")
	key := uuid.New()
	_, before := categoryWebBaseline(t, f)
	var seq int64
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	f.sql(`CREATE FUNCTION public.h07_web_category_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_id='` + key.String() + `'::uuid THEN RAISE EXCEPTION 'receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER h07_web_category_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION public.h07_web_category_fail()`)
	t.Cleanup(func() {
		f.sql(`DROP TRIGGER IF EXISTS h07_web_category_fail ON mutation_receipts; DROP FUNCTION IF EXISTS public.h07_web_category_fail()`)
	})
	r := categoryWebRequest(f, "PATCH", id, key, "1", map[string]any{"sort_order": 99}, mfHeader(t, before))
	if r.Status < 500 {
		t.Fatalf("receipt fault accepted: %d %s", r.Status, r.Raw)
	}
	financeCheck(f, `SELECT (SELECT last_seq=$2 FROM account_sync_state WHERE account_id=$1) AND (SELECT version=1 AND sort_order=0 FROM expense_categories WHERE id=$3) AND NOT EXISTS(SELECT 1 FROM mutation_receipts WHERE account_id=$1 AND operation_id=$4) AND NOT EXISTS(SELECT 1 FROM sync_changes WHERE account_id=$1 AND seq>$2)`, f.owner, seq, id, key)
	_, after := categoryWebBaseline(t, f)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rollback changed category revision")
	}
}

func TestWebCategoryGuardsIsolationEpochAndEmpty(t *testing.T) {
	f := newPushFixture(t)
	id := categoryWebCreate(t, f, "boundary")
	f.sql(`INSERT INTO account_sync_capabilities(account_id,collection_guards_required) VALUES($1,true)`, f.owner)
	_, before := categoryWebBaseline(t, f)
	foreign := f.registerWeb(uniqueEmail(), "correct horse battery").data()["access_token"].(string)
	expectStatus(t, f.do(request{method: "PATCH", path: "/expense-categories/" + id.String(), token: foreign, headers: f.authHeaders(map[string]string{"If-Match": `"1"`}), body: map[string]any{"sort_order": 0}}), 404, "RESOURCE_NOT_FOUND")
	wrong := append([]write.ScopeRevision(nil), before...)
	wrong[0].ScopeID = uuid.NewString()
	expectStatus(t, categoryWebRequest(f, "PATCH", id, uuid.New(), "1", map[string]any{"sort_order": 0}, mfHeader(t, wrong)), 422, "INVALID_REFERENCE")
	// Epoch rotation invalidates an otherwise identical member/version set.
	f.sql(`UPDATE account_sync_state SET sync_epoch=$2 WHERE account_id=$1`, f.owner, uuid.New())
	expectStatus(t, categoryWebRequest(f, "PATCH", id, uuid.New(), "1", map[string]any{"sort_order": 0}, mfHeader(t, before)), 412, "COLLECTION_CONFLICT")
	if err := f.pool.QueryRow(context.Background(), `SELECT sync_epoch FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&f.epoch); err != nil {
		t.Fatal(err)
	}
	items, _ := categoryWebBaseline(t, f)
	for _, item := range items {
		r := categoryWebRequest(f, "DELETE", item.ID, uuid.New(), strconv.FormatInt(int64(item.Version), 10), nil, "")
		expectStatus(t, r, 200, "")
		categoryWebFacts(t, f, mfResult(t, r))
	}
	expectStatus(t, categoryWebRequest(f, "PATCH", id, uuid.New(), "2", map[string]any{"sort_order": 0}, ""), 410, "RESOURCE_GONE")
	empty, base := categoryWebBaseline(t, f)
	if len(empty) != 0 {
		t.Fatal("nonempty collection")
	}
	op := withFinanceGuard(financeOp("expense_category", "reorder", uuid.Nil, uuid.Nil, nil, map[string]any{"ordered_ids": []uuid.UUID{}}), financeGuard(f, "categories", f.owner))
	expectStatus(t, f.do(request{method: "POST", path: "/sync/push", token: f.token, headers: map[string]string{"X-Tripfolio-Sync-Version": "2"}, body: f.input(op)}), 409, "SYNC_NOT_READY")
	enablePushTestAccount(f)
	r := f.push(op).Results[0]
	financeApplied(t, r)
	if r.Result.CommitCursor != nil || !reflect.DeepEqual(r.Result.ScopeRevisions, base) {
		t.Fatal("empty reorder lost facts")
	}
}
