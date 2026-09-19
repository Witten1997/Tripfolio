package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// 旅行成员、账目分摊与结算的 HTTP 闭环（接口设计 2.5、3.5、3.8）：创建旅行自动生成「我」、整体保存成员、按比例分摊、结算与转账、被引用成员不可删。
func TestHTTPMembersSplitAndSettlement(t *testing.T) {
	f := newAPIFixture(t)
	reg := f.registerWeb(uniqueEmail(), "correct horse battery")
	token, _ := reg.data()["access_token"].(string)
	trip := f.createTrip(token, map[string]any{"name": "成员测试", "start_date": "2026-10-01", "end_date": "2026-10-03"})
	tripID := trip["id"].(string)
	base := "/trips/" + tripID

	// 创建旅行即有「我」
	list := f.do(request{method: http.MethodGet, path: base + "/members", token: token})
	expectStatus(t, list, http.StatusOK, "")
	members, _ := list.Body["data"].([]any)
	if len(members) != 1 || members[0].(map[string]any)["is_self"] != true || members[0].(map[string]any)["share_percent"] != "100" {
		t.Fatalf("self member: %s", list.Raw)
	}
	selfID := members[0].(map[string]any)["id"].(string)

	// 总和不为 100 → 422 SHARE_PERCENT_SUM；正确保存 → affected 含两位成员
	friendID := uuid.NewString()
	bad := f.do(request{method: http.MethodPut, path: base + "/members", token: token, headers: f.authHeaders(nil), body: map[string]any{
		"members": []map[string]any{{"id": selfID, "name": "我", "share_percent": "60"}, {"id": friendID, "name": "小王", "share_percent": "50"}},
	}})
	expectStatus(t, bad, http.StatusUnprocessableEntity, "SHARE_PERCENT_SUM")
	ok := f.do(request{method: http.MethodPut, path: base + "/members", token: token, headers: f.authHeaders(nil), body: map[string]any{
		"members": []map[string]any{{"id": selfID, "name": "我", "share_percent": "60"}, {"id": friendID, "name": "小王", "share_percent": "40"}},
	}})
	expectStatus(t, ok, http.StatusOK, "")
	if affected, _ := ok.data()["affected"].([]any); len(affected) != 2 {
		t.Fatalf("save members affected: %s", ok.Raw)
	}

	// 记账：小王付 100，按比例分摊 → 我 60、小王 40；付款人与份额落库
	cats := f.do(request{method: http.MethodGet, path: "/expense-categories", token: token})
	categoryID := cats.Body["data"].([]any)[0].(map[string]any)["id"].(string)
	entryID := uuid.NewString()
	created := f.do(request{method: http.MethodPost, path: base + "/ledger-entries", token: token, headers: f.authHeaders(nil), body: map[string]any{
		"id": entryID, "kind": "expense", "amount": "100", "category_id": categoryID,
		"payer_member_id": friendID, "split_mode": "ratio",
	}})
	expectStatus(t, created, http.StatusCreated, "")
	entry := created.data()["data"].(map[string]any)
	splits, _ := entry["splits"].([]any)
	if entry["payer_member_id"] != friendID || entry["split_count"] != float64(2) || entry["personal_amount"] != "60.00" || len(splits) != 2 ||
		splits[0].(map[string]any)["amount"] != "60.00" || splits[1].(map[string]any)["amount"] != "40.00" {
		t.Fatalf("split entry: %s", created.Raw)
	}

	// 结算：小王付 100 承担 40 → 应收 60；我应付 60；转账一笔
	settle := f.do(request{method: http.MethodGet, path: base + "/settlement", token: token})
	expectStatus(t, settle, http.StatusOK, "")
	rows, _ := settle.data()["members"].([]any)
	transfers, _ := settle.data()["transfers"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["net_amount"] != "-60.00" || rows[1].(map[string]any)["net_amount"] != "60.00" ||
		len(transfers) != 1 || transfers[0].(map[string]any)["from_member_id"] != selfID || transfers[0].(map[string]any)["amount"] != "60.00" {
		t.Fatalf("settlement: %s", settle.Raw)
	}

	// 被引用的成员不能删除 → 409 MEMBER_IN_USE
	del := f.do(request{method: http.MethodPut, path: base + "/members", token: token, headers: f.authHeaders(nil), body: map[string]any{
		"members": []map[string]any{{"id": selfID, "name": "我", "share_percent": "100"}},
	}})
	expectStatus(t, del, http.StatusConflict, "MEMBER_IN_USE")
}
