package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// tripContentFixture 注册账号并创建一趟旅行，供旅行内容（行程、行李、待办）测试共用。
type tripContentFixture struct {
	*apiFixture
	token     string
	accountID string
	tripID    string
}

func newTripContentFixture(t *testing.T) *tripContentFixture {
	t.Helper()
	f := newAPIFixture(t)
	reg := f.registerWeb(uniqueEmail(), "correct horse battery")
	token, _ := reg.data()["access_token"].(string)
	accountID, _ := reg.data()["account"].(map[string]any)["id"].(string)
	trip := f.createTrip(token, map[string]any{"name": "东京", "start_date": "2026-10-01", "end_date": "2026-10-07", "currency_code": "JPY"})
	return &tripContentFixture{apiFixture: f, token: token, accountID: accountID, tripID: trip["id"].(string)}
}

func (f *tripContentFixture) path(suffix string) string { return "/trips/" + f.tripID + suffix }

func (f *tripContentFixture) post(path string, body map[string]any) apiResponse {
	return f.do(request{method: http.MethodPost, path: path, token: f.token, body: body, headers: f.authHeaders(nil)})
}

func (f *tripContentFixture) patch(path, etag string, body map[string]any) apiResponse {
	return f.do(request{method: http.MethodPatch, path: path, token: f.token, body: body, headers: f.authHeaders(map[string]string{"If-Match": etag})})
}

func (f *tripContentFixture) del(path, etag string) apiResponse {
	return f.do(request{method: http.MethodDelete, path: path, token: f.token, headers: f.authHeaders(map[string]string{"If-Match": etag})})
}

func (f *tripContentFixture) get(path string) apiResponse {
	return f.do(request{method: http.MethodGet, path: path, token: f.token})
}

func (f *tripContentFixture) created(res apiResponse) map[string]any {
	f.t.Helper()
	expectStatus(f.t, res, http.StatusCreated, "")
	d, _ := res.data()["data"].(map[string]any)
	return d
}

func items(res apiResponse) []map[string]any {
	raw, _ := res.Body["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(map[string]any))
	}
	return out
}

