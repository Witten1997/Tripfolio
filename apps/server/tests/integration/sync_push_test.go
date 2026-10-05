package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	syncpg "tripfolio/server/internal/adapters/postgres/sync"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
	syncmodule "tripfolio/server/internal/modules/sync"
)

type pushFixture struct {
	*snapshotFixture
	client uuid.UUID
}

func newPushFixture(t *testing.T) *pushFixture {
	f := newSnapshotFixture(t)
	f.svc.WithPush(syncpg.NewPushStore(pgcore.NewWriter(f.pool, nil, clock.Real{}, quietLogger())))
	return &pushFixture{f, uuid.New()}
}
func pushOp(kind string, id uuid.UUID, base *syncmodule.BaseReference, payload any, deps ...uuid.UUID) syncmodule.Operation {
	raw, _ := json.Marshal(payload)
	return syncmodule.Operation{OperationID: uuid.New(), Type: kind, EntityType: "trip", EntityID: &id, TripID: &id, Base: base, Guards: []syncmodule.GuardReference{}, DependsOn: append([]uuid.UUID{}, deps...), Payload: raw}
}
func createPush(name string) (syncmodule.Operation, uuid.UUID) {
	self := uuid.New()
	return pushOp("trip.create", uuid.New(), nil, map[string]any{"name": name, "start_date": "2026-10-01", "end_date": "2026-10-04", "timezone": "Asia/Shanghai", "currency_code": "CNY", "self_member_id": self}), self
}
func refBase(op uuid.UUID) *syncmodule.BaseReference {
	return &syncmodule.BaseReference{OperationID: &op}
}
func versionBase(v string) *syncmodule.BaseReference { return &syncmodule.BaseReference{Version: &v} }
func (f *pushFixture) input(ops ...syncmodule.Operation) syncmodule.PushInput {
	return syncmodule.PushInput{SyncEpoch: f.epoch, ClientID: f.client, Operations: ops}
}
func (f *pushFixture) push(ops ...syncmodule.Operation) syncmodule.PushOutput {
	f.t.Helper()
	r := f.do(request{method: http.MethodPost, path: "/sync/push", token: f.token, headers: map[string]string{"X-Tripfolio-Sync-Version": "2"}, body: f.input(ops...)})
	expectStatus(f.t, r, 200, "")
	var out syncmodule.PushOutput
	if err := json.Unmarshal(r.Raw, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}
func resultVersion(t *testing.T, r syncmodule.PushResult, id uuid.UUID) string {
	t.Helper()
	if r.Result == nil {
		t.Fatalf("missing success result: %+v", r)
	}
	for _, ref := range r.Result.References {
		if ref.Type == "trip" && ref.ID == id && ref.Version != nil {
			return strconv.FormatInt(int64(*ref.Version), 10)
		}
	}
	t.Fatal("missing trip reference")
	return ""
}

func TestHTTPSyncPushTransactions(t *testing.T) {
	f := newPushFixture(t)
	ctx := context.Background()
	create, self := createPush("first")
	update := pushOp("trip.update", *create.EntityID, refBase(create.OperationID), map[string]any{"name": "second"}, create.OperationID)
	noop := pushOp("trip.set_archived", *create.EntityID, refBase(update.OperationID), map[string]any{"archived": false}, update.OperationID, create.OperationID)
	noop.Guards = []syncmodule.GuardReference{{Kind: "members", ScopeID: create.TripID.String(), OperationID: &create.OperationID}}
	first := f.push(create, update, noop)
	for i, r := range first.Results {
		if r.Status != "applied" {
			t.Fatalf("operation %d: %+v", i, r)
		}
	}
	if resultVersion(t, first.Results[0], *create.EntityID) != "1" || resultVersion(t, first.Results[1], *create.EntityID) != "2" || resultVersion(t, first.Results[2], *create.EntityID) != "2" || first.Results[2].Result.CommitCursor != nil || len(first.Results[2].Result.ScopeRevisions) != 1 {
		t.Fatal("original/no-op facts incorrect")
	}
	var memberID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM trip_members WHERE account_id=$1 AND trip_id=$2 AND is_self`, f.owner, *create.EntityID).Scan(&memberID); err != nil || memberID != self {
		t.Fatalf("stable self: %v %v", memberID, err)
	}
	replay := f.push(create, update, noop)
	for i, r := range replay.Results {
		if r.Status != "replayed" || resultVersion(t, r, *create.EntityID) != resultVersion(t, first.Results[i], *create.EntityID) {
			t.Fatalf("replay facts %+v", r)
		}
	}
	current := replay.Results[0].Result.Data.(map[string]any)
	if current["version"] != "2" || current["name"] != "second" {
		t.Fatal("replay data is not current")
	}
	changed := create
	changed.Payload = bytes.Replace(create.Payload, []byte("first"), []byte("different"), 1)
	r := f.push(changed).Results[0]
	if r.Error == nil || r.Error.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed fingerprint: %+v", r)
	}
	stale := pushOp("trip.set_archived", *create.EntityID, versionBase("1"), map[string]any{"archived": false})
	if r := f.push(stale).Results[0]; r.Status != "conflict" {
		t.Fatalf("same target stale state accepted %+v", r)
	}
	next := pushOp("trip.update", *create.EntityID, refBase(noop.OperationID), map[string]any{"notes": "from no-op"}, noop.OperationID)
	if r := f.push(next).Results[0]; r.Status != "applied" {
		t.Fatalf("no-op dependency failed %+v", r)
	}
	if r := f.push(noop).Results[0]; resultVersion(t, r, *create.EntityID) != "2" || r.Result.Data.(map[string]any)["version"] != "3" {
		t.Fatal("no-op fact mutated with later version")
	}
	if _, err := f.svc.Changes(ctx, f.a, "2", *first.Results[0].Result.CommitCursor, 10); err == nil {
		t.Fatal("commit cursor accepted as checkpoint")
	}
	baseline := f.snapshot("baseline", []uuid.UUID{*create.EntityID})
	f.build(baseline.ID)
	_, last := f.pages(baseline.ID)
	archive := pushOp("trip.set_archived", *create.EntityID, versionBase("3"), map[string]any{"archived": true})
	del := pushOp("trip.delete", *create.EntityID, refBase(archive.OperationID), map[string]any{}, archive.OperationID)
	restore := pushOp("trip.restore", *create.EntityID, refBase(del.OperationID), map[string]any{}, del.OperationID)
	for _, r := range f.push(archive, del, restore).Results {
		if r.Status != "applied" {
			t.Fatalf("lifecycle %+v", r)
		}
	}
	delta, err := f.svc.Changes(ctx, f.a, "2", *last.BaselineCursor, 100)
	if err != nil || len(delta.Changes) != 3 || !delta.Changes[2].RequiresSnapshot {
		t.Fatalf("push changes: %+v %v", delta, err)
	}
	f.sql(`UPDATE trips SET deleted_at=now(),purge_after_at=now(),purge_requested_at=now() WHERE id=$1`, *create.EntityID)
	if r := f.push(create).Results[0]; r.Status != "replayed" || r.Result.Data != nil {
		t.Fatalf("purging replay %+v", r)
	}
}

func TestHTTPSyncPushFailureIsolationAndLimits(t *testing.T) {
	f := newPushFixture(t)
	ctx := context.Background()
	bad, _ := createPush("")
	dep := pushOp("trip.update", *bad.EntityID, refBase(bad.OperationID), map[string]any{"notes": "blocked"}, bad.OperationID)
	good, _ := createPush("independent")
	out := f.push(bad, dep, good)
	if out.Results[0].Status != "rejected" || out.Results[1].Error.Code != "DEPENDENCY_REJECTED" || out.Results[2].Status != "applied" {
		t.Fatalf("isolation %+v", out)
	}
	forward, _ := createPush("never committed")
	later, _ := createPush("later")
	forward.DependsOn = []uuid.UUID{later.OperationID}
	r := f.do(request{method: http.MethodPost, path: "/sync/push", token: f.token, headers: map[string]string{"X-Tripfolio-Sync-Version": "2"}, body: f.input(forward, later)})
	expectStatus(t, r, 400, "MALFORMED_REQUEST")
	var exists bool
	_ = f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM trips WHERE id=$1 OR id=$2)`, *forward.EntityID, *later.EntityID).Scan(&exists)
	if exists {
		t.Fatal("invalid batch partially committed")
	}
	missing := uuid.New()
	unresolved, _ := createPush("unresolved")
	unresolved.DependsOn = []uuid.UUID{missing}
	if r := f.push(unresolved).Results[0]; r.Error.Code != "DEPENDENCY_NOT_APPLIED" || !r.Error.Retryable {
		t.Fatalf("missing dep %+v", r)
	}
	foreign := f.registerWeb(uniqueEmail(), "correct horse battery")
	foreignToken := foreign.data()["access_token"].(string)
	foreignTrip := uuid.MustParse(f.createTrip(foreignToken, map[string]any{"name": "foreign", "start_date": "2026-10-01", "end_date": "2026-10-04"})["id"].(string))
	var foreignOperation uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT batch_id FROM sync_changes WHERE entity_type='trip' AND entity_id=$1 ORDER BY seq LIMIT 1`, foreignTrip).Scan(&foreignOperation); err != nil {
		t.Fatal(err)
	}
	crossDependency, _ := createPush("foreign dependency")
	crossDependency.DependsOn = []uuid.UUID{foreignOperation}
	if r := f.push(crossDependency).Results[0]; r.Error.Code != "DEPENDENCY_NOT_APPLIED" {
		t.Fatalf("cross-account dependency leaked %+v", r)
	}
	var legacyHasSync bool
	if err := f.pool.QueryRow(ctx, `SELECT result ? 'sync' FROM mutation_receipts WHERE operation_id=$1`, foreignOperation).Scan(&legacyHasSync); err != nil || legacyHasSync {
		t.Fatalf("legacy receipt changed %t %v", legacyHasSync, err)
	}
	if r := f.push(pushOp("trip.update", foreignTrip, versionBase("1"), map[string]any{"name": "forbidden"})).Results[0]; r.Error.Code != "RESOURCE_NOT_FOUND" {
		t.Fatalf("foreign write %+v", r)
	}
	for _, test := range []struct {
		body, encoding string
		status         int
	}{{strings.Repeat(" ", syncmodule.MaxPushBytes+1), "", 413}, {`{}`, "gzip", 415}, {`{"sync_epoch":"x","sync_epoch":"y"}`, "", 400}} {
		req, _ := http.NewRequest(http.MethodPost, f.server.URL+"/api/v1/sync/push", io.NopCloser(strings.NewReader(test.body)))
		req.ContentLength = -1
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("X-Tripfolio-Sync-Version", "2")
		req.Header.Set("Content-Type", "application/json")
		if test.encoding != "" {
			req.Header.Set("Content-Encoding", test.encoding)
		}
		resp, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != test.status {
			t.Fatalf("chunked/encoded status=%d want=%d", resp.StatusCode, test.status)
		}
	}
	wrong := f.input(good)
	used, _ := createPush("reused self")
	var payload map[string]any
	_ = json.Unmarshal(used.Payload, &payload)
	var existingSelf uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM trip_members WHERE trip_id=$1 AND is_self`, *good.EntityID).Scan(&existingSelf); err != nil {
		t.Fatal(err)
	}
	payload["self_member_id"] = existingSelf
	used.Payload, _ = json.Marshal(payload)
	if r := f.push(used).Results[0]; r.Error.Code != "ID_ALREADY_USED" {
		t.Fatalf("reused self accepted %+v", r)
	}
	tombstoneSelf := uuid.New()
	f.sql(`INSERT INTO entity_tombstones(account_id,entity_type,entity_id,trip_id,last_version,deleted_at,purged_at) VALUES($1,'trip_member',$2,$3,3,now(),now())`, f.owner, tombstoneSelf, *good.EntityID)
	used.OperationID = uuid.New()
	payload["self_member_id"] = tombstoneSelf
	used.Payload, _ = json.Marshal(payload)
	if r := f.push(used).Results[0]; r.Error.Code != "ID_ALREADY_USED" {
		t.Fatalf("tombstoned self accepted %+v", r)
	}
	wrong.SyncEpoch = uuid.New()
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/sync/push", token: f.token, headers: map[string]string{"X-Tripfolio-Sync-Version": "2"}, body: wrong}), 409, "SYNC_EPOCH_MISMATCH")
	f.sql(`UPDATE account_sessions SET revoked_at=now() WHERE id=$1`, f.a.SessionID)
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/sync/push", token: f.token, headers: map[string]string{"X-Tripfolio-Sync-Version": "2"}, body: f.input(good)}), 401, "SESSION_EXPIRED")
}

