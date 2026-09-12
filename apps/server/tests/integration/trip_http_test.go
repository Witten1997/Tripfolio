package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func (f *apiFixture) authHeaders(extra map[string]string) map[string]string {
	h := map[string]string{"Idempotency-Key": uuid.NewString()}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

func (f *apiFixture) createTrip(token string, body map[string]any) map[string]any {
	f.t.Helper()
	if _, ok := body["id"]; !ok {
		body["id"] = uuid.NewString()
	}
	res := f.do(request{method: http.MethodPost, path: "/trips", token: token, body: body, headers: f.authHeaders(nil)})
	expectStatus(f.t, res, http.StatusCreated, "")
	d, _ := res.data()["data"].(map[string]any)
	return d
}

func (f *apiFixture) countChanges(accountID, entityType string) (n int, lastKind string, lastVersion int64) {
	f.t.Helper()
	ctx := context.Background()
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM sync_changes WHERE account_id = $1 AND entity_type = $2`, accountID, entityType).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	if n == 0 {
		return 0, "", 0
	}
	if err := f.pool.QueryRow(ctx, `SELECT change_kind, entity_version FROM sync_changes WHERE account_id = $1 AND entity_type = $2 ORDER BY seq DESC LIMIT 1`, accountID, entityType).Scan(&lastKind, &lastVersion); err != nil {
		f.t.Fatal(err)
	}
	return n, lastKind, lastVersion
}

func TestHTTPTripCreateUpdateAndChangeLog(t *testing.T) {
	f := newAPIFixture(t)
	reg := f.registerWeb(uniqueEmail(), "correct horse battery")
	token, _ := reg.data()["access_token"].(string)
	accountID, _ := reg.data()["account"].(map[string]any)["id"].(string)

	// 创建：默认值来自账号与元数据；变更日志一条 upsert，trip_id 等于自身
	tripID := uuid.NewString()
	opKey := uuid.NewString()
	body := map[string]any{"id": tripID, "name": " 东京 ", "start_date": "2026-10-01", "end_date": "2026-10-07", "budget_amount": "5000.5"}
	res := f.do(request{method: http.MethodPost, path: "/trips", token: token, body: body, headers: map[string]string{"Idempotency-Key": opKey}})
	expectStatus(t, res, http.StatusCreated, "")
	created, _ := res.data()["data"].(map[string]any)
	if created["name"] != "东京" || created["timezone"] != "Asia/Shanghai" || created["currency_code"] != "CNY" || created["budget_amount"] != "5000.50" || created["version"] != "1" {
		t.Fatalf("created trip: %v", created)
	}
	if res.data()["replayed"] != false || res.data()["primary"].(map[string]any)["type"] != "trip" {
		t.Fatalf("write result: %v", res.data())
	}
	var tripIDInLog string
	if err := f.pool.QueryRow(context.Background(), `SELECT trip_id::text FROM sync_changes WHERE account_id = $1 AND entity_type = 'trip'`, accountID).Scan(&tripIDInLog); err != nil || tripIDInLog != tripID {
		t.Fatalf("trip change should carry its own trip_id: %q %v", tripIDInLog, err)
	}

	// 同键重放：201 且 replayed=true，日志不增
	replay := f.do(request{method: http.MethodPost, path: "/trips", token: token, body: body, headers: map[string]string{"Idempotency-Key": opKey}})
	expectStatus(t, replay, http.StatusCreated, "")
	if replay.data()["replayed"] != true {
		t.Fatalf("replay: %s", replay.Raw)
	}
	if n, _, _ := f.countChanges(accountID, "trip"); n != 1 {
		t.Fatalf("replay must not add changes, got %d", n)
	}
	// 同键不同内容 → 409；新键同 ID → 409 ID_ALREADY_USED
	body2 := map[string]any{"id": tripID, "name": "大阪", "start_date": "2026-10-01", "end_date": "2026-10-07"}
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/trips", token: token, body: body2, headers: map[string]string{"Idempotency-Key": opKey}}), http.StatusConflict, "IDEMPOTENCY_CONFLICT")
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/trips", token: token, body: body2, headers: f.authHeaders(nil)}), http.StatusConflict, "ID_ALREADY_USED")

	// GET 带 ETag；缺 If-Match 428
	got := f.do(request{method: http.MethodGet, path: "/trips/" + tripID, token: token})
	expectStatus(t, got, http.StatusOK, "")
	if got.Header.Get("ETag") != strongETag1 || got.data()["id"] != tripID {
		t.Fatalf("GET trip: %s ETag=%s", got.Raw, got.Header.Get("ETag"))
	}
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/trips/" + tripID, token: token, body: map[string]any{"name": "x"}, headers: f.authHeaders(nil)}), http.StatusPreconditionRequired, "VERSION_REQUIRED")

	// PATCH 基线 1 改名 → 版本 2；再以基线 1 改备注 → 合并为版本 3 并带警告；以基线 1 改名 → 412 且 conflicting_fields=[name]
	up := f.do(request{method: http.MethodPatch, path: "/trips/" + tripID, token: token, body: map[string]any{"name": "京都"}, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})})
	expectStatus(t, up, http.StatusOK, "")
	if d := up.data()["data"].(map[string]any); d["version"] != "2" || d["name"] != "京都" {
		t.Fatalf("patch: %s", up.Raw)
	}
	merged := f.do(request{method: http.MethodPatch, path: "/trips/" + tripID, token: token, body: map[string]any{"notes": "带雨伞", "budget_amount": nil}, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})})
	expectStatus(t, merged, http.StatusOK, "")
	if d := merged.data()["data"].(map[string]any); d["version"] != "3" || d["notes"] != "带雨伞" || d["budget_amount"] != nil {
		t.Fatalf("merged patch: %s", merged.Raw)
	}
	if w, _ := merged.data()["warnings"].([]any); len(w) != 1 || w[0] != "MERGED_WITH_NEWER_VERSION" {
		t.Fatalf("warnings: %s", merged.Raw)
	}
	conflict := f.do(request{method: http.MethodPatch, path: "/trips/" + tripID, token: token, body: map[string]any{"name": "神户"}, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})})
	expectStatus(t, conflict, http.StatusPreconditionFailed, "VERSION_CONFLICT")
	c, _ := conflict.Body["conflict"].(map[string]any)
	if c["current_version"] != "3" || c["conflicting_fields"].([]any)[0] != "name" || c["current"].(map[string]any)["name"] != "京都" {
		t.Fatalf("conflict payload: %s", conflict.Raw)
	}
	if n, kind, ver := f.countChanges(accountID, "trip"); n != 3 || kind != "upsert" || ver != 3 {
		t.Fatalf("change log after patches: n=%d kind=%s version=%d", n, kind, ver)
	}

	// 校验：日期顺序、非法币种、超精度预算；币种规则
	bad := f.do(request{method: http.MethodPatch, path: "/trips/" + tripID, token: token, body: map[string]any{"end_date": "2026-09-30"}, headers: f.authHeaders(map[string]string{"If-Match": `"3"`})})
	expectStatus(t, bad, http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/trips/" + tripID, token: token, body: map[string]any{"currency_code": "JPY", "budget_amount": "1.5"}, headers: f.authHeaders(map[string]string{"If-Match": `"3"`})}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	cur := f.do(request{method: http.MethodPatch, path: "/trips/" + tripID, token: token, body: map[string]any{"currency_code": "JPY", "budget_amount": "1000"}, headers: f.authHeaders(map[string]string{"If-Match": `"3"`})})
	expectStatus(t, cur, http.StatusOK, "")
	if d := cur.data()["data"].(map[string]any); d["currency_code"] != "JPY" || d["budget_amount"] != "1000" {
		t.Fatalf("currency change: %s", cur.Raw)
	}
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/trips/" + tripID, token: token, body: map[string]any{"currency_code": "USD"}, headers: f.authHeaders(map[string]string{"If-Match": `"4"`})}),
		http.StatusConflict, "CURRENCY_AMOUNTS_EXIST")
	// 有账目时锁定：直接写入 currency_locked_at 模拟切片 4 的账目创建
	if _, err := f.pool.Exec(context.Background(), `UPDATE trips SET currency_locked_at = now() WHERE id = $1`, tripID); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/trips/" + tripID, token: token, body: map[string]any{"currency_code": "USD", "budget_amount": nil}, headers: f.authHeaders(map[string]string{"If-Match": `"4"`})}),
		http.StatusConflict, "CURRENCY_LOCKED")

	// 跨账号：404
	other := f.registerWeb(uniqueEmail(), "correct horse battery")
	otherToken, _ := other.data()["access_token"].(string)
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/trips/" + tripID, token: otherToken}), http.StatusNotFound, "RESOURCE_NOT_FOUND")
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/trips/" + tripID, token: otherToken, body: map[string]any{"name": "x"}, headers: f.authHeaders(map[string]string{"If-Match": `"4"`})}), http.StatusNotFound, "RESOURCE_NOT_FOUND")
	expectStatus(t, f.do(request{method: http.MethodDelete, path: "/trips/" + tripID, token: otherToken, headers: f.authHeaders(map[string]string{"If-Match": `"4"`})}), http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestHTTPTripListRecycleBinAndPurge(t *testing.T) {
	f := newAPIFixture(t)
	reg := f.registerWeb(uniqueEmail(), "correct horse battery")
	token, _ := reg.data()["access_token"].(string)
	accountID, _ := reg.data()["account"].(map[string]any)["id"].(string)

	past := f.createTrip(token, map[string]any{"name": "去年冲绳", "start_date": "2025-12-01", "end_date": "2025-12-05", "destination": "Okinawa"})
	future := f.createTrip(token, map[string]any{"name": "明年元旦", "start_date": "2027-01-01", "end_date": "2027-01-03"})
	far := f.createTrip(token, map[string]any{"name": "远期", "start_date": "2028-05-01", "end_date": "2028-05-03", "timezone": "Asia/Tokyo"})

	// 列表默认按开始日期倒序，带 phase；q 与 phase 筛选；分页游标
	list := f.do(request{method: http.MethodGet, path: "/trips", token: token})
	expectStatus(t, list, http.StatusOK, "")
	items, _ := list.Body["items"].([]any)
	if len(items) != 3 || items[0].(map[string]any)["id"] != far["id"] || items[2].(map[string]any)["phase"] != "ended" || items[0].(map[string]any)["phase"] != "planned" {
		t.Fatalf("list: %s", list.Raw)
	}
	if list.Body["next_cursor"] != nil {
		t.Fatalf("no more pages expected: %s", list.Raw)
	}
	q := f.do(request{method: http.MethodGet, path: "/trips?q=okinawa", token: token})
	if items, _ := q.Body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != past["id"] {
		t.Fatalf("q filter: %s", q.Raw)
	}
	ph := f.do(request{method: http.MethodGet, path: "/trips?phase=ended", token: token})
	if items, _ := ph.Body["items"].([]any); len(items) != 1 {
		t.Fatalf("phase filter: %s", ph.Raw)
	}
	page1 := f.do(request{method: http.MethodGet, path: "/trips?limit=2", token: token})
	expectStatus(t, page1, http.StatusOK, "")
	cursor, _ := page1.Body["next_cursor"].(string)
	if items, _ := page1.Body["items"].([]any); len(items) != 2 || cursor == "" {
		t.Fatalf("page 1: %s", page1.Raw)
	}
	page2 := f.do(request{method: http.MethodGet, path: "/trips?limit=2&cursor=" + cursor, token: token})
	expectStatus(t, page2, http.StatusOK, "")
	if items, _ := page2.Body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != past["id"] || page2.Body["next_cursor"] != nil {
		t.Fatalf("page 2: %s", page2.Raw)
	}
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/trips?limit=2&cursor=" + cursor + "&sort=updated_at_desc", token: token}), http.StatusBadRequest, "INVALID_CURSOR")
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/trips?cursor=garbage", token: token}), http.StatusBadRequest, "INVALID_CURSOR")
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/trips?limit=0", token: token}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/recycle-bin/trips?limit=101", token: token}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 归档：版本 2；archived=true 筛选只含它
	arch := f.do(request{method: http.MethodPost, path: "/trips/" + past["id"].(string) + "/archive", token: token, body: map[string]any{"archived": true}, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})})
	expectStatus(t, arch, http.StatusOK, "")
	if d := arch.data()["data"].(map[string]any); d["archived_at"] == nil || d["version"] != "2" {
		t.Fatalf("archive: %s", arch.Raw)
	}
	onlyArchived := f.do(request{method: http.MethodGet, path: "/trips?archived=true", token: token})
	if items, _ := onlyArchived.Body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != past["id"] {
		t.Fatalf("archived filter: %s", onlyArchived.Raw)
	}

	// 删除到回收站：版本不等 412；正确 → 200，日志 delete；GET 410；列表隐藏；回收站可见
	futureID := future["id"].(string)
	expectStatus(t, f.do(request{method: http.MethodDelete, path: "/trips/" + futureID, token: token, headers: f.authHeaders(map[string]string{"If-Match": `"9"`})}), http.StatusPreconditionFailed, "VERSION_CONFLICT")
	del := f.do(request{method: http.MethodDelete, path: "/trips/" + futureID, token: token, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})})
	expectStatus(t, del, http.StatusOK, "")
	if d := del.data()["data"].(map[string]any); d["deleted_at"] == nil || d["purge_after_at"] == nil || d["version"] != "2" {
		t.Fatalf("trash: %s", del.Raw)
	}
	if _, kind, _ := f.countChanges(accountID, "trip"); kind != "delete" {
		t.Fatalf("last change should be delete, got %s", kind)
	}
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/trips/" + futureID, token: token}), http.StatusGone, "TRIP_DELETED")
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/trips/" + futureID, token: token, body: map[string]any{"name": "x"}, headers: f.authHeaders(map[string]string{"If-Match": `"2"`})}), http.StatusGone, "TRIP_DELETED")
	if items, _ := f.do(request{method: http.MethodGet, path: "/trips", token: token}).Body["items"].([]any); len(items) != 2 {
		t.Fatalf("trashed trip must be hidden from list, got %d", len(items))
	}
	bin := f.do(request{method: http.MethodGet, path: "/recycle-bin/trips", token: token})
	expectStatus(t, bin, http.StatusOK, "")
	if items, _ := bin.Body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != futureID {
		t.Fatalf("recycle bin: %s", bin.Raw)
	}
	one := f.do(request{method: http.MethodGet, path: "/recycle-bin/trips/" + futureID, token: token})
	expectStatus(t, one, http.StatusOK, "")
	if one.Header.Get("ETag") != `"2"` {
		t.Fatalf("trashed ETag: %s", one.Header.Get("ETag"))
	}
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/recycle-bin/trips/" + far["id"].(string), token: token}), http.StatusNotFound, "RESOURCE_NOT_FOUND")

	// 恢复：版本 3，日志 upsert 带 requires_snapshot；之后回收站 404
	restore := f.do(request{method: http.MethodPost, path: "/recycle-bin/trips/" + futureID + "/restore", token: token, headers: f.authHeaders(map[string]string{"If-Match": `"2"`})})
	expectStatus(t, restore, http.StatusOK, "")
	if d := restore.data()["data"].(map[string]any); d["deleted_at"] != nil || d["version"] != "3" {
		t.Fatalf("restore: %s", restore.Raw)
	}
	var requiresSnapshot bool
	if err := f.pool.QueryRow(context.Background(), `SELECT requires_snapshot FROM sync_changes WHERE account_id = $1 AND entity_type = 'trip' ORDER BY seq DESC LIMIT 1`, accountID).Scan(&requiresSnapshot); err != nil || !requiresSnapshot {
		t.Fatalf("restore change requires_snapshot: %v %v", requiresSnapshot, err)
	}
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/recycle-bin/trips/" + futureID, token: token}), http.StatusNotFound, "RESOURCE_NOT_FOUND")
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/trips/" + futureID, token: token}), http.StatusOK, "")

	// 永久清理：先删到回收站；未复验 403；复验后 202，affected 含 deletion_job，River 有任务；再恢复 409
	del2 := f.do(request{method: http.MethodDelete, path: "/trips/" + futureID, token: token, headers: f.authHeaders(map[string]string{"If-Match": `"3"`})})
	expectStatus(t, del2, http.StatusOK, "")
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/recycle-bin/trips/" + futureID + "/purge", token: token, body: map[string]any{"confirm": true}, headers: f.authHeaders(map[string]string{"If-Match": `"4"`})}),
		http.StatusForbidden, "REAUTH_REQUIRED")
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/auth/reauthenticate", token: token, body: map[string]any{"password": "correct horse battery"}}), http.StatusNoContent, "")
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/recycle-bin/trips/" + futureID + "/purge", token: token, body: map[string]any{"confirm": false}, headers: f.authHeaders(map[string]string{"If-Match": `"4"`})}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	purge := f.do(request{method: http.MethodPost, path: "/recycle-bin/trips/" + futureID + "/purge", token: token, body: map[string]any{"confirm": true}, headers: f.authHeaders(map[string]string{"If-Match": `"4"`})})
	expectStatus(t, purge, http.StatusAccepted, "")
	affected, _ := purge.data()["affected"].([]any)
	if len(affected) != 2 || affected[1].(map[string]any)["type"] != "deletion_job" || affected[1].(map[string]any)["version"] != nil {
		t.Fatalf("purge affected: %s", purge.Raw)
	}
	jobID := affected[1].(map[string]any)["id"].(string)
	var queued int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job WHERE kind = 'trip_purge' AND args->>'job_id' = $1`, jobID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("river job for purge: %d %v", queued, err)
	}
	var status string
	if err := f.pool.QueryRow(context.Background(), `SELECT status FROM deletion_jobs WHERE id = $1 AND owner_account_id = $2 AND target_trip_id = $3`, jobID, accountID, futureID).Scan(&status); err != nil || status != "queued" {
		t.Fatalf("deletion job row: %s %v", status, err)
	}
	if _, kind, _ := f.countChanges(accountID, "trip"); kind != "purge" {
		t.Fatalf("last change should be purge, got %s", kind)
	}
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/recycle-bin/trips/" + futureID + "/restore", token: token, headers: f.authHeaders(map[string]string{"If-Match": `"5"`})}),
		http.StatusConflict, "RESTORE_UNAVAILABLE")
	again := f.do(request{method: http.MethodPost, path: "/recycle-bin/trips/" + futureID + "/purge", token: token, body: map[string]any{"confirm": true}, headers: f.authHeaders(map[string]string{"If-Match": `"5"`})})
	expectStatus(t, again, http.StatusAccepted, "")
	if a, _ := again.data()["affected"].([]any); a[1].(map[string]any)["id"] != jobID {
		t.Fatalf("second purge should reuse the job: %s", again.Raw)
	}
}
