package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	syncpg "tripfolio/server/internal/adapters/postgres/sync"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/clock"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/transport/httpapi"
)

func TestHTTPSyncReadProtocol(t *testing.T) {
	u, err := url.Parse(testDatabaseURL(t))
	if err != nil || u.Path != "/h07_sync_test" {
		t.Fatal("sync verification requires the dedicated h07_sync_test database")
	}
	f := newAPIFixture(t)
	ctx := context.Background()
	keys, err := security.ParseKeyring("sync-test=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	if err != nil {
		t.Fatal(err)
	}
	reader := syncpg.NewStore(f.pool)
	svc := syncmodule.NewService(reader, security.NewCursorCodec(keys), clock.Real{})
	s := f.services
	f.server = httptest.NewServer(httpapi.NewRouter(httpapi.Deps{
		Logger: quietLogger(), CORSOrigins: []string{f.origin},
		Identity: s.Identity, Sessions: s.Sessions, Profile: s.Profile, Trips: s.Trips,
		Categories: s.Categories, Members: s.Members, Sync: svc,
	}))
	t.Cleanup(f.server.Close)
	email, password := uniqueEmail(), "correct horse battery"
	reg := f.registerWeb(email, password)
	accountID := uuid.MustParse(reg.data()["account"].(map[string]any)["id"].(string))
	webToken := reg.data()["access_token"].(string)
	login := f.do(request{method: http.MethodPost, path: "/auth/login", body: map[string]any{
		"email": email, "password": password,
		"client": map[string]any{"kind": "harmony", "device_id": uuid.NewString(), "device_name": "H07 integration"},
	}})
	expectStatus(t, login, http.StatusOK, "")
	token := login.data()["access_token"].(string)
	status := f.do(request{method: http.MethodGet, path: "/sync/status", token: token})
	expectStatus(t, status, http.StatusOK, "")
	if status.Body["recommended_protocol_version"] != float64(2) || status.Body["next_cursor"] != nil {
		t.Fatalf("protocol discovery must not manufacture a checkpoint: %s", status.Raw)
	}
	var epoch, sessionID uuid.UUID
	var baseline int64
	if err := f.pool.QueryRow(ctx, `SELECT sync_epoch,last_seq FROM account_sync_state WHERE account_id=$1`, accountID).Scan(&epoch, &baseline); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT id FROM account_sessions WHERE account_id=$1 AND client_kind='harmony'`, accountID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := svc.BaselineCursor(accountID, epoch, baseline)
	if err != nil {
		t.Fatal(err)
	}
	get := func(cursor string, limit int) apiResponse {
		return f.do(request{method: http.MethodGet, path: "/sync/changes?cursor=" + url.QueryEscape(cursor) + "&limit=" + strconv.Itoa(limit), token: token, headers: map[string]string{"X-Tripfolio-Sync-Version": "2"}})
	}
	decode := func(r apiResponse) syncmodule.Page {
		t.Helper()
		expectStatus(t, r, http.StatusOK, "")
		var p syncmodule.Page
		if err := json.Unmarshal(r.Raw, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// A real Web write emits the associated trip/member batch. Limit 1 splits it.
	first := f.createTrip(webToken, map[string]any{"name": "同步第一页", "start_date": "2026-10-01", "end_date": "2026-10-02"})
	p := decode(get(checkpoint, 1))
	if len(p.Changes) != 1 || !p.HasMore || p.Changes[0].BatchEndSeq == p.Changes[0].Seq {
		t.Fatalf("expected a split associated batch: %+v", p)
	}
	var high int64
	if err := f.pool.QueryRow(ctx, `SELECT last_seq FROM account_sync_state WHERE account_id=$1`, accountID).Scan(&high); err != nil {
		t.Fatal(err)
	}
	second := f.createTrip(webToken, map[string]any{"name": "分页之后的新写入", "start_date": "2026-11-01", "end_date": "2026-11-02"})
	changes := append([]syncmodule.Change{}, p.Changes...)
	for n := 0; p.HasMore && n < 20; n++ {
		p = decode(get(p.NextCursor, 1))
		changes = append(changes, p.Changes...)
	}
	if p.HasMore || int64(len(changes)) != high-baseline {
		t.Fatalf("fixed high water page count: %+v", changes)
	}
	for i, c := range changes {
		if c.Seq != strconv.FormatInt(baseline+int64(i)+1, 10) || c.TripID == nil || c.TripID.String() != first["id"] || c.SchemaVersion != 2 {
			t.Fatalf("foreign/new data or lost sequence on fixed-H page: %+v", c)
		}
	}
	latest := decode(get(p.NextCursor, 500))
	if latest.HasMore || len(latest.Changes) < 2 || latest.Changes[0].TripID.String() != second["id"] {
		t.Fatalf("new checkpoint did not pick up the later Web batch: %+v", latest)
	}
	empty := decode(get(latest.NextCursor, 500))
	if empty.HasMore || len(empty.Changes) != 0 || empty.NextCursor == "" {
		t.Fatalf("empty incremental poll: %+v", empty)
	}

	// Capture a repeatable-read view, commit a concurrent write, and ensure its
	// high water and records cannot leak into that already established view.
	a := actor.Actor{AccountID: accountID, SessionID: sessionID, ClientKind: actor.ClientHarmony}
	if err := reader.Read(ctx, a, func(v syncmodule.View) error {
		before, err := v.State(ctx)
		if err != nil {
			return err
		}
		f.createTrip(webToken, map[string]any{"name": "并发视图", "start_date": "2026-12-01", "end_date": "2026-12-02"})
		after, err := v.State(ctx)
		if err != nil {
			return err
		}
		rows, err := v.Changes(ctx, before.LastSeq, before.LastSeq+100, 500)
		if err == nil && (after.LastSeq != before.LastSeq || len(rows) != 0) {
			t.Error("repeatable read observed a later transaction")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}

	expectStatus(t, f.do(request{method: http.MethodGet, path: "/sync/status", token: webToken}), http.StatusForbidden, "NATIVE_SESSION_REQUIRED")
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/sync/status", token: token, headers: map[string]string{"X-Tripfolio-Share-Token": "not-a-session"}}), http.StatusForbidden, "NATIVE_SESSION_REQUIRED")
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/sync/changes?cursor=" + checkpoint, token: token}), http.StatusUpgradeRequired, "SYNC_PROTOCOL_UNSUPPORTED")
	expectStatus(t, get(checkpoint, 0), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expectStatus(t, get("invalid", 1), http.StatusBadRequest, "INVALID_CURSOR")
	foreign, _ := svc.BaselineCursor(uuid.New(), epoch, baseline)
	expectStatus(t, get(foreign, 1), http.StatusBadRequest, "INVALID_CURSOR")
	if _, err := f.pool.Exec(ctx, `UPDATE account_sync_state SET retained_after_seq=$2 WHERE account_id=$1`, accountID, baseline+1); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, get(checkpoint, 1), http.StatusGone, "CURSOR_EXPIRED")
	if _, err := f.pool.Exec(ctx, `UPDATE account_sync_state SET sync_epoch=gen_random_uuid() WHERE account_id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, get(checkpoint, 1), http.StatusConflict, "SYNC_EPOCH_MISMATCH")
	if _, err := f.pool.Exec(ctx, `UPDATE account_sessions SET revoked_at=clock_timestamp() WHERE id=$1`, sessionID); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/sync/status", token: token}), http.StatusUnauthorized, "SESSION_EXPIRED")
}