func TestSyncPushConcurrentRetryAndRollback(t *testing.T) {
	f := newPushFixture(t)
	ctx := context.Background()
	op, _ := createPush("concurrent")
	var wg sync.WaitGroup
	results := make(chan syncmodule.PushOutput, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := f.svc.Push(ctx, f.a, "2", f.input(op)); results <- r; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	applied := 0
	for r := range results {
		if r.Results[0].Status == "applied" {
			applied++
		} else if r.Results[0].Status != "replayed" {
			t.Fatalf("concurrent %+v", r)
		}
	}
	if applied != 1 {
		t.Fatalf("applied %d times", applied)
	}
	// Lose the HTTP connection after the transaction commits, before delivering a response.
	lost, _ := createPush("lost response")
	original := f.server.Config.Handler
	var once atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sync/push" && once.CompareAndSwap(false, true) {
			original.ServeHTTP(httptest.NewRecorder(), r)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		original.ServeHTTP(w, r)
	}))
	defer server.Close()
	raw, _ := json.Marshal(f.input(lost))
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/sync/push", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Tripfolio-Sync-Version", "2")
	response, err := server.Client().Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("response loss injection failed")
	}
	if r := f.push(lost).Results[0]; r.Status != "replayed" {
		t.Fatalf("lost response duplicated write %+v", r)
	}
	if r := f.push(op).Results[0]; r.Status != "replayed" {
		t.Fatal("uncertain retry duplicated write")
	}
	f.sql(`CREATE FUNCTION public.h07_fail_push() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type LIKE 'sync.%' THEN RAISE EXCEPTION 'H07 receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER h07_fail_push BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION public.h07_fail_push()`)
	fault, _ := createPush("must roll back")
	if r := f.push(fault).Results[0]; r.Status != "failed" {
		t.Fatalf("fault not surfaced %+v", r)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM trips WHERE id=$1)+(SELECT count(*) FROM trip_members WHERE trip_id=$1)+(SELECT count(*) FROM sync_changes WHERE batch_id=$2)+(SELECT count(*) FROM mutation_receipts WHERE operation_id=$2)`, *fault.EntityID, fault.OperationID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial commit %d %v", count, err)
	}
	f.sql(`DROP TRIGGER h07_fail_push ON mutation_receipts; DROP FUNCTION public.h07_fail_push()`)
	if r := f.push(fault).Results[0]; r.Status != "applied" {
		t.Fatalf("retry after rollback %+v", r)
	}
	// A legacy receipt must never be interpreted as containing a synchronized original version.
	legacyID := uuid.New()
	f.sql(`INSERT INTO mutation_receipts(account_id,operation_id,operation_type,request_hash,result) VALUES($1,$2,'legacy',decode(repeat('00',32),'hex'),'{}')`, f.owner, legacyID)
	legacy := pushOp("trip.update", *op.EntityID, refBase(legacyID), map[string]any{"notes": "no fabricated version"}, legacyID)
	if r := f.push(legacy).Results[0]; r.Error.Code != "INVALID_REFERENCE" {
		t.Fatalf("legacy reference %+v", r)
	}
}

type afterPush struct {
	repo  syncmodule.PushRepository
	after func()
	once  sync.Once
}

func (p *afterPush) Execute(ctx context.Context, a actor.Actor, epoch uuid.UUID, op syncmodule.PreparedOperation) (write.Result, error) {
	r, err := p.repo.Execute(ctx, a, epoch, op)
	if err == nil {
		p.once.Do(p.after)
	}
	return r, err
}

func TestSyncPushMidBatchAuthorizationAndMerge(t *testing.T) {
	for _, scenario := range []string{"session", "epoch"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPushFixture(t)
			first, _ := createPush("first committed")
			second, _ := createPush("must not execute")
			repo := syncpg.NewPushStore(pgcore.NewWriter(f.pool, nil, clock.Real{}, quietLogger()))
			f.svc.WithPush(&afterPush{repo: repo, after: func() {
				if scenario == "session" {
					f.sql(`UPDATE account_sessions SET revoked_at=now() WHERE id=$1`, f.a.SessionID)
				} else {
					f.sql(`UPDATE account_sync_state SET sync_epoch=gen_random_uuid() WHERE account_id=$1`, f.owner)
				}
			}})
			_, err := f.svc.Push(context.Background(), f.a, "2", f.input(first, second))
			e, ok := apperr.As(err)
			want := "SESSION_EXPIRED"
			if scenario == "epoch" {
				want = "SYNC_EPOCH_MISMATCH"
			}
			if !ok || e.Code != want {
				t.Fatalf("midbatch error %v", err)
			}
			var firstExists, secondExists bool
			if err := f.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM trips WHERE id=$1),EXISTS(SELECT 1 FROM trips WHERE id=$2)`, *first.EntityID, *second.EntityID).Scan(&firstExists, &secondExists); err != nil || !firstExists || secondExists {
				t.Fatalf("midbatch effects %t %t %v", firstExists, secondExists, err)
			}
		})
	}
	t.Run("field groups and history", func(t *testing.T) {
		f := newPushFixture(t)
		create, _ := createPush("merge")
		f.push(create)
		date := pushOp("trip.update", *create.EntityID, versionBase("1"), map[string]any{"end_date": "2026-10-05"})
		f.push(date)
		staleDate := pushOp("trip.update", *create.EntityID, versionBase("1"), map[string]any{"start_date": "2026-09-30"})
		if r := f.push(staleDate).Results[0]; r.Status != "conflict" {
			t.Fatalf("related dates merged %+v", r)
		}
		independent := pushOp("trip.update", *create.EntityID, versionBase("1"), map[string]any{"notes": "safe merge"})
		r := f.push(independent).Results[0]
		if r.Status != "applied" || len(r.Result.Warnings) != 1 || r.Result.Warnings[0] != "MERGED_WITH_NEWER_VERSION" {
			t.Fatalf("independent merge %+v", r)
		}
		f.sql(`DELETE FROM sync_changes WHERE account_id=$1 AND entity_id=$2 AND entity_version=2`, f.owner, *create.EntityID)
		history := pushOp("trip.update", *create.EntityID, versionBase("1"), map[string]any{"destination": "cannot prove"})
		if r := f.push(history).Results[0]; r.Error.Code != "MERGE_HISTORY_UNAVAILABLE" {
			t.Fatalf("missing history %+v", r)
		}
	})
}
