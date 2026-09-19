package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// 账单分类的 HTTP 行为（接口设计 2.5、3.5）：预设可改、名称唯一、排序、字段级合并、软删除与 CATEGORY_IN_USE。
func TestHTTPExpenseCategoriesLifecycle(t *testing.T) {
	f := newAPIFixture(t)
	reg := f.registerWeb(uniqueEmail(), "correct horse battery")
	token, _ := reg.data()["access_token"].(string)
	accountID, _ := reg.data()["account"].(map[string]any)["id"].(string)

	list := f.do(request{method: http.MethodGet, path: "/expense-categories", token: token})
	expectStatus(t, list, http.StatusOK, "")
	presets, _ := list.Body["data"].([]any)
	if len(presets) != 6 || presets[0].(map[string]any)["name"] != "交通" || presets[5].(map[string]any)["is_preset"] != true {
		t.Fatalf("presets: %s", list.Raw)
	}
	if n, _, _ := f.countChanges(accountID, "expense_category"); n != 6 {
		t.Fatalf("registration should log 6 category changes, got %d", n)
	}
	var tripIDs int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM sync_changes WHERE account_id = $1 AND entity_type = 'expense_category' AND trip_id IS NOT NULL`, accountID).Scan(&tripIDs); err != nil || tripIDs != 0 {
		t.Fatalf("account-level changes must have null trip_id: %d %v", tripIDs, err)
	}
	transport := presets[0].(map[string]any)["id"].(string)

	// 创建：缺省 sort_order 追加到末尾；同名（大小写、空白不敏感）422；未知图标 422
	id := uuid.NewString()
	created := f.do(request{method: http.MethodPost, path: "/expense-categories", token: token, body: map[string]any{"id": id, "name": " 门票 ", "icon": "ticket"}, headers: f.authHeaders(nil)})
	expectStatus(t, created, http.StatusCreated, "")
	if d := created.data()["data"].(map[string]any); d["name"] != "门票" || d["sort_order"] != float64(6) || d["is_preset"] != false || d["icon"] != "ticket" {
		t.Fatalf("created category: %s", created.Raw)
	}
	dup := f.do(request{method: http.MethodPost, path: "/expense-categories", token: token, body: map[string]any{"id": uuid.NewString(), "name": "门票 "}, headers: f.authHeaders(nil)})
	expectStatus(t, dup, http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	if errs, _ := dup.Body["errors"].([]any); len(errs) != 1 || errs[0].(map[string]any)["code"] != "DUPLICATE" {
		t.Fatalf("duplicate name: %s", dup.Raw)
	}
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/expense-categories", token: token, body: map[string]any{"id": uuid.NewString(), "name": "x", "icon": "rocket"}, headers: f.authHeaders(nil)}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 预设改名 + 排序到末尾；icon 显式 null 清空；字段级合并与冲突
	up := f.do(request{method: http.MethodPatch, path: "/expense-categories/" + transport, token: token, body: map[string]any{"name": "交通出行", "sort_order": 10}, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})})
	expectStatus(t, up, http.StatusOK, "")
	if d := up.data()["data"].(map[string]any); d["version"] != "2" || d["name"] != "交通出行" || d["sort_order"] != float64(10) || d["is_preset"] != true {
		t.Fatalf("patch preset: %s", up.Raw)
	}
	cleared := f.do(request{method: http.MethodPatch, path: "/expense-categories/" + transport, token: token, body: map[string]any{"icon": nil}, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})})
	expectStatus(t, cleared, http.StatusOK, "")
	if d := cleared.data()["data"].(map[string]any); d["version"] != "3" || d["icon"] != nil {
		t.Fatalf("clear icon with stale base should merge: %s", cleared.Raw)
	}
	if w, _ := cleared.data()["warnings"].([]any); len(w) != 1 || w[0] != "MERGED_WITH_NEWER_VERSION" {
		t.Fatalf("merge warning: %s", cleared.Raw)
	}
	conflict := f.do(request{method: http.MethodPatch, path: "/expense-categories/" + transport, token: token, body: map[string]any{"name": "交通"}, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})})
	expectStatus(t, conflict, http.StatusPreconditionFailed, "VERSION_CONFLICT")
	if c, _ := conflict.Body["conflict"].(map[string]any); c["conflicting_fields"].([]any)[0] != "name" {
		t.Fatalf("conflict: %s", conflict.Raw)
	}
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/expense-categories/" + transport, token: token, body: map[string]any{}, headers: f.authHeaders(map[string]string{"If-Match": `"3"`})}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	// 列表顺序：sort_order 升序，改名后的预设排到末尾
	list = f.do(request{method: http.MethodGet, path: "/expense-categories", token: token})
	items, _ := list.Body["data"].([]any)
	if len(items) != 7 || items[6].(map[string]any)["id"] != transport || items[5].(map[string]any)["id"] != id {
		t.Fatalf("ordering: %s", list.Raw)
	}

	// 删除：被账目引用 → 409 CATEGORY_IN_USE（账目在回收站旅行内同样算引用）；解除引用后 → 200 软删除；GET 410；列表隐藏；名称可复用
	trip := f.createTrip(token, map[string]any{"name": "关西", "start_date": "2026-10-01", "end_date": "2026-10-03"})
	entryID := uuid.NewString()
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO ledger_entries (id, account_id, trip_id, kind, amount, category_id, occurred_on, payer_member_id, created_at, updated_at)
		 VALUES ($1, $2, $3, 'expense', 12.5, $4, DATE '2026-10-01',
		         (SELECT m.id FROM trip_members m WHERE m.account_id = $2 AND m.trip_id = $3 AND m.is_self AND m.deleted_at IS NULL), now(), now())`, entryID, accountID, trip["id"], id); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, f.do(request{method: http.MethodDelete, path: "/expense-categories/" + id, token: token, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})}),
		http.StatusConflict, "CATEGORY_IN_USE")
	expectStatus(t, f.do(request{method: http.MethodDelete, path: "/trips/" + trip["id"].(string), token: token, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})}), http.StatusOK, "")
	expectStatus(t, f.do(request{method: http.MethodDelete, path: "/expense-categories/" + id, token: token, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})}),
		http.StatusConflict, "CATEGORY_IN_USE")
	if _, err := f.pool.Exec(context.Background(), `UPDATE ledger_entries SET deleted_at = now() WHERE id = $1`, entryID); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, f.do(request{method: http.MethodDelete, path: "/expense-categories/" + id, token: token, headers: f.authHeaders(map[string]string{"If-Match": `"2"`})}),
		http.StatusPreconditionFailed, "VERSION_CONFLICT")
	del := f.do(request{method: http.MethodDelete, path: "/expense-categories/" + id, token: token, headers: f.authHeaders(map[string]string{"If-Match": strongETag1})})
	expectStatus(t, del, http.StatusOK, "")
	if d := del.data()["data"].(map[string]any); d["deleted_at"] == nil || d["version"] != "2" {
		t.Fatalf("soft delete: %s", del.Raw)
	}
	if _, kind, _ := f.countChanges(accountID, "expense_category"); kind != "delete" {
		t.Fatalf("last category change should be delete, got %s", kind)
	}
	expectStatus(t, f.do(request{method: http.MethodGet, path: "/expense-categories/" + id, token: token}), http.StatusGone, "RESOURCE_GONE")
	expectStatus(t, f.do(request{method: http.MethodPatch, path: "/expense-categories/" + id, token: token, body: map[string]any{"name": "x"}, headers: f.authHeaders(map[string]string{"If-Match": `"2"`})}), http.StatusGone, "RESOURCE_GONE")
	if items, _ := f.do(request{method: http.MethodGet, path: "/expense-categories", token: token}).Body["data"].([]any); len(items) != 6 {
		t.Fatalf("deleted category must be hidden, got %d", len(items))
	}
	reuse := f.do(request{method: http.MethodPost, path: "/expense-categories", token: token, body: map[string]any{"id": uuid.NewString(), "name": "门票"}, headers: f.authHeaders(nil)})
	expectStatus(t, reuse, http.StatusCreated, "")
	// 已删除的 ID 不能复用
	expectStatus(t, f.do(request{method: http.MethodPost, path: "/expense-categories", token: token, body: map[string]any{"id": id, "name": "别的"}, headers: f.authHeaders(nil)}),
		http.StatusConflict, "ID_ALREADY_USED")
}
