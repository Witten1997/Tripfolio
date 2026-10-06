package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	financepg "tripfolio/server/internal/adapters/postgres/finance"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/modules/travel/member"
)

func mfBaseline(t *testing.T, f *pushFixture, trip uuid.UUID) member.ListBaseline {
	t.Helper()
	r := f.do(request{method: "GET", path: "/trips/" + trip.String() + "/members", token: f.webToken})
	expectStatus(t, r, 200, "")
	var body struct {
		Data           []member.Resource     `json:"data"`
		ScopeRevisions []write.ScopeRevision `json:"scope_revisions"`
	}
	if err := json.Unmarshal(r.Raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Data == nil || len(body.ScopeRevisions) != 1 {
		t.Fatalf("incomplete baseline: %s", r.Raw)
	}
	expected := financeGuard(f, "members", trip)
	if body.ScopeRevisions[0].Revision != *expected.Revision {
		t.Fatalf("members mismatch: %s", r.Raw)
	}
	return member.ListBaseline{Members: body.Data, ScopeRevisions: body.ScopeRevisions}
}
func mfHeader(t *testing.T, revisions []write.ScopeRevision) string {
	t.Helper()
	data, err := json.Marshal(revisions)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func mfResult(t *testing.T, r apiResponse) write.Result {
	t.Helper()
	var body struct {
		Data write.Result `json:"data"`
	}
	if err := json.Unmarshal(r.Raw, &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}
func TestWebMemberFinanceGuards(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	trip, self, cat := financeStart(t, f)
	base := mfBaseline(t, f, trip)
	path := "/trips/" + trip.String()
	saveBody := map[string]any{"members": []any{memberInput(self, "Me", "100")}}
	save := func(headers map[string]string) apiResponse {
		return f.do(request{method: "PUT", path: path + "/members", token: f.webToken, headers: headers, body: saveBody})
	}
	expectStatus(t, save(f.authHeaders(nil)), 428, "COLLECTION_BASE_REQUIRED")
	old := mfHeader(t, base.ScopeRevisions)
	changed := replaceMembers(f, trip, memberInput(self, "Renamed", "100"))
	financeApplied(t, f.push(changed).Results[0])
	expectStatus(t, save(f.authHeaders(map[string]string{"X-Collection-Guards": old})), 412, "COLLECTION_CONFLICT")
	base = mfBaseline(t, f, trip)
	headers := f.authHeaders(map[string]string{"X-Collection-Guards": mfHeader(t, base.ScopeRevisions)})
	saved := save(headers)
	expectStatus(t, saved, 200, "")
	facts := mfResult(t, saved).ScopeRevisions
	if len(facts) != 1 || facts[0].Revision == base.ScopeRevisions[0].Revision {
		t.Fatal("missing final member facts")
	}
	financeApplied(t, f.push(replaceMembers(f, trip, memberInput(self, "After", "100"))).Results[0])
	replay := save(headers)
	expectStatus(t, replay, 200, "")
	if !mfResult(t, replay).Replayed || !reflect.DeepEqual(facts, mfResult(t, replay).ScopeRevisions) {
		t.Fatal("member replay facts drift")
	}
	base = mfBaseline(t, f, trip)
	createBody := map[string]any{"id": uuid.NewString(), "kind": "expense", "amount": "10", "currency_code": "CNY", "category_id": cat, "split_mode": "personal"}
	create := func(h map[string]string) apiResponse {
		return f.do(request{method: "POST", path: path + "/ledger-entries", token: f.webToken, headers: h, body: createBody})
	}
	expectStatus(t, create(f.authHeaders(nil)), 428, "COLLECTION_BASE_REQUIRED")
	expectStatus(t, create(f.authHeaders(map[string]string{"X-Collection-Guards": old})), 412, "COLLECTION_CONFLICT")
	created := create(f.authHeaders(map[string]string{"X-Collection-Guards": mfHeader(t, base.ScopeRevisions)}))
	expectStatus(t, created, 201, "")
	if !reflect.DeepEqual(mfResult(t, created).ScopeRevisions, base.ScopeRevisions) {
		t.Fatal("ledger facts not passed through")
	}
	entryPath := path + "/ledger-entries/" + createBody["id"].(string)
	patches := []map[string]any{{"amount": "10"}, {"currency_code": "CNY"}, {"payer_member_id": self}, {"split_mode": "personal"}, {"participant_member_ids": []uuid.UUID{self}}, {"participant_member_ids": []uuid.UUID{}}}
	for _, patch := range patches {
		send := func(guard string) apiResponse {
			h := f.authHeaders(map[string]string{"If-Match": `"1"`})
			if guard != "" {
				h["X-Collection-Guards"] = guard
			}
			return f.do(request{method: "PATCH", path: entryPath, token: f.webToken, headers: h, body: patch})
		}
		expectStatus(t, send(""), 428, "COLLECTION_BASE_REQUIRED")
		expectStatus(t, send(old), 412, "COLLECTION_CONFLICT")
		r := send(mfHeader(t, base.ScopeRevisions))
		if ids, ok := patch["participant_member_ids"].([]uuid.UUID); ok && len(ids) == 0 {
			expectStatus(t, r, 422, "VALIDATION_FAILED")
		} else {
			expectStatus(t, r, 200, "")
			if !reflect.DeepEqual(mfResult(t, r).ScopeRevisions, base.ScopeRevisions) {
				t.Fatal("no-op facts missing")
			}
		}
	}
	notes := map[string]any{"notes": "memo"}
	expectStatus(t, f.do(request{method: "PATCH", path: entryPath, token: f.webToken, headers: f.authHeaders(map[string]string{"If-Match": `"1"`, "X-Collection-Guards": mfHeader(t, base.ScopeRevisions)}), body: notes}), 422, "INVALID_REFERENCE")
	expectStatus(t, f.do(request{method: "PATCH", path: entryPath, token: f.webToken, headers: f.authHeaders(map[string]string{"If-Match": `"1"`}), body: notes}), 200, "")
	native := ledgerCreate(f, trip, cat, self, "20", self)
	financeApplied(t, f.push(native).Results[0])
	// A required proof established by native CheckCollections must survive service reuse.
	noop := withFinanceGuard(financeOp("ledger_entry", "update", trip, *native.EntityID, versionBase("1"), map[string]any{"currency_code": "CNY"}), financeGuard(f, "members", trip))
	financeApplied(t, f.push(noop).Results[0])
	foreign := f.registerWeb(uniqueEmail(), "correct horse battery").data()["access_token"].(string)
	expectStatus(t, f.do(request{method: "GET", path: path + "/members", token: foreign}), 404, "RESOURCE_NOT_FOUND")
	f.sql(`UPDATE trips SET deleted_at=now(),purge_after_at=now()+interval '30 days' WHERE id=$1`, trip)
	expectStatus(t, f.do(request{method: "GET", path: path + "/members", token: f.webToken}), 410, "TRIP_DELETED")
}

func mfWorkbook(t *testing.T, data []byte, category string) []byte {
	t.Helper()
	book, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	for cell, value := range map[string]string{"A5": "100", "B5": category, "C5": "个人", "D5": "2026-10-01"} {
		if err := book.SetCellStr("账单", cell, value); err != nil {
			t.Fatal(err)
		}
	}
	buf, err := book.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func mfUpload(t *testing.T, f *pushFixture, path string, data []byte, headers map[string]string) apiResponse {
	t.Helper()
	req, err := http.NewRequest("POST", f.server.URL+"/api/v1"+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", f.origin)
	req.Header.Set("Authorization", "Bearer "+f.webToken)
	req.Header.Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("response: %s %v", raw, err)
	}
	return apiResponse{Status: resp.StatusCode, Raw: raw, Body: body, Header: resp.Header}
}
func TestWebMemberFinanceImport(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	trip, self, _ := financeStart(t, f)
	path := "/trips/" + trip.String()
	template, err := f.services.Ledger.ImportTemplate(context.Background(), f.a, trip)
	if err != nil {
		t.Fatal(err)
	}
	file := mfWorkbook(t, template, "custom")
	preview := func() finance.LedgerImportPreview {
		r := mfUpload(t, f, path+"/ledger-import-preview", file, nil)
		expectStatus(t, r, 200, "")
		var b struct {
			Data finance.LedgerImportPreview `json:"data"`
		}
		if err := json.Unmarshal(r.Raw, &b); err != nil {
			t.Fatal(err)
		}
		if b.Data.ValidCount != 1 || len(b.Data.ScopeRevisions) != 1 {
			t.Fatalf("preview: %s", r.Raw)
		}
		return b.Data
	}
	first := preview()
	commitPath := path + "/ledger-import?preview_digest=" + first.Digest
	expectStatus(t, mfUpload(t, f, commitPath, file, f.authHeaders(nil)), 428, "COLLECTION_BASE_REQUIRED")
	financeApplied(t, f.push(replaceMembers(f, trip, memberInput(self, "After", "100"))).Results[0])
	expectStatus(t, mfUpload(t, f, commitPath, file, f.authHeaders(map[string]string{"X-Collection-Guards": mfHeader(t, first.ScopeRevisions)})), 412, "COLLECTION_CONFLICT")
	latest := preview()
	expectStatus(t, mfUpload(t, f, commitPath, file, f.authHeaders(map[string]string{"X-Collection-Guards": mfHeader(t, latest.ScopeRevisions)})), 409, "IMPORT_PREVIEW_CHANGED")
	financeCheck(f, `SELECT NOT EXISTS(SELECT 1 FROM ledger_entries WHERE trip_id=$1) AND (SELECT currency_locked_at IS NULL FROM trips WHERE id=$1)`, trip)
	commitPath = path + "/ledger-import?preview_digest=" + latest.Digest
	headers := f.authHeaders(map[string]string{"X-Collection-Guards": mfHeader(t, latest.ScopeRevisions)})
	var seq int64
	if err := f.pool.QueryRow(context.Background(), `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, f.owner).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	f.sql(`CREATE FUNCTION h07_mf_import_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type='ledger_entry.import' THEN RAISE EXCEPTION 'import receipt failure'; END IF;RETURN NEW; END $$;CREATE TRIGGER h07_mf_import_fail BEFORE INSERT ON mutation_receipts FOR EACH ROW EXECUTE FUNCTION h07_mf_import_fail()`)
	t.Cleanup(func() {
		f.sql(`DROP TRIGGER IF EXISTS h07_mf_import_fail ON mutation_receipts;DROP FUNCTION IF EXISTS h07_mf_import_fail()`)
	})
	failed := mfUpload(t, f, commitPath, file, headers)
	expectStatus(t, failed, 500, "INTERNAL_ERROR")
	financeCheck(f, `SELECT NOT EXISTS(SELECT 1 FROM ledger_entries WHERE trip_id=$1) AND (SELECT currency_locked_at IS NULL FROM trips WHERE id=$1) AND (SELECT last_seq=$3 FROM account_sync_state WHERE account_id=$2) AND NOT EXISTS(SELECT 1 FROM mutation_receipts WHERE operation_id=$4) AND NOT EXISTS(SELECT 1 FROM sync_changes WHERE batch_id=$4)`, trip, f.owner, seq, headers["Idempotency-Key"])
	f.sql(`DROP TRIGGER h07_mf_import_fail ON mutation_receipts;DROP FUNCTION h07_mf_import_fail()`)
	result := mfUpload(t, f, commitPath, file, headers)
	expectStatus(t, result, 201, "")
	if !reflect.DeepEqual(mfResult(t, result).ScopeRevisions, latest.ScopeRevisions) {
		t.Fatal("import facts missing")
	}
	financeApplied(t, f.push(replaceMembers(f, trip, memberInput(self, "Later", "100"))).Results[0])
	replay := mfUpload(t, f, commitPath, file, headers)
	expectStatus(t, replay, 201, "")
	if !mfResult(t, replay).Replayed || !reflect.DeepEqual(mfResult(t, result).ScopeRevisions, mfResult(t, replay).ScopeRevisions) {
		t.Fatal("import replay drift")
	}
	financeCheck(f, `SELECT count(*)=1 FROM ledger_entries WHERE trip_id=$1`, trip)
}

// Pause immediately after the trip query establishes a PostgreSQL snapshot.
// The mutation commits on another pool before the remaining reads proceed.
type mfTraceKey struct{}
type mfSnapshotGate struct {
	once             sync.Once
	entered, release chan struct{}
}

func (g *mfSnapshotGate) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, mfTraceKey{}, strings.Contains(d.SQL, "-- name: GetTripContentInfo") || strings.Contains(d.SQL, "-- name: GetLedgerTripInfo"))
}
func (g *mfSnapshotGate) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if yes, _ := ctx.Value(mfTraceKey{}).(bool); yes {
		g.once.Do(func() {
			close(g.entered)
			select {
			case <-g.release:
			case <-ctx.Done():
			}
		})
	}
}
func TestWebMemberFinanceRepeatableRead(t *testing.T) {
	for _, kind := range []string{"members", "import"} {
		t.Run(kind, func(t *testing.T) {
			f := newPushFixture(t)
			enablePushTestAccount(f)
			trip, _, _ := financeStart(t, f)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			normal := financepg.NewLedgerReader(f.pool)
			baseline, err := normal.ReadImportSnapshot(ctx, f.owner, trip)
			if err != nil {
				t.Fatal(err)
			}
			template, err := f.services.Ledger.ImportTemplate(ctx, f.a, trip)
			if err != nil {
				t.Fatal(err)
			}
			file := mfWorkbook(t, template, "custom")
			before, err := f.services.Ledger.PreviewImport(ctx, f.a, trip, file)
			if err != nil {
				t.Fatal(err)
			}
			gate := &mfSnapshotGate{entered: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			defer release.Do(func() { close(gate.release) })
			config, err := pgxpool.ParseConfig(f.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			config.ConnConfig.Tracer = gate
			pool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			done := make(chan error, 1)
			var members member.ListBaseline
			var preview finance.LedgerImportPreview
			go func() {
				var err error
				if kind == "members" {
					members, err = travelpg.NewMemberReader(pool).ListWithBaseline(ctx, f.owner, trip)
				} else {
					svc := finance.NewLedgerService(nil, financepg.NewLedgerReader(pool), nil, clock.Real{})
					preview, err = svc.PreviewImport(ctx, f.a, trip, file)
				}
				done <- err
			}()
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal("snapshot query not reached")
			}
			f.sql(`UPDATE trip_members SET name='Changed',version=version+1 WHERE trip_id=$1`, trip)
			f.sql(`UPDATE expense_categories SET name=name||' changed',version=version+1 WHERE account_id=$1`, f.owner)
			f.sql(`UPDATE trips SET currency_code='USD',version=version+1 WHERE id=$1`, trip)
			f.sql(`UPDATE account_sync_state SET sync_epoch=$2 WHERE account_id=$1`, f.owner, uuid.New())
			release.Do(func() { close(gate.release) })
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if kind == "members" {
				if !reflect.DeepEqual(members.Members, baseline.Members) || !reflect.DeepEqual(members.ScopeRevisions, baseline.ScopeRevisions) {
					t.Fatalf("torn member snapshot: %+v", members)
				}
			} else if !reflect.DeepEqual(preview, before) {
				t.Fatalf("torn import snapshot: %+v vs %+v", preview, before)
			}
			current, err := normal.ReadImportSnapshot(ctx, f.owner, trip)
			if err != nil {
				t.Fatal(err)
			}
			if current.Trip.CurrencyCode != "USD" || reflect.DeepEqual(current.ScopeRevisions, baseline.ScopeRevisions) {
				t.Fatal("concurrent changes not visible after transaction")
			}
		})
	}
}

func TestWebMemberFinanceReadBoundaries(t *testing.T) {
	f := newPushFixture(t)
	enablePushTestAccount(f)
	trip, _, _ := financeStart(t, f)
	ctx := context.Background()
	mr := travelpg.NewMemberReader(f.pool)
	fr := financepg.NewLedgerReader(f.pool)
	for _, id := range []uuid.UUID{uuid.New(), trip} {
		owner := f.owner
		if id == trip {
			owner = uuid.New()
		}
		_, err := mr.ListWithBaseline(ctx, owner, id)
		policyError(t, err, 404, "RESOURCE_NOT_FOUND")
		_, err = fr.ReadImportSnapshot(ctx, owner, id)
		policyError(t, err, 404, "RESOURCE_NOT_FOUND")
	}
	f.sql(`UPDATE trips SET deleted_at=now(),purge_after_at=now()+interval '30 days' WHERE id=$1`, trip)
	_, err := fr.ReadImportSnapshot(ctx, f.owner, trip)
	policyError(t, err, 410, "TRIP_DELETED")
	f.sql(`UPDATE trips SET deleted_at=NULL,purge_after_at=NULL WHERE id=$1`, trip)
	f.sql(`DELETE FROM trip_members WHERE trip_id=$1`, trip)
	empty, err := mr.ListWithBaseline(ctx, f.owner, trip)
	if err != nil || empty.Members == nil || len(empty.Members) != 0 || len(empty.ScopeRevisions) != 1 {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	snapshot, err := fr.ReadImportSnapshot(ctx, f.owner, trip)
	if err != nil || snapshot.Members == nil || !reflect.DeepEqual(snapshot.ScopeRevisions, empty.ScopeRevisions) {
		t.Fatalf("empty import: %+v %v", snapshot, err)
	}
	f.sql(`DELETE FROM account_sync_state WHERE account_id=$1`, f.owner)
	_, err = mr.ListWithBaseline(ctx, f.owner, trip)
	policyError(t, err, 503, "DEPENDENCY_UNAVAILABLE")
	_, err = fr.ReadImportSnapshot(ctx, f.owner, trip)
	policyError(t, err, 503, "DEPENDENCY_UNAVAILABLE")
}