func TestHTTPItineraryLifecycle(t *testing.T) {
	f := newTripContentFixture(t)

	// 创建：默认值、追加到当天末尾、变更日志 trip_id 指向旅行；金额按 JPY 规范化并派生 currency_code
	a := f.created(f.post(f.path("/itinerary-items"), map[string]any{
		"id": uuid.NewString(), "title": " 浅草寺 ", "kind": "attraction", "scheduled_on": "2026-10-02",
		"estimated_amount": "1500", "currency_code": "JPY", "latitude": 35.7148, "longitude": 139.7967,
	}))
	if a["title"] != "浅草寺" || a["sort_order"] != float64(0) || a["status"] != "pending" || a["estimated_amount"] != "1500" || a["currency_code"] != "JPY" {
		t.Fatalf("created: %v", a)
	}
	if a["latitude"] != 35.7148 || a["longitude"] != 139.7967 {
		t.Fatalf("coordinates: %v", a)
	}
	b := f.created(f.post(f.path("/itinerary-items"), map[string]any{"id": uuid.NewString(), "title": "晴空塔", "kind": "attraction", "scheduled_on": "2026-10-02"}))
	if b["sort_order"] != float64(1) || b["currency_code"] != nil {
		t.Fatalf("second item: %v", b)
	}
	var tripIDInLog string
	if err := f.pool.QueryRow(context.Background(), `SELECT trip_id::text FROM sync_changes WHERE account_id = $1 AND entity_type = 'itinerary_item' ORDER BY seq LIMIT 1`, f.accountID).Scan(&tripIDInLog); err != nil || tripIDInLog != f.tripID {
		t.Fatalf("change trip_id: %q %v", tripIDInLog, err)
	}

	// 旅行日期之外 → 警告；币种不符 → 422 CURRENCY_MISMATCH
	outside := f.post(f.path("/itinerary-items"), map[string]any{"id": uuid.NewString(), "title": "机场", "kind": "transport", "scheduled_on": "2026-09-30"})
	expectStatus(t, outside, http.StatusCreated, "")
	if w, _ := outside.data()["warnings"].([]any); len(w) != 1 || w[0] != "ITINERARY_OUTSIDE_TRIP_DATES" {
		t.Fatalf("warnings: %s", outside.Raw)
	}
	expectStatus(t, f.post(f.path("/itinerary-items"), map[string]any{"id": uuid.NewString(), "title": "x", "kind": "other", "scheduled_on": "2026-10-02", "estimated_amount": "1", "currency_code": "CNY"}),
		http.StatusUnprocessableEntity, "CURRENCY_MISMATCH")
	expectStatus(t, f.post(f.path("/itinerary-items"), map[string]any{"id": uuid.NewString(), "title": "x", "kind": "other", "scheduled_on": "2026-10-02", "planned_end_local": "2026-10-02T12:00:00", "planned_duration_minutes": 30}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// GET 带 ETag；PATCH 清空金额（显式 null）；合并与冲突
	aID := a["id"].(string)
	got := f.get(f.path("/itinerary-items/" + aID))
	expectStatus(t, got, http.StatusOK, "")
	if got.Header.Get("ETag") != strongETag1 {
		t.Fatalf("etag: %s", got.Header.Get("ETag"))
	}
	up := f.patch(f.path("/itinerary-items/"+aID), strongETag1, map[string]any{"estimated_amount": nil, "status": "completed"})
	expectStatus(t, up, http.StatusOK, "")
	if d := up.data()["data"].(map[string]any); d["estimated_amount"] != nil || d["currency_code"] != nil || d["status"] != "completed" || d["version"] != "2" {
		t.Fatalf("patch: %s", up.Raw)
	}
	merged := f.patch(f.path("/itinerary-items/"+aID), strongETag1, map[string]any{"notes": "提前买票"})
	expectStatus(t, merged, http.StatusOK, "")
	if w, _ := merged.data()["warnings"].([]any); len(w) != 1 || w[0] != "MERGED_WITH_NEWER_VERSION" {
		t.Fatalf("merge warnings: %s", merged.Raw)
	}
	conflict := f.patch(f.path("/itinerary-items/"+aID), strongETag1, map[string]any{"status": "skipped"})
	expectStatus(t, conflict, http.StatusPreconditionFailed, "VERSION_CONFLICT")
	if c, _ := conflict.Body["conflict"].(map[string]any); c["conflicting_fields"].([]any)[0] != "status" {
		t.Fatalf("conflict: %s", conflict.Raw)
	}

	// 重排：跨日移动，只写变化行；集合不符 409；版本不符 412
	bID := b["id"].(string)
	reorder := f.post(f.path("/itinerary-items/reorder"), map[string]any{"days": []map[string]any{
		{"date": "2026-10-02", "items": []map[string]any{{"id": bID, "base_version": "1"}}},
		{"date": "2026-10-03", "items": []map[string]any{{"id": aID, "base_version": "3"}}},
	}})
	expectStatus(t, reorder, http.StatusOK, "")
	if reorder.data()["primary"] != nil || reorder.data()["data"] != nil {
		t.Fatalf("reorder primary/data: %s", reorder.Raw)
	}
	if aff, _ := reorder.data()["affected"].([]any); len(aff) != 2 {
		t.Fatalf("affected: %s", reorder.Raw)
	}
	list := f.get(f.path("/itinerary-items?date_from=2026-10-02&date_to=2026-10-03"))
	expectStatus(t, list, http.StatusOK, "")
	rows := items(list)
	if len(rows) != 2 || rows[0]["id"] != bID || rows[0]["sort_order"] != float64(0) || rows[1]["id"] != aID || rows[1]["scheduled_on"] != "2026-10-03" || rows[1]["version"] != "4" {
		t.Fatalf("after reorder: %s", list.Raw)
	}
	expectStatus(t, f.post(f.path("/itinerary-items/reorder"), map[string]any{"days": []map[string]any{{"date": "2026-10-02", "items": []map[string]any{}}}}),
		http.StatusConflict, "ORDER_CHANGED")
	expectStatus(t, f.post(f.path("/itinerary-items/reorder"), map[string]any{"days": []map[string]any{{"date": "2026-10-02", "items": []map[string]any{{"id": bID, "base_version": "9"}}}}}),
		http.StatusPreconditionFailed, "VERSION_CONFLICT")

	// 分页游标
	first := f.get(f.path("/itinerary-items?limit=2"))
	expectStatus(t, first, http.StatusOK, "")
	cursor, _ := first.Body["next_cursor"].(string)
	if len(items(first)) != 2 || cursor == "" {
		t.Fatalf("first page: %s", first.Raw)
	}
	second := f.get(f.path("/itinerary-items?limit=2&cursor=" + cursor))
	expectStatus(t, second, http.StatusOK, "")
	if len(items(second)) != 1 || second.Body["next_cursor"] != nil {
		t.Fatalf("second page: %s", second.Raw)
	}
	expectStatus(t, f.get(f.path("/itinerary-items?limit=2&status=pending&cursor="+cursor)), http.StatusBadRequest, "INVALID_CURSOR")

	// 删除：版本相等；之后 410；日志最后一条 delete
	expectStatus(t, f.del(f.path("/itinerary-items/"+bID), `"1"`), http.StatusPreconditionFailed, "VERSION_CONFLICT")
	expectStatus(t, f.del(f.path("/itinerary-items/"+bID), `"2"`), http.StatusOK, "")
	expectStatus(t, f.get(f.path("/itinerary-items/"+bID)), http.StatusGone, "RESOURCE_GONE")
	if _, kind, _ := f.countChanges(f.accountID, "itinerary_item"); kind != "delete" {
		t.Fatalf("last change kind = %s", kind)
	}

	// 旅行进回收站后 410 TRIP_DELETED；跨账号 404
	tripETag := f.get("/trips/" + f.tripID).Header.Get("ETag")
	expectStatus(t, f.del("/trips/"+f.tripID, tripETag), http.StatusOK, "")
	expectStatus(t, f.get(f.path("/itinerary-items")), http.StatusGone, "TRIP_DELETED")
	other := f.registerWeb(uniqueEmail(), "correct horse battery")
	otherToken, _ := other.data()["access_token"].(string)
	expectStatus(t, f.do(request{method: http.MethodGet, path: f.path("/itinerary-items"), token: otherToken}), http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestHTTPPackingLifecycle(t *testing.T) {
	f := newTripContentFixture(t)

	// 物品库
	lib := f.get("/packing-library")
	expectStatus(t, lib, http.StatusOK, "")
	if lib.data()["version"] != "1" || len(lib.data()["categories"].([]any)) != 7 {
		t.Fatalf("library: %s", lib.Raw)
	}

	// 创建与同名冲突（大小写、空格不敏感）
	a := f.created(f.post(f.path("/packing-items"), map[string]any{"id": uuid.NewString(), "name": "Passport", "category": "documents"}))
	if a["quantity"] != float64(1) || a["status"] != "pending" {
		t.Fatalf("created: %v", a)
	}
	expectStatus(t, f.post(f.path("/packing-items"), map[string]any{"id": uuid.NewString(), "name": " passport ", "category": "documents"}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 批量：跳过既有与请求内重复；重放
	x, y, z := uuid.NewString(), uuid.NewString(), uuid.NewString()
	batchBody := map[string]any{"items": []map[string]any{
		{"id": x, "name": "充电宝", "category": "electronics", "quantity": 2},
		{"id": y, "name": "PASSPORT", "category": "documents"},
		{"id": z, "name": "充电宝", "category": "electronics"},
	}}
	opKey := uuid.NewString()
	batch := f.do(request{method: http.MethodPost, path: f.path("/packing-items/batch"), token: f.token, body: batchBody, headers: map[string]string{"Idempotency-Key": opKey}})
	expectStatus(t, batch, http.StatusCreated, "")
	d := batch.data()
	if d["primary"] != nil || d["data"] != nil || len(d["created_ids"].([]any)) != 1 || d["created_ids"].([]any)[0] != x || len(d["skipped"].([]any)) != 2 {
		t.Fatalf("batch: %s", batch.Raw)
	}
	replay := f.do(request{method: http.MethodPost, path: f.path("/packing-items/batch"), token: f.token, body: batchBody, headers: map[string]string{"Idempotency-Key": opKey}})
	expectStatus(t, replay, http.StatusCreated, "")
	if replay.data()["replayed"] != true || len(replay.data()["created_ids"].([]any)) != 1 {
		t.Fatalf("batch replay: %s", replay.Raw)
	}
	if n, _, _ := f.countChanges(f.accountID, "packing_item"); n != 2 {
		t.Fatalf("changes = %d, want 2", n)
	}

	// 列表按分类升序；筛选；状态推进；改名撞车 422
	list := f.get(f.path("/packing-items"))
	expectStatus(t, list, http.StatusOK, "")
	rows := items(list)
	if len(rows) != 2 || rows[0]["category"] != "documents" || rows[1]["category"] != "electronics" {
		t.Fatalf("list: %s", list.Raw)
	}
	aID := a["id"].(string)
	up := f.patch(f.path("/packing-items/"+aID), strongETag1, map[string]any{"status": "packed"})
	expectStatus(t, up, http.StatusOK, "")
	if up.data()["data"].(map[string]any)["status"] != "packed" {
		t.Fatalf("status: %s", up.Raw)
	}
	packed := f.get(f.path("/packing-items?status=packed"))
	if len(items(packed)) != 1 {
		t.Fatalf("status filter: %s", packed.Raw)
	}
	expectStatus(t, f.patch(f.path("/packing-items/"+x), strongETag1, map[string]any{"name": "passport", "category": "documents"}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 删除后名称可复用
	expectStatus(t, f.del(f.path("/packing-items/"+aID), `"2"`), http.StatusOK, "")
	expectStatus(t, f.get(f.path("/packing-items/"+aID)), http.StatusGone, "RESOURCE_GONE")
	f.created(f.post(f.path("/packing-items"), map[string]any{"id": uuid.NewString(), "name": "passport", "category": "documents"}))
}

func TestHTTPTodoLifecycle(t *testing.T) {
	f := newTripContentFixture(t)

	// 创建：completed=true 时服务端写 completed_at
	done := f.created(f.post(f.path("/todos"), map[string]any{"id": uuid.NewString(), "title": "买保险", "due_on": "2026-09-01", "completed": true}))
	if done["completed"] != true || done["completed_at"] == nil {
		t.Fatalf("completed: %v", done)
	}
	late := f.created(f.post(f.path("/todos"), map[string]any{"id": uuid.NewString(), "title": "换日元", "due_on": "2026-09-05"}))
	future := f.created(f.post(f.path("/todos"), map[string]any{"id": uuid.NewString(), "title": "打印行程", "due_on": "2099-01-01"}))
	none := f.created(f.post(f.path("/todos"), map[string]any{"id": uuid.NewString(), "title": "无期限"}))

	// 列表：截止日期升序、空日期最后；is_overdue 由旅行时区今天计算
	all := f.get(f.path("/todos"))
	expectStatus(t, all, http.StatusOK, "")
	rows := items(all)
	if len(rows) != 4 || rows[0]["id"] != done["id"] || rows[1]["id"] != late["id"] || rows[2]["id"] != future["id"] || rows[3]["id"] != none["id"] {
		t.Fatalf("order: %s", all.Raw)
	}
	if rows[0]["is_overdue"] != false || rows[1]["is_overdue"] != true || rows[2]["is_overdue"] != false || rows[3]["is_overdue"] != false {
		t.Fatalf("overdue flags: %s", all.Raw)
	}
	overdue := f.get(f.path("/todos?state=overdue"))
	if r := items(overdue); len(r) != 1 || r[0]["id"] != late["id"] {
		t.Fatalf("overdue filter: %s", overdue.Raw)
	}
	if r := items(f.get(f.path("/todos?state=completed"))); len(r) != 1 || r[0]["id"] != done["id"] {
		t.Fatalf("completed filter: %v", r)
	}
	if r := items(f.get(f.path("/todos?state=pending"))); len(r) != 3 {
		t.Fatalf("pending filter: %v", r)
	}
	expectStatus(t, f.get(f.path("/todos?state=late")), http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 游标跨越空日期哨兵
	first := f.get(f.path("/todos?limit=3"))
	cursor, _ := first.Body["next_cursor"].(string)
	second := f.get(f.path("/todos?limit=3&cursor=" + cursor))
	expectStatus(t, second, http.StatusOK, "")
	if r := items(second); len(r) != 1 || r[0]["id"] != none["id"] {
		t.Fatalf("second page: %s", second.Raw)
	}

	// PATCH：完成/取消完成、清空截止日期；合并与冲突
	lateID := late["id"].(string)
	up := f.patch(f.path("/todos/"+lateID), strongETag1, map[string]any{"completed": true})
	expectStatus(t, up, http.StatusOK, "")
	if d := up.data()["data"].(map[string]any); d["completed"] != true || d["completed_at"] == nil {
		t.Fatalf("complete: %s", up.Raw)
	}
	reopen := f.patch(f.path("/todos/"+lateID), `"2"`, map[string]any{"completed": false, "due_on": nil})
	expectStatus(t, reopen, http.StatusOK, "")
	if d := reopen.data()["data"].(map[string]any); d["completed"] != false || d["completed_at"] != nil || d["due_on"] != nil {
		t.Fatalf("reopen: %s", reopen.Raw)
	}
	merged := f.patch(f.path("/todos/"+lateID), strongETag1, map[string]any{"title": "换 5 万日元"})
	expectStatus(t, merged, http.StatusOK, "")
	if w, _ := merged.data()["warnings"].([]any); len(w) != 1 || w[0] != "MERGED_WITH_NEWER_VERSION" {
		t.Fatalf("merge: %s", merged.Raw)
	}
	expectStatus(t, f.patch(f.path("/todos/"+lateID), strongETag1, map[string]any{"completed": true}), http.StatusPreconditionFailed, "VERSION_CONFLICT")

	// 删除
	expectStatus(t, f.del(f.path("/todos/"+lateID), `"4"`), http.StatusOK, "")
	expectStatus(t, f.get(f.path("/todos/"+lateID)), http.StatusGone, "RESOURCE_GONE")
	if n, kind, _ := f.countChanges(f.accountID, "todo"); n != 8 || kind != "delete" {
		t.Fatalf("changes n=%d kind=%s", n, kind)
	}
}
