package integration

import (
	"context"
	"github.com/getkin/kin-openapi/openapi3"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func (f *tripContentFixture) shareReq(method, path string) apiResponse {
	return f.do(request{method: method, path: path, token: f.token})
}

func (f *apiFixture) asGuest(token, path string) apiResponse {
	return f.do(request{method: http.MethodGet, path: path, headers: map[string]string{"Authorization": "Share " + token}})
}

func shareToken(t *testing.T, res apiResponse) string {
	t.Helper()
	expectStatus(t, res, http.StatusOK, "")
	d := res.data()
	tok, _ := d["token"].(string)
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`).MatchString(tok) {
		t.Fatalf("token: %s", res.Raw)
	}
	return tok
}

func TestHTTPShareLifecycleAndGuestReads(t *testing.T) {
	f := newTripContentFixture(t)
	f.created(f.post(f.path("/itinerary-items"), map[string]any{
		"id": uuid.NewString(), "title": "浅草寺", "kind": "attraction", "scheduled_on": "2026-10-02",
		"latitude": 35.7148, "longitude": 139.7967, "notes": "私密备注", "estimated_amount": "1500", "currency_code": "JPY",
	}))
	f.created(f.post(f.path("/itinerary-items"), map[string]any{"id": uuid.NewString(), "title": "酒店", "kind": "lodging", "scheduled_on": "2026-10-01"}))

	// 未开启：404 SHARE_NOT_FOUND；开启幂等；url 按 WebBaseURL 拼出
	expectStatus(t, f.shareReq(http.MethodGet, f.path("/share")), http.StatusNotFound, "SHARE_NOT_FOUND")
	first := f.shareReq(http.MethodPut, f.path("/share"))
	tok := shareToken(t, first)
	if d := first.data(); d["url"] != "http://localhost:5173/s/"+tok || d["view_count"] != float64(0) {
		t.Fatalf("share: %s", first.Raw)
	}
	if again := shareToken(t, f.shareReq(http.MethodPut, f.path("/share"))); again != tok {
		t.Fatalf("重复开启应返回同一令牌")
	}
	var changesBefore int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM sync_changes WHERE account_id = $1", f.accountID).Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}

	// 访客读取：旅行只含公开字段；行程只含骨架且顺序正确；计数递增
	trip := f.asGuest(tok, "/public/trip")
	expectStatus(t, trip, http.StatusOK, "")
	if trip.Header.Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Fatalf("robots: %v", trip.Header)
	}
	td := trip.data()
	if td["name"] != "东京" || td["timezone"] == nil || td["currency_code"] != nil || td["notes"] != nil || td["budget_amount"] != nil || len(td) != 5 {
		t.Fatalf("public trip: %s", trip.Raw)
	}
	list := f.asGuest(tok, "/public/itinerary-items?limit=1")
	expectStatus(t, list, http.StatusOK, "")
	page1 := items(list)
	if len(page1) != 1 || page1[0]["title"] != "酒店" || list.Body["next_cursor"] == nil {
		t.Fatalf("page 1: %s", list.Raw)
	}
	cursor, _ := list.Body["next_cursor"].(string)
	list2 := f.asGuest(tok, "/public/itinerary-items?limit=1&cursor="+cursor)
	page2 := items(list2)
	if len(page2) != 1 || page2[0]["title"] != "浅草寺" || page2[0]["latitude"] != 35.7148 || len(page2[0]) != 12 {
		t.Fatalf("page 2: %s", list2.Raw)
	}
	for _, forbidden := range []string{"notes", "estimated_amount", "currency_code", "status", "actual_notes", "version", "trip_id"} {
		if _, ok := page2[0][forbidden]; ok {
			t.Fatalf("公开行程泄露 %s: %s", forbidden, list2.Raw)
		}
	}
	if got := f.shareReq(http.MethodGet, f.path("/share")); got.data()["view_count"] != float64(1) {
		t.Fatalf("view_count: %s", got.Raw)
	}

	// 重新生成：旧令牌立即失效；关闭：204 幂等，新令牌也失效
	rotated := shareToken(t, f.shareReq(http.MethodPost, f.path("/share/rotate")))
	if rotated == tok {
		t.Fatal("rotate 应换令牌")
	}
	expectStatus(t, f.asGuest(tok, "/public/trip"), http.StatusNotFound, "SHARE_NOT_FOUND")
	expectStatus(t, f.asGuest(rotated, "/public/trip"), http.StatusOK, "")
	expectStatus(t, f.shareReq(http.MethodDelete, f.path("/share")), http.StatusNoContent, "")
	expectStatus(t, f.shareReq(http.MethodDelete, f.path("/share")), http.StatusNoContent, "")
	expectStatus(t, f.asGuest(rotated, "/public/trip"), http.StatusNotFound, "SHARE_NOT_FOUND")
	expectStatus(t, f.shareReq(http.MethodPost, f.path("/share/rotate")), http.StatusNotFound, "SHARE_NOT_FOUND")

	// 分享不进同步体系：旅行版本仍为 1，没有 trip 变更日志之外的记录
	if n, _, _ := f.countChanges(f.accountID, "trip"); n != 1 {
		t.Fatalf("分享操作不应写旅行变更日志，实际 %d 条", n)
	}
	var changesAfter int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM sync_changes WHERE account_id = $1", f.accountID).Scan(&changesAfter); err != nil || changesAfter != changesBefore {
		t.Fatalf("分享不得产生任何类型同步日志: %d -> %d, %v", changesBefore, changesAfter, err)
	}
	var version int64
	if err := f.pool.QueryRow(context.Background(), `SELECT version FROM trips WHERE account_id = $1 AND id = $2`, f.accountID, f.tripID).Scan(&version); err != nil || version != 1 {
		t.Fatalf("分享操作不应递增旅行版本: %d %v", version, err)
	}
}

func TestHTTPShareSameOwnerTripsAndCursorIsolation(t *testing.T) {
	f := newTripContentFixture(t)
	for _, title := range []string{"A", "B"} {
		f.created(f.post(f.path("/itinerary-items"), map[string]any{"id": uuid.NewString(), "title": title, "kind": "attraction", "scheduled_on": "2026-10-01"}))
	}
	tok := shareToken(t, f.shareReq(http.MethodPut, f.path("/share")))
	second := f.createTrip(f.token, map[string]any{"name": "同账号另一趟", "start_date": "2026-11-01", "end_date": "2026-11-03"})
	secondID := second["id"].(string)
	secondToken := shareToken(t, f.shareReq(http.MethodPut, "/trips/"+secondID+"/share"))
	firstPage := f.asGuest(tok, "/public/itinerary-items?limit=1")
	expectStatus(t, firstPage, 200, "")
	cursor := firstPage.Body["next_cursor"].(string)
	expectStatus(t, f.asGuest(secondToken, "/public/itinerary-items?cursor="+cursor), 400, "INVALID_CURSOR")
	got := f.asGuest(tok, "/public/trip?trip_id="+secondID)
	expectStatus(t, got, 200, "")
	if got.data()["name"] != "东京" {
		t.Fatalf("查询参数不得改变令牌绑定旅行: %s", got.Raw)
	}
	empty := f.asGuest(secondToken, "/public/itinerary-items")
	expectStatus(t, empty, 200, "")
	if len(items(empty)) != 0 {
		t.Fatalf("不得读到同账号另一趟的行程: %s", empty.Raw)
	}
	for _, path := range []string{"/public/trip", "/public/itinerary-items", "/public/routes?mode=driving"} {
		for _, headers := range []map[string]string{nil, {"Authorization": "Bearer " + f.token}} {
			res := f.do(request{method: http.MethodGet, path: path, headers: headers})
			expectStatus(t, res, 401, "AUTH_REQUIRED")
			if res.Header.Get("X-Robots-Tag") != "noindex, nofollow" {
				t.Fatal("错误响应也必须防收录")
			}
		}
	}
}

func TestHTTPShareIsolationAndTripState(t *testing.T) {
	f := newTripContentFixture(t)
	tok := shareToken(t, f.shareReq(http.MethodPut, f.path("/share")))

	// 另一账号：不能操作别人的分享；自己的令牌读不到别人的旅行
	other := f.registerWeb(uniqueEmail(), "correct horse battery")
	otherToken, _ := other.data()["access_token"].(string)
	expectStatus(t, f.do(request{method: http.MethodPut, path: f.path("/share"), token: otherToken}), http.StatusNotFound, "RESOURCE_NOT_FOUND")
	otherTrip := f.createTrip(otherToken, map[string]any{"name": "大阪", "start_date": "2026-11-01", "end_date": "2026-11-03"})
	otherShare := shareToken(t, f.do(request{method: http.MethodPut, path: "/trips/" + otherTrip["id"].(string) + "/share", token: otherToken}))
	if got := f.asGuest(otherShare, "/public/trip"); got.data()["name"] != "大阪" {
		t.Fatalf("令牌应只读到自己那趟旅行: %s", got.Raw)
	}

	// 归档仍可看；回收站 410；恢复后继续有效
	expectStatus(t, f.do(request{method: http.MethodPost, path: f.path("/archive"), token: f.token, body: map[string]any{"archived": true}, headers: f.authHeaders(map[string]string{"If-Match": `"1"`})}), http.StatusOK, "")
	expectStatus(t, f.asGuest(tok, "/public/trip"), http.StatusOK, "")
	expectStatus(t, f.do(request{method: http.MethodDelete, path: "/trips/" + f.tripID, token: f.token, headers: f.authHeaders(map[string]string{"If-Match": `"2"`})}), http.StatusOK, "")
	expectStatus(t, f.asGuest(tok, "/public/trip"), http.StatusGone, "TRIP_DELETED")
	expectStatus(t, f.asGuest(tok, "/public/itinerary-items"), http.StatusGone, "TRIP_DELETED")
	expectStatus(t, f.shareReq(http.MethodGet, f.path("/share")), http.StatusGone, "TRIP_DELETED")
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/recycle-bin/trips/" + f.tripID + "/restore", token: f.token, headers: f.authHeaders(map[string]string{"If-Match": `"3"`})}), http.StatusOK, "")
	expectStatus(t, f.asGuest(tok, "/public/trip"), http.StatusOK, "")

	// 主人注销中：403
	if _, err := f.pool.Exec(context.Background(), `UPDATE accounts SET status = 'deleting' WHERE id = $1`, f.accountID); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, f.asGuest(tok, "/public/trip"), http.StatusForbidden, "ACCOUNT_DELETING")
}

// 越权表驱动：显式场景加契约中的全部账号接口，分享令牌一律 401。
func TestHTTPShareTokenCannotReachAccountEndpoints(t *testing.T) {
	f := newTripContentFixture(t)
	tok := shareToken(t, f.shareReq(http.MethodPut, f.path("/share")))
	cases := []struct{ method, path string }{
		{http.MethodGet, "/account"}, {http.MethodPatch, "/account"}, {http.MethodGet, "/account/sessions"},
		{http.MethodGet, "/trips"}, {http.MethodPost, "/trips"}, {http.MethodGet, "/trips/" + f.tripID}, {http.MethodPatch, "/trips/" + f.tripID}, {http.MethodDelete, "/trips/" + f.tripID},
		{http.MethodGet, f.path("/share")}, {http.MethodPut, f.path("/share")}, {http.MethodPost, f.path("/share/rotate")}, {http.MethodDelete, f.path("/share")},
		{http.MethodGet, f.path("/itinerary-items")}, {http.MethodPost, f.path("/itinerary-items")}, {http.MethodPost, f.path("/itinerary-items/reorder")},
		{http.MethodGet, f.path("/packing-items")}, {http.MethodGet, f.path("/todos")}, {http.MethodGet, f.path("/ledger-entries")}, {http.MethodGet, f.path("/statistics")},
		{http.MethodGet, "/expense-categories"}, {http.MethodGet, "/recycle-bin/trips"}, {http.MethodGet, "/packing-library"},
		{http.MethodGet, "/geo/routes?origin_latitude=35.1&origin_longitude=139.1&destination_latitude=35.2&destination_longitude=139.2&mode=driving"},
		{http.MethodGet, "/geo/places?q=tokyo"}, {http.MethodGet, f.path("/assets")}, {http.MethodPost, "/assets"},
	}

	// 从当前契约收集全部 bearerAuth 操作，新增账号接口会自动进入越权表。
	spec, err := openapi3.NewLoader().LoadFromFile("../../../../packages/contracts/dist/openapi.v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	accountOperations := map[string]bool{}
	for path, entry := range spec.Paths.Map() {
		for method, operation := range entry.Operations() {
			security := spec.Security
			if operation.Security != nil {
				security = *operation.Security
			}
			bearer := false
			for _, requirement := range security {
				if _, ok := requirement["bearerAuth"]; ok {
					bearer = true
				}
			}
			if !bearer {
				continue
			}
			accountOperations[method+" "+path] = true
			resolved := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, f.tripID)
			cases = append(cases, struct{ method, path string }{strings.ToUpper(method), resolved})
		}
	}
	// 设计已登记但尚未进入 OpenAPI 的后续切片接口，也必须被账号认证挡住。
	design, err := os.ReadFile("../../../../docs/api/2026-09-11-P0接口设计.md")
	if err != nil {
		t.Fatal(err)
	}
	public := map[string]bool{"/metadata": true, "/auth/email-challenges": true, "/auth/register": true, "/auth/login": true, "/auth/refresh": true, "/auth/password-reset": true}
	for _, row := range regexp.MustCompile(`(?m)^\| (GET|POST|PUT|PATCH|DELETE) \| ([^ |]+) \|`).FindAllStringSubmatch(string(design), -1) {
		method, path := row[1], row[2]
		if public[path] || strings.HasPrefix(path, "/public/") || (method == "GET" && path == "/account/deletion/{job_id}") {
			continue
		}
		accountOperations[method+" "+path] = true
		resolved := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, f.tripID)
		cases = append(cases, struct{ method, path string }{method, resolved})
	}
	if len(accountOperations) != 95 {
		t.Fatalf("设计及契约应覆盖全部 95 个账号接口，实际 %d", len(accountOperations))
	}
	t.Logf("遍历全部 %d 个账号接口，并保留计划的显式场景", len(accountOperations))
	for _, c := range cases {
		res := f.do(request{method: c.method, path: c.path, headers: map[string]string{"Authorization": "Share " + tok, "Idempotency-Key": uuid.NewString(), "If-Match": `"1"`}})
		if res.Status != http.StatusUnauthorized || res.Body["code"] != "AUTH_REQUIRED" {
			t.Errorf("%s %s: status=%d code=%v；分享令牌不得触达账号接口", c.method, c.path, res.Status, res.Body["code"])
		}
	}
	// 反向：登录用户的 Bearer 不能代替分享令牌读公开接口
	expectStatus(t, f.get("/public/trip"), http.StatusUnauthorized, "AUTH_REQUIRED")
}

func TestHTTPSharedRoutesDegradeWithoutProvider(t *testing.T) {
	f := newTripContentFixture(t)
	f.created(f.post(f.path("/itinerary-items"), map[string]any{"id": uuid.NewString(), "title": "A", "kind": "attraction", "scheduled_on": "2026-10-02", "latitude": 35.1, "longitude": 139.1}))
	f.created(f.post(f.path("/itinerary-items"), map[string]any{"id": uuid.NewString(), "title": "B", "kind": "attraction", "scheduled_on": "2026-10-02", "latitude": 35.2, "longitude": 139.2}))
	tok := shareToken(t, f.shareReq(http.MethodPut, f.path("/share")))
	expectStatus(t, f.asGuest(tok, "/public/routes?mode=flying"), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	// 测试夹具未配置高德：整体 503 且不带 Retry-After，前端据此降级为示意连线
	res := f.asGuest(tok, "/public/routes?mode=driving")
	expectStatus(t, res, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE")
	if res.Header.Get("Retry-After") != "" {
		t.Fatalf("未配置不应带 Retry-After: %v", res.Header)
	}
	// 没有任何有坐标项目时不触发上游：200 空结果
	f2 := newTripContentFixture(t)
	tok2 := shareToken(t, f2.shareReq(http.MethodPut, f2.path("/share")))
	empty := f2.asGuest(tok2, "/public/routes?mode=driving")
	expectStatus(t, empty, http.StatusOK, "")
	if d := empty.data(); len(d["legs"].([]any)) != 0 || d["unlocated_item_count"] != float64(0) || d["failed_leg_count"] != float64(0) {
		t.Fatalf("empty routes: %s", empty.Raw)
	}
}
