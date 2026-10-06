package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	syncmodule "tripfolio/server/internal/modules/sync"
)

// Requires an explicitly approved final cmd artifact and reviewed evidence
// beside it. This test never manufactures acceptance reports or a verifier.
func TestSyncActivationCLI(t *testing.T) {
	original := os.Getenv("TRIPFOLIO_TEST_ACTIVATION_BINARY")
	addr := os.Getenv("TRIPFOLIO_TEST_ACTIVATION_HTTP_ADDR")
	if original == "" || addr == "" {
		t.Skip("explicit final cmd artifact and isolated HTTP address required")
	}
	if !filepath.IsAbs(original) {
		t.Fatal("final binary must be absolute")
	}
	rawURL := testDatabaseURL(t)
	target, err := url.Parse(rawURL)
	if err != nil || (target.Path != "/h07_sync_test" && target.Path != "/tripfolio_test") {
		t.Fatal("dedicated database required")
	}
	host, port, err := net.SplitHostPort(addr)
	number, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || number < 1 || number > 65535 || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("explicit loopback HTTP address required")
	}
	if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("HTTP port occupied")
	}
	work := t.TempDir()
	binaryPath := filepath.Join(work, filepath.Base(original))
	copyFile := func(src, dst string, limit int64) {
		t.Helper()
		st, err := os.Lstat(src)
		if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
			t.Fatal("invalid artifact/evidence file")
		}
		in, err := os.Open(src)
		if err != nil {
			t.Fatal("artifact open failed")
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			t.Fatal(err)
		}
		_, copyErr := io.Copy(out, io.LimitReader(in, limit+1))
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatal("artifact copy failed")
		}
	}
	copyFile(original, binaryPath, 512<<20)
	f := newAPIFixture(t)
	email, password := uniqueEmail(), "correct horse battery"
	registered := f.registerWeb(email, password)
	owner := uuid.MustParse(registered.data()["account"].(map[string]any)["id"].(string))
	tripID := uuid.MustParse(f.createTrip(registered.data()["access_token"].(string), map[string]any{"name": "activation CLI", "start_date": "2026-10-01", "end_date": "2026-10-04"})["id"].(string))
	f.server.Close()
	baseEnv := []string{}
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR"} {
		if value := os.Getenv(key); value != "" {
			baseEnv = append(baseEnv, key+"="+value)
		}
	}
	keyring := "activation-cli-test=" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'c'}, 32))
	env := append(append([]string{}, baseEnv...), "TRIPFOLIO_ENV=test", "TRIPFOLIO_DATABASE_URL="+rawURL, "TRIPFOLIO_KEYRING="+keyring, "TRIPFOLIO_HTTP_ADDR="+addr, "TRIPFOLIO_MAIL_DRIVER=disabled", "TRIPFOLIO_CORS_ORIGINS="+f.origin, "TRIPFOLIO_PASSWORD_HASH_CONCURRENCY=1")
	run := func(proof string, args ...string) ([]byte, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binaryPath, args...)
		cmd.Dir = work
		cmd.Env = env
		cmd.Stdin = strings.NewReader(proof)
		output, err := cmd.CombinedOutput()
		for _, secret := range []string{proof, rawURL, keyring} {
			if secret != "" && bytes.Contains(output, []byte(secret)) {
				t.Fatal("CLI output disclosed sensitive input")
			}
		}
		return output, err
	}
	// release-info must work without DB/config and identify the actual cmd bytes.
	infoCmd := exec.Command(binaryPath, "sync", "release-info")
	infoCmd.Dir = work
	infoCmd.Env = baseEnv
	infoRaw, err := infoCmd.Output()
	if err != nil {
		t.Fatal("final cmd release-info failed (actual embedded Web is required)")
	}
	var info struct {
		ExecutableSHA256 string `json:"executable_sha256"`
		WebTreeSHA256    string `json:"web_tree_sha256"`
	}
	if json.Unmarshal(infoRaw, &info) != nil || len(info.WebTreeSHA256) != 64 {
		t.Fatal("invalid actual artifact facts")
	}
	file, err := os.Open(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil || hex.EncodeToString(hash.Sum(nil)) != info.ExecutableSHA256 {
		t.Fatal("not the final executable fingerprint")
	}
	ctx, stop := context.WithCancel(context.Background())
	serve := exec.CommandContext(ctx, binaryPath, "serve")
	serve.Dir = work
	serve.Env = env
	serverLog, err := os.Create(filepath.Join(work, "serve.log"))
	if err != nil {
		t.Fatal(err)
	}
	serve.Stdout = serverLog
	serve.Stderr = serverLog
	if err = serve.Start(); err != nil {
		serverLog.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serve.Wait() }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("external serve did not terminate")
		}
		serverLog.Close()
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			t.Error("external HTTP remained open")
		}
	})
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := client.Get("http://" + addr + "/health/ready")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("external serve readiness timeout")
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.server.URL = "http://" + addr
	f.server.Client().Timeout = 5 * time.Second
	login := f.do(request{method: "POST", path: "/auth/login", body: map[string]any{"email": email, "password": password, "client": map[string]any{"kind": "harmony", "device_id": uuid.NewString(), "device_name": "CLI integration"}}})
	expectStatus(t, login, 200, "")
	token := login.data()["access_token"].(string)
	status := f.do(request{method: "GET", path: "/sync/status", token: token, headers: map[string]string{"X-Tripfolio-Sync-Version": "2"}})
	expectStatus(t, status, 200, "")
	epoch := uuid.MustParse(status.Body["sync_epoch"].(string))
	native := func(method, path string, body any) apiResponse {
		return f.do(request{method: method, path: path, token: token, body: body, headers: map[string]string{"X-Tripfolio-Sync-Version": "2", "Idempotency-Key": uuid.NewString()}})
	}
	operation, _ := createPush("CLI gated write")
	input := syncmodule.PushInput{SyncEpoch: epoch, ClientID: uuid.New(), Operations: []syncmodule.Operation{operation}}
	expectStatus(t, native("POST", "/sync/push", input), 409, "SYNC_NOT_READY")
	created := native("POST", "/sync/snapshots", syncmodule.SnapshotInput{SyncEpoch: epoch, Purpose: "baseline", SelectedTripIDs: []uuid.UUID{tripID}})
	expectStatus(t, created, 202, "")
	id := uuid.MustParse(created.data()["id"].(string))
	deadline = time.Now().Add(30 * time.Second)
	for {
		response := native("GET", "/sync/snapshots/"+id.String(), nil)
		expectStatus(t, response, 200, "")
		var meta syncmodule.Snapshot
		if json.Unmarshal(response.Raw, &meta) != nil {
			t.Fatal("snapshot response")
		}
		if meta.Status == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real worker did not publish baseline")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var completed int
	deadline = time.Now().Add(5 * time.Second)
	for {
		if err = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job WHERE kind='sync_snapshot' AND args->>'id'=$1 AND state='completed' AND attempt>0`, id.String()).Scan(&completed); err != nil {
			t.Fatal("cannot verify real River job")
		}
		if completed == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real River job not completed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var page syncmodule.SnapshotPage
	cursor := ""
	ordinal := 0
	for n := 0; n < 100; n++ {
		response := native("GET", "/sync/snapshots/"+id.String()+"/items?limit=2&cursor="+url.QueryEscape(cursor), nil)
		expectStatus(t, response, 200, "")
		page = syncmodule.SnapshotPage{}
		if json.Unmarshal(response.Raw, &page) != nil {
			t.Fatal("snapshot page")
		}
		for _, item := range page.Items {
			ordinal++
			if item.Ordinal != strconv.Itoa(ordinal) {
				t.Fatal("ordinal gap")
			}
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == nil || page.BaselineCursor != nil {
			t.Fatal("invalid intermediate page")
		}
		cursor = *page.NextCursor
	}
	if page.HasMore || page.BaselineCursor == nil || page.ItemCount != strconv.Itoa(ordinal) {
		t.Fatal("incomplete baseline")
	}
	proof := *page.BaselineCursor
	args := []string{"sync", "activate", "--account", owner.String(), "--snapshot", id.String(), "--proof-file", "-"}
	assertDisabled := func() {
		t.Helper()
		var n int
		if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM account_sync_capabilities WHERE account_id=$1`, owner).Scan(&n); err != nil || n != 0 {
			t.Fatal("rejected CLI wrote capability")
		}
	}
	if _, err = run(proof, args...); err == nil {
		t.Fatal("missing manifest accepted")
	}
	assertDisabled()
	// Only copy operator-supplied, reviewed evidence. No generated passed reports.
	releaseSource := filepath.Join(filepath.Dir(original), "tripfolio.sync-release")
	releaseTarget := filepath.Join(work, "tripfolio.sync-release")
	err = filepath.WalkDir(releaseSource, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fs.ErrInvalid
		}
		rel, err := filepath.Rel(releaseSource, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(releaseTarget, rel)
		if d.IsDir() {
			return os.Mkdir(dest, 0700)
		}
		copyFile(path, dest, 8<<20)
		return nil
	})
	if err != nil {
		t.Fatal("reviewed release evidence required")
	}
	manifestPath := filepath.Join(releaseTarget, "manifest.json")
	accepted, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, []byte(`{"schema":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = run(proof, args...); err == nil {
		t.Fatal("wrong manifest accepted")
	}
	assertDisabled()
	if err = os.WriteFile(manifestPath, accepted, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = run(proof+"!", args...); err == nil {
		t.Fatal("forged proof accepted")
	}
	assertDisabled()
	first, err := run(proof, args...)
	if err != nil {
		t.Fatal("approved final CLI activation failed")
	}
	second, err := run(proof, args...)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("repeated activation changed facts")
	}
	applied := native("POST", "/sync/push", input)
	expectStatus(t, applied, 200, "")
	repeated := native("POST", "/sync/push", input)
	expectStatus(t, repeated, 200, "")
	var initial, replay syncmodule.PushOutput
	if json.Unmarshal(applied.Raw, &initial) != nil || json.Unmarshal(repeated.Raw, &replay) != nil || len(initial.Results) != 1 || len(replay.Results) != 1 || initial.Results[0].Status != "applied" || replay.Results[0].Status != "replayed" || !reflect.DeepEqual(initial.Results[0].Result, replay.Results[0].Result) {
		t.Fatal("CLI enabled write/replay mismatch")
	}
	webLogin := f.do(request{method: "POST", path: "/auth/login", body: map[string]any{"email": email, "password": password, "client": webClientBody()}})
	expectStatus(t, webLogin, 200, "")
	webToken := webLogin.data()["access_token"].(string)
	memberList := f.do(request{method: "GET", path: "/trips/" + tripID.String() + "/members", token: webToken})
	expectStatus(t, memberList, 200, "")
	members, ok := memberList.Body["data"].([]any)
	if !ok || len(members) != 1 {
		t.Fatal("expected complete self-member baseline")
	}
	self := members[0].(map[string]any)
	result := f.do(request{method: "PUT", path: "/trips/" + tripID.String() + "/members", token: webToken, headers: map[string]string{"Idempotency-Key": uuid.NewString(), "X-CSRF-Token": webLogin.data()["csrf_token"].(string)}, cookies: webLogin.Cookies, body: map[string]any{"members": []any{memberInput(uuid.MustParse(self["id"].(string)), "Me", "100")}}})
	expectStatus(t, result, 428, "COLLECTION_BASE_REQUIRED")
	// Test-only restore transition: the old terminal proof must not enable a new epoch.
	if _, err = f.pool.Exec(context.Background(), `UPDATE account_sync_state SET sync_epoch=gen_random_uuid() WHERE account_id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = run(proof, args...); err == nil {
		t.Fatal("old epoch proof accepted")
	}
}
