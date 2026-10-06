package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/bootstrap"
	"tripfolio/server/internal/config"
	syncmodule "tripfolio/server/internal/modules/sync"
)

// 此测试必须显式分配独立测试库和回环监听端口；不会自行选择已有服务或业务数据库。
func TestRunServeSyncRuntime(t *testing.T) {
	rawURL := testDatabaseURL(t)
	database, err := url.Parse(rawURL)
	if err != nil || (database.Path != "/h07_sync_test" && database.Path != "/tripfolio_test") {
		t.Fatal("runtime sync requires a dedicated test database")
	}
	addr := os.Getenv("TRIPFOLIO_TEST_RUNTIME_HTTP_ADDR")
	if addr == "" {
		t.Skip("TRIPFOLIO_TEST_RUNTIME_HTTP_ADDR 未设置，跳过真实RunServe测试")
	}
	host, port, err := net.SplitHostPort(addr)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || portErr != nil || portNumber < 1 || portNumber > 65535 {
		t.Fatal("runtime HTTP address must be an explicit loopback host and port")
	}
	if conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("runtime HTTP port is already in use")
	}
	// 既有fixture仅用于测试账号/密码与旅行数据准备。被测请求全部走下面真实RunServe。
	f := newAPIFixture(t)
	email, secondEmail, password := uniqueEmail(), uniqueEmail(), "correct horse battery"
	registered := f.registerWeb(email, password)
	second := f.registerWeb(secondEmail, password)
	owner := uuid.MustParse(registered.data()["account"].(map[string]any)["id"].(string))
	secondOwner := uuid.MustParse(second.data()["account"].(map[string]any)["id"].(string))
	selected := uuid.MustParse(f.createTrip(registered.data()["access_token"].(string), map[string]any{
		"name": "runtime baseline", "start_date": "2026-10-01", "end_date": "2026-10-04",
	})["id"].(string))
	f.server.Close()

	env := map[string]string{"TRIPFOLIO_ENV": "test", "TRIPFOLIO_DATABASE_URL": rawURL,
		"TRIPFOLIO_KEYRING":     "runtime-test=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("r", 32))),
		"TRIPFOLIO_MAIL_DRIVER": "disabled"}
	cfg, err := config.Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	cfg.HTTPAddr, cfg.DBMaxConns, cfg.WorkerMaxJobs = addr, 8, 2
	cfg.AutoMigrate, cfg.CookieSecure = false, false
	cfg.CORSOrigins = []string{f.origin}
	cfg.StartupDBTimeout, cfg.StartupDBRetryInterval, cfg.ShutdownTimeout = 5*time.Second, 100*time.Millisecond, 5*time.Second
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bootstrap.RunServe(ctx, cfg, quietLogger()) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("RunServe failed: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("RunServe did not stop HTTP and worker within deadline")
		}
		if conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
			conn.Close()
			t.Error("HTTP listener remained open after RunServe stopped")
		}
	}
	t.Cleanup(stop)
	client := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	baseURL := "http://" + addr
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case err := <-done:
			stopped = true
			cancel()
			t.Fatalf("RunServe exited before readiness: %v", err)
		default:
		}
		resp, err := client.Get(baseURL + "/api/v1/metadata")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("real HTTP service did not become ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
	do := func(method, path, token string, body any, headers map[string]string) apiResponse {
		t.Helper()
		var input bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&input).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequest(method, baseURL+"/api/v1"+path, &input)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", f.origin)
		req.Header.Set("X-Tripfolio-Sync-Version", "2")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if err != nil {
			t.Fatal(err)
		}
		out := apiResponse{Status: resp.StatusCode, Raw: raw, Header: resp.Header}
		if err := json.Unmarshal(raw, &out.Body); err != nil {
			t.Fatalf("invalid HTTP JSON (%d): %v", resp.StatusCode, err)
		}
		return out
	}
	login := func(email string) string {
		t.Helper()
		result := do(http.MethodPost, "/auth/login", "", map[string]any{"email": email, "password": password,
			"client": map[string]any{"kind": "harmony", "device_id": uuid.NewString(), "device_name": "runtime test"}}, nil)
		expectStatus(t, result, 200, "")
		return result.data()["access_token"].(string)
	}
	token, secondToken := login(email), login(secondEmail)
	status := do(http.MethodGet, "/sync/status", token, nil, nil)
	expectStatus(t, status, 200, "")
	epoch := uuid.MustParse(status.Body["sync_epoch"].(string))
	secondStatus := do(http.MethodGet, "/sync/status", secondToken, nil, nil)
	expectStatus(t, secondStatus, 200, "")
	secondEpoch := uuid.MustParse(secondStatus.Body["sync_epoch"].(string))
	type writeState struct{ seq, receipts, trips int64 }
	state := func(id uuid.UUID) writeState {
		t.Helper()
		var result writeState
		if err := f.pool.QueryRow(context.Background(), `SELECT last_seq,
   (SELECT count(*) FROM mutation_receipts WHERE account_id=$1),
   (SELECT count(*) FROM trips WHERE account_id=$1) FROM account_sync_state WHERE account_id=$1`, id).Scan(&result.seq, &result.receipts, &result.trips); err != nil {
			t.Fatal(err)
		}
		return result
	}
	assertNoPolicy := func(id uuid.UUID) {
		t.Helper()
		var n int
		if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM account_sync_capabilities WHERE account_id=$1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("runtime construction/read/worker must not activate an account")
		}
	}
	newPush, _ := createPush("real runtime push")
	input := syncmodule.PushInput{SyncEpoch: epoch, ClientID: uuid.New(), Operations: []syncmodule.Operation{newPush}}
	before := state(owner)
	assertNoPolicy(owner)
	expectStatus(t, do(http.MethodPost, "/sync/push", token, input, nil), 409, "SYNC_NOT_READY")
	if got := state(owner); got != before {
		t.Fatalf("unactivated push wrote data: before=%+v after=%+v", before, got)
	}
	assertNoPolicy(owner)

	key := uuid.NewString()
	snapshotInput := syncmodule.SnapshotInput{SyncEpoch: epoch, Purpose: "baseline", SelectedTripIDs: []uuid.UUID{selected}}
	created := do(http.MethodPost, "/sync/snapshots", token, snapshotInput, map[string]string{"Idempotency-Key": key})
	expectStatus(t, created, 202, "")
	var wrapped struct {
		Data syncmodule.Snapshot `json:"data"`
	}
	if err := json.Unmarshal(created.Raw, &wrapped); err != nil {
		t.Fatal(err)
	}
	snapshotID := wrapped.Data.ID
	duplicate := do(http.MethodPost, "/sync/snapshots", token, snapshotInput, map[string]string{"Idempotency-Key": key})
	expectStatus(t, duplicate, 202, "")
	if duplicate.data()["id"] != snapshotID.String() {
		t.Fatal("snapshot replay changed ID")
	}
	deadline = time.Now().Add(30 * time.Second)
	for {
		response := do(http.MethodGet, "/sync/snapshots/"+snapshotID.String(), token, nil, nil)
		expectStatus(t, response, 200, "")
		var snapshot syncmodule.Snapshot
		if err := json.Unmarshal(response.Raw, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.ID != snapshotID || snapshot.SyncEpoch != epoch {
			t.Fatal("snapshot metadata identity changed")
		}
		if snapshot.Status == "ready" {
			break
		}
		if snapshot.Status == "failed" || snapshot.Status == "expired" || snapshot.Status == "invalidated" {
			t.Fatalf("snapshot did not publish: %+v", snapshot)
		}
		if time.Now().After(deadline) {
			t.Fatal("real River worker did not publish snapshot")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// ready来自事务发布；继续确认真实队列回执完成，而不是测试直接调用BuildSnapshot。
	deadline = time.Now().Add(5 * time.Second)
	for {
		var jobs, completed int
		if err := f.pool.QueryRow(context.Background(), `SELECT count(*),count(*) FILTER(WHERE state='completed' AND attempt>0)
   FROM river_job WHERE kind='sync_snapshot' AND args->>'id'=$1`, snapshotID.String()).Scan(&jobs, &completed); err != nil {
			t.Fatal(err)
		}
		if jobs != 1 {
			t.Fatalf("expected one queued snapshot job, got %d", jobs)
		}
		if completed == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("River did not confirm snapshot job completion")
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertNoPolicy(owner)
	var page syncmodule.SnapshotPage
	cursor := ""
	ordinal := 0
	sawTrip := false
	for n := 0; n < 100; n++ {
		response := do(http.MethodGet, "/sync/snapshots/"+snapshotID.String()+"/items?limit=2&cursor="+url.QueryEscape(cursor), token, nil, nil)
		expectStatus(t, response, 200, "")
		page = syncmodule.SnapshotPage{}
		if err := json.Unmarshal(response.Raw, &page); err != nil {
			t.Fatal(err)
		}
		if page.SnapshotID != snapshotID || page.SyncEpoch != epoch {
			t.Fatal("snapshot page identity changed")
		}
		for _, item := range page.Items {
			ordinal++
			if item.Ordinal != strconv.Itoa(ordinal) {
				t.Fatal("snapshot ordinal is not continuous")
			}
			if item.EntityType == "trip" && item.EntityID == selected {
				sawTrip = true
			}
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == nil || page.BaselineCursor != nil {
			t.Fatal("invalid intermediate snapshot cursor")
		}
		cursor = *page.NextCursor
	}
	if page.HasMore || page.BaselineCursor == nil || page.ItemCount != strconv.Itoa(ordinal) || !sawTrip {
		t.Fatalf("incomplete real runtime snapshot: %+v", page)
	}
	baselineCursor := *page.BaselineCursor
	expectStatus(t, do(http.MethodGet, "/sync/changes?cursor="+url.QueryEscape(baselineCursor), token, nil, nil), 200, "")
	assertNoPolicy(owner)
	// 仅本测试账号的显式前置，用于验证已准入运行链；不代表受控CLI激活已经验收。
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO account_sync_capabilities(account_id,collection_guards_required,v2_enabled_epoch,enabled_at) VALUES($1,true,$2,now())`, owner, epoch); err != nil {
		t.Fatal(err)
	}
	before = state(owner)
	applied := do(http.MethodPost, "/sync/push", token, input, nil)
	expectStatus(t, applied, 200, "")
	var first syncmodule.PushOutput
	if err := json.Unmarshal(applied.Raw, &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Results) != 1 || first.Results[0].Status != "applied" || first.Results[0].Result == nil {
		t.Fatalf("runtime push not applied: %+v", first)
	}
	after := state(owner)
	if after.receipts != before.receipts+1 || after.trips != before.trips+1 || after.seq <= before.seq {
		t.Fatalf("runtime push effects invalid: %+v -> %+v", before, after)
	}
	repeated := do(http.MethodPost, "/sync/push", token, input, nil)
	expectStatus(t, repeated, 200, "")
	var replay syncmodule.PushOutput
	if err := json.Unmarshal(repeated.Raw, &replay); err != nil {
		t.Fatal(err)
	}
	if len(replay.Results) != 1 || replay.Results[0].Status != "replayed" || !reflect.DeepEqual(first.Results[0].Result, replay.Results[0].Result) {
		t.Fatal("runtime replay did not preserve original facts")
	}
	if state(owner) != after {
		t.Fatal("runtime replay wrote data again")
	}
	changed := do(http.MethodGet, "/sync/changes?cursor="+url.QueryEscape(baselineCursor), token, nil, nil)
	expectStatus(t, changed, 200, "")
	var changes syncmodule.Page
	if err := json.Unmarshal(changed.Raw, &changes); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, change := range changes.Changes {
		if change.EntityType == "trip" && change.EntityID == *newPush.EntityID {
			found = true
		}
	}
	if !found {
		t.Fatal("runtime changes did not include real push")
	}
	secondOp, _ := createPush("still not enabled")
	secondBefore := state(secondOwner)
	expectStatus(t, do(http.MethodPost, "/sync/push", secondToken, syncmodule.PushInput{SyncEpoch: secondEpoch, ClientID: uuid.New(), Operations: []syncmodule.Operation{secondOp}}, nil), 409, "SYNC_NOT_READY")
	if state(secondOwner) != secondBefore {
		t.Fatal("other unactivated account wrote data")
	}
	assertNoPolicy(secondOwner)
	stop()
	var running int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job WHERE kind='sync_snapshot' AND args->>'id'=$1 AND state='running'`, snapshotID.String()).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if running != 0 {
		t.Fatal("snapshot job remained running after shutdown")
	}
}
