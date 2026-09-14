package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// ledgerFixture 在旅行内容 fixture 上加预设分类 ID，供账目与统计测试共用。
type ledgerFixture struct {
	*tripContentFixture
	food, lodging string
}

func newLedgerFixture(t *testing.T) *ledgerFixture {
	t.Helper()
	f := newTripContentFixture(t)
	cats := f.get("/expense-categories")
	expectStatus(t, cats, http.StatusOK, "")
	byName := map[string]string{}
	for _, c := range cats.Body["data"].([]any) {
		m := c.(map[string]any)
		byName[m["name"].(string)] = m["id"].(string)
	}
	return &ledgerFixture{tripContentFixture: f, food: byName["美食"], lodging: byName["住宿"]}
}

func (f *ledgerFixture) entries() string { return f.path("/ledger-entries") }

func (f *ledgerFixture) entry(id string) string { return f.path("/ledger-entries/" + id) }

// insertAsset 直接写一条旅行图片资产（资产接口在切片 5 提供）。
func (f *ledgerFixture) insertAsset(deleted bool) string {
	f.t.Helper()
	id := uuid.NewString()
	deletedExpr := "NULL"
	if deleted {
		deletedExpr = "now()"
	}
	_, err := f.pool.Exec(context.Background(),
		`INSERT INTO assets (id, account_id, scope, trip_id, original_name, status, declared_media_type, staging_object_key, expected_size, upload_expires_at, deleted_at)
		 VALUES ($1, $2, 'trip', $3, 'receipt.jpg', 'uploading', 'image/jpeg', 'staging/' || $4, 1024, now() + interval '1 hour', `+deletedExpr+`)`, id, f.accountID, f.tripID, id)
	if err != nil {
		f.t.Fatalf("insert asset: %v", err)
	}
	return id
}

func (f *ledgerFixture) tripVersionAndLock() (string, bool) {
	f.t.Helper()
	res := f.get("/trips/" + f.tripID)
	expectStatus(f.t, res, http.StatusOK, "")
	d := res.data()
	return d["version"].(string), d["currency_locked_at"] != nil
}

func TestHTTPLedgerLifecycle(t *testing.T) {
	f := newLedgerFixture(t)

	// 创建：金额按 JPY 规范化、币种派生、默认日期为旅行时区今天；第一条锁定旅行币种并进入 affected
	first := f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "expense", "amount": "1500.0", "category_id": f.food, "notes": "拉面"})
	a := f.created(first)
	if a["amount"] != "1500" || a["currency_code"] != "JPY" || a["version"] != "1" || a["refunded_entry_id"] != nil || a["notes"] != "拉面" {
		t.Fatalf("created: %v", a)
	}
	if att, ok := a["attachment_asset_ids"].([]any); !ok || len(att) != 0 {
		t.Fatalf("attachments should be []: %s", first.Raw)
	}
	affected := first.data()["affected"].([]any)
	if len(affected) != 2 || affected[1].(map[string]any)["type"] != "trip" {
		t.Fatalf("affected should include trip: %s", first.Raw)
	}
	if v, locked := f.tripVersionAndLock(); v != "2" || !locked {
		t.Fatalf("trip should be locked at v2: %s %v", v, locked)
	}
	if n, kind, ver := f.countChanges(f.accountID, "ledger_entry"); n != 1 || kind != "upsert" || ver != 1 {
		t.Fatalf("ledger changes: %d %s %d", n, kind, ver)
	}
	if n, _, ver := f.countChanges(f.accountID, "trip"); n != 2 || ver != 2 {
		t.Fatalf("trip changes: %d %d", n, ver)
	}
	// 币种锁定后旅行改币种被拒
	expectStatus(t, f.patch("/trips/"+f.tripID, `"2"`, map[string]any{"currency_code": "CNY"}), http.StatusConflict, "CURRENCY_LOCKED")

	// 校验：JPY 小数、币种不符、分类无效、类型非法
	expectStatus(t, f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "expense", "amount": "10.5", "category_id": f.food}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expectStatus(t, f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "expense", "amount": "10", "currency_code": "CNY", "category_id": f.food}), http.StatusUnprocessableEntity, "CURRENCY_MISMATCH")
	expectStatus(t, f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "expense", "amount": "10", "category_id": uuid.NewString()}), http.StatusUnprocessableEntity, "INVALID_REFERENCE")
	expectStatus(t, f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "income", "amount": "10", "category_id": f.food}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 票据：有效资产、已删除资产、跨旅行资产；顺序保持
	ok1, ok2, gone := f.insertAsset(false), f.insertAsset(false), f.insertAsset(true)
	expectStatus(t, f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "expense", "amount": "10", "category_id": f.food, "attachment_asset_ids": []string{ok1, gone}}), http.StatusUnprocessableEntity, "INVALID_REFERENCE")
	withAtt := f.created(f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "expense", "amount": "800", "category_id": f.food, "occurred_on": "2026-10-02", "attachment_asset_ids": []string{ok2, ok1}}))
	if att := withAtt["attachment_asset_ids"].([]any); len(att) != 2 || att[0] != ok2 || att[1] != ok1 {
		t.Fatalf("attachment order: %v", att)
	}
	got := f.get(f.entry(withAtt["id"].(string)))
	expectStatus(t, got, http.StatusOK, "")
	if att := got.data()["attachment_asset_ids"].([]any); len(att) != 2 || att[0] != ok2 || got.Header.Get("ETag") != strongETag1 {
		t.Fatalf("get attachments/etag: %s", got.Raw)
	}
	// 替换为空数组；PATCH 合并与冲突
	cleared := f.patch(f.entry(withAtt["id"].(string)), strongETag1, map[string]any{"attachment_asset_ids": []string{}})
	expectStatus(t, cleared, http.StatusOK, "")
	if att, ok := cleared.data()["data"].(map[string]any)["attachment_asset_ids"].([]any); !ok || len(att) != 0 {
		t.Fatalf("cleared: %s", cleared.Raw)
	}
	merged := f.patch(f.entry(withAtt["id"].(string)), strongETag1, map[string]any{"notes": "晚餐"})
	expectStatus(t, merged, http.StatusOK, "")
	if w := merged.data()["warnings"].([]any); len(w) != 1 || w[0] != "MERGED_WITH_NEWER_VERSION" {
		t.Fatalf("merge: %s", merged.Raw)
	}
	conflict := f.patch(f.entry(withAtt["id"].(string)), strongETag1, map[string]any{"attachment_asset_ids": []string{ok1}})
	expectStatus(t, conflict, http.StatusPreconditionFailed, "VERSION_CONFLICT")
	if c := conflict.Body["conflict"].(map[string]any); c["conflicting_fields"].([]any)[0] != "attachment_asset_ids" || c["current"].(map[string]any)["version"] != "3" {
		t.Fatalf("conflict: %s", conflict.Raw)
	}
	expectStatus(t, f.patch(f.entry(withAtt["id"].(string)), `"3"`, map[string]any{}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expectStatus(t, f.patch(f.entry(withAtt["id"].(string)), `"3"`, map[string]any{"kind": "refund"}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 幂等重放：同键同内容 201 replayed；同键不同内容 409
	opID := uuid.NewString()
	body := map[string]any{"id": uuid.NewString(), "kind": "expense", "amount": "200", "category_id": f.lodging, "occurred_on": "2026-10-03"}
	r1 := f.do(request{method: http.MethodPost, path: f.entries(), token: f.token, body: body, headers: map[string]string{"Idempotency-Key": opID}})
	expectStatus(t, r1, http.StatusCreated, "")
	r2 := f.do(request{method: http.MethodPost, path: f.entries(), token: f.token, body: body, headers: map[string]string{"Idempotency-Key": opID}})
	expectStatus(t, r2, http.StatusCreated, "")
	if r2.data()["replayed"] != true || r2.data()["data"].(map[string]any)["id"] != body["id"] {
		t.Fatalf("replay: %s", r2.Raw)
	}
	body["amount"] = "201"
	expectStatus(t, f.do(request{method: http.MethodPost, path: f.entries(), token: f.token, body: body, headers: map[string]string{"Idempotency-Key": opID}}), http.StatusConflict, "IDEMPOTENCY_CONFLICT")
	expectStatus(t, f.post(f.entries(), map[string]any{"id": body["id"], "kind": "expense", "amount": "1", "category_id": f.food}), http.StatusConflict, "ID_ALREADY_USED")

	// 跨账号 404；旅行回收站后 410
	reg := f.registerWeb(uniqueEmail(), "correct horse battery")
	other, _ := reg.data()["access_token"].(string)
	expectStatus(t, f.do(request{method: http.MethodGet, path: f.entry(a["id"].(string)), token: other}), http.StatusNotFound, "RESOURCE_NOT_FOUND")
	expectStatus(t, f.do(request{method: http.MethodGet, path: f.path("/statistics"), token: other}), http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestHTTPLedgerRefundsAndCurrencyUnlock(t *testing.T) {
	f := newLedgerFixture(t)
	hotel := f.created(f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "expense", "amount": "10000", "category_id": f.lodging, "occurred_on": "2026-10-01"}))
	hotelID := hotel["id"].(string)

	// 退款规则：分类不一致、原支出无效、超额、支出不可关联
	expectStatus(t, f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "refund", "amount": "100", "category_id": f.food, "refunded_entry_id": hotelID}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expectStatus(t, f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "refund", "amount": "100", "category_id": f.lodging, "refunded_entry_id": uuid.NewString()}), http.StatusUnprocessableEntity, "INVALID_REFERENCE")
	expectStatus(t, f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "expense", "amount": "100", "category_id": f.lodging, "refunded_entry_id": hotelID}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	r1 := f.created(f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "refund", "amount": "6000", "category_id": f.lodging, "refunded_entry_id": hotelID, "occurred_on": "2026-10-04"}))
	expectStatus(t, f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "refund", "amount": "4001", "category_id": f.lodging, "refunded_entry_id": hotelID}), http.StatusUnprocessableEntity, "REFUND_AMOUNT_EXCEEDED")
	r2 := f.created(f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "refund", "amount": "4000", "category_id": f.lodging, "refunded_entry_id": hotelID, "occurred_on": "2026-10-05"}))
	standalone := f.created(f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": "refund", "amount": "300", "category_id": f.lodging, "occurred_on": "2026-10-06"}))

	// 原支出减额受限；改分类联动关联退款并进入 affected
	expectStatus(t, f.patch(f.entry(hotelID), strongETag1, map[string]any{"amount": "9999"}), http.StatusUnprocessableEntity, "REFUND_AMOUNT_EXCEEDED")
	recat := f.patch(f.entry(hotelID), strongETag1, map[string]any{"category_id": f.food})
	expectStatus(t, recat, http.StatusOK, "")
	affected := recat.data()["affected"].([]any)
	if len(affected) != 3 || affected[0].(map[string]any)["id"] != hotelID {
		t.Fatalf("recategorize affected: %s", recat.Raw)
	}
	for _, id := range []string{r1["id"].(string), r2["id"].(string)} {
		got := f.get(f.entry(id)).data()
		if got["category_id"] != f.food || got["version"] != "2" {
			t.Fatalf("linked refund %s should follow: %v", id, got)
		}
	}
	if got := f.get(f.entry(standalone["id"].(string))).data(); got["category_id"] != f.lodging || got["version"] != "1" {
		t.Fatalf("standalone refund must be untouched: %v", got)
	}
	// 列表按关联筛选
	linked := f.get(f.entries() + "?refunded_entry_id=" + hotelID)
	expectStatus(t, linked, http.StatusOK, "")
	if got := items(linked); len(got) != 2 || got[0]["id"] != r2["id"] || got[1]["id"] != r1["id"] {
		t.Fatalf("linked list: %s", linked.Raw)
	}
	// 退款解除关联后可改分类
	unlink := f.patch(f.entry(r2["id"].(string)), `"2"`, map[string]any{"refunded_entry_id": nil, "category_id": f.lodging})
	expectStatus(t, unlink, http.StatusOK, "")
	if d := unlink.data()["data"].(map[string]any); d["refunded_entry_id"] != nil || d["category_id"] != f.lodging {
		t.Fatalf("unlink: %s", unlink.Raw)
	}

	// 删除原支出：解除 r1 关联、警告、版本相等要求；旅行仍有账目不解锁
	expectStatus(t, f.del(f.entry(hotelID), strongETag1), http.StatusPreconditionFailed, "VERSION_CONFLICT")
	del := f.del(f.entry(hotelID), `"2"`)
	expectStatus(t, del, http.StatusOK, "")
	if w := del.data()["warnings"].([]any); len(w) != 1 || w[0] != "REFUNDS_UNLINKED" {
		t.Fatalf("delete warnings: %s", del.Raw)
	}
	if aff := del.data()["affected"].([]any); len(aff) != 2 || aff[1].(map[string]any)["id"] != r1["id"] {
		t.Fatalf("delete affected: %s", del.Raw)
	}
	if got := f.get(f.entry(r1["id"].(string))).data(); got["refunded_entry_id"] != nil || got["version"] != "3" {
		t.Fatalf("r1 should be unlinked: %v", got)
	}
	if _, locked := f.tripVersionAndLock(); !locked {
		t.Fatal("trip must stay locked while entries remain")
	}
	expectStatus(t, f.get(f.entry(hotelID)), http.StatusGone, "RESOURCE_GONE")
	expectStatus(t, f.del(f.entry(hotelID), `"3"`), http.StatusGone, "RESOURCE_GONE")
	var fields string
	if err := f.pool.QueryRow(context.Background(), `SELECT array_to_string(changed_fields, ',') FROM sync_changes WHERE account_id = $1 AND entity_id = $2 ORDER BY seq DESC LIMIT 1`, f.accountID, r1["id"]).Scan(&fields); err != nil || fields != "refunded_entry_id" {
		t.Fatalf("unlink changed_fields: %q %v", fields, err)
	}

	// 删除剩余账目 → 解锁币种，旅行进入 affected；随后可改币种
	for _, id := range []string{r1["id"].(string), r2["id"].(string), standalone["id"].(string)} {
		cur := f.get(f.entry(id)).data()
		res := f.del(f.entry(id), `"`+cur["version"].(string)+`"`)
		expectStatus(t, res, http.StatusOK, "")
		if id == standalone["id"] {
			if aff := res.data()["affected"].([]any); len(aff) != 2 || aff[1].(map[string]any)["type"] != "trip" {
				t.Fatalf("last delete should unlock trip: %s", res.Raw)
			}
		} else if aff := res.data()["affected"].([]any); len(aff) != 1 {
			t.Fatalf("intermediate delete must not touch trip: %s", res.Raw)
		}
	}
	v, locked := f.tripVersionAndLock()
	if locked || v != "3" {
		t.Fatalf("trip should be unlocked at v3: %s %v", v, locked)
	}
	expectStatus(t, f.patch("/trips/"+f.tripID, `"3"`, map[string]any{"currency_code": "CNY"}), http.StatusOK, "")

	// 回收站中的旅行：账目接口 410
	trash := f.del("/trips/"+f.tripID, `"4"`)
	expectStatus(t, trash, http.StatusOK, "")
	expectStatus(t, f.get(f.entries()), http.StatusGone, "TRIP_DELETED")
	expectStatus(t, f.get(f.path("/statistics")), http.StatusGone, "TRIP_DELETED")
}

func TestHTTPLedgerListAndStatistics(t *testing.T) {
	f := newLedgerFixture(t)
	expectStatus(t, f.patch("/trips/"+f.tripID, strongETag1, map[string]any{"budget_amount": "5000"}), http.StatusOK, "")
	mk := func(kind, amount, cat, on string) string {
		return f.created(f.post(f.entries(), map[string]any{"id": uuid.NewString(), "kind": kind, "amount": amount, "category_id": cat, "occurred_on": on}))["id"].(string)
	}
	e1 := mk("expense", "3000", f.lodging, "2026-10-01")
	e2 := mk("expense", "1000", f.food, "2026-10-02")
	e3 := mk("expense", "500", f.food, "2026-10-02")
	r1 := mk("refund", "500", f.lodging, "2026-10-03")

	// 列表：默认降序、同日 id 降序、筛选、分页游标绑定筛选
	all := items(f.get(f.entries()))
	if len(all) != 4 || all[0]["id"] != r1 || all[3]["id"] != e1 || all[1]["occurred_on"] != "2026-10-02" {
		t.Fatalf("default order: %v", all)
	}
	if all[1]["id"].(string) < all[2]["id"].(string) {
		t.Fatalf("same-day should be id desc: %v %v", all[1]["id"], all[2]["id"])
	}
	_ = e2
	_ = e3
	if got := items(f.get(f.entries() + "?kind=refund")); len(got) != 1 || got[0]["id"] != r1 {
		t.Fatalf("kind filter: %v", got)
	}
	if got := items(f.get(f.entries() + "?category_id=" + f.lodging + "&date_from=2026-10-01&date_to=2026-10-01")); len(got) != 1 || got[0]["id"] != e1 {
		t.Fatalf("category+date filter: %v", got)
	}
	expectStatus(t, f.get(f.entries()+"?date_from=2026-10-05&date_to=2026-10-01"), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expectStatus(t, f.get(f.entries()+"?kind=income"), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	page1 := f.get(f.entries() + "?limit=3")
	expectStatus(t, page1, http.StatusOK, "")
	cursor, _ := page1.Body["next_cursor"].(string)
	if len(items(page1)) != 3 || cursor == "" {
		t.Fatalf("page1: %s", page1.Raw)
	}
	page2 := f.get(f.entries() + "?limit=3&cursor=" + cursor)
	expectStatus(t, page2, http.StatusOK, "")
	if got := items(page2); len(got) != 1 || got[0]["id"] != e1 || page2.Body["next_cursor"] != nil {
		t.Fatalf("page2: %s", page2.Raw)
	}
	expectStatus(t, f.get(f.entries()+"?limit=3&kind=expense&cursor="+cursor), http.StatusBadRequest, "INVALID_CURSOR")

	// 统计：总额、预算对比、分类占比、每日
	stats := f.get(f.path("/statistics"))
	expectStatus(t, stats, http.StatusOK, "")
	s := stats.data()
	totals := s["filtered_totals"].(map[string]any)
	if totals["expense_amount"] != "4500" || totals["refund_amount"] != "500" || totals["net_amount"] != "4000" || totals["entry_count"] != float64(4) {
		t.Fatalf("totals: %v", totals)
	}
	budget := s["trip_budget"].(map[string]any)
	if budget["budget_amount"] != "5000" || budget["trip_net_amount"] != "4000" || budget["remaining_amount"] != "1000" || budget["overspent_amount"] != "0" {
		t.Fatalf("budget: %v", budget)
	}
	if s["currency_code"] != "JPY" || s["ratio_available"] != true {
		t.Fatalf("currency/ratio: %v %v", s["currency_code"], s["ratio_available"])
	}
	cats := s["by_category"].([]any)
	if len(cats) != 6 {
		t.Fatalf("all six presets expected: %d", len(cats))
	}
	var lodging, food map[string]any
	for _, c := range cats {
		m := c.(map[string]any)
		switch m["category_id"] {
		case f.lodging:
			lodging = m
		case f.food:
			food = m
		}
	}
	if lodging["net_amount"] != "2500" || lodging["share"] != 0.625 || lodging["trip_category_net_amount"] != "2500" || lodging["icon"] != "lodging" {
		t.Fatalf("lodging: %v", lodging)
	}
	if food["net_amount"] != "1500" || food["share"] != 0.375 {
		t.Fatalf("food: %v", food)
	}
	daily := s["daily"].(map[string]any)
	days := daily["items"].([]any)
	if len(days) != 3 || days[0].(map[string]any)["date"] != "2026-10-03" || days[0].(map[string]any)["net_amount"] != "-500" || days[1].(map[string]any)["expense_amount"] != "1500" {
		t.Fatalf("daily: %v", days)
	}
	scope := s["scope"].(map[string]any)
	if scope["date_from"] != nil || scope["category_id"] != nil {
		t.Fatalf("scope: %v", scope)
	}

	// 日期筛选：住宿只剩退款 → 占比不可用；预算对比不变；每日分页
	filtered := f.get(f.path("/statistics?date_from=2026-10-02&date_to=2026-10-03&daily_limit=1"))
	expectStatus(t, filtered, http.StatusOK, "")
	fs := filtered.data()
	if fs["ratio_available"] != false || fs["filtered_totals"].(map[string]any)["net_amount"] != "1000" || fs["trip_budget"].(map[string]any)["remaining_amount"] != "1000" {
		t.Fatalf("filtered: %v", fs)
	}
	if fs["scope"].(map[string]any)["date_from"] != "2026-10-02" {
		t.Fatalf("filtered scope: %v", fs["scope"])
	}
	fd := fs["daily"].(map[string]any)
	dcursor, _ := fd["next_cursor"].(string)
	if len(fd["items"].([]any)) != 1 || dcursor == "" {
		t.Fatalf("daily page1: %v", fd)
	}
	next := f.get(f.path("/statistics?date_from=2026-10-02&date_to=2026-10-03&daily_limit=1&daily_cursor=" + dcursor))
	expectStatus(t, next, http.StatusOK, "")
	nd := next.data()["daily"].(map[string]any)
	if ni := nd["items"].([]any); len(ni) != 1 || ni[0].(map[string]any)["date"] != "2026-10-02" || nd["next_cursor"] != nil {
		t.Fatalf("daily page2: %v", nd)
	}
	expectStatus(t, f.get(f.path("/statistics?daily_cursor="+dcursor)), http.StatusBadRequest, "INVALID_CURSOR")
	expectStatus(t, f.get(f.path("/statistics?daily_limit=101")), http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	// 分类筛选：已删除但被引用的分类仍出现在 by_category
	catFiltered := f.get(f.path("/statistics?category_id=" + f.food))
	expectStatus(t, catFiltered, http.StatusOK, "")
	if catFiltered.data()["filtered_totals"].(map[string]any)["net_amount"] != "1500" {
		t.Fatalf("category filtered: %s", catFiltered.Raw)
	}
	// 删除有账目引用的分类被拒（CATEGORY_IN_USE）；把账目改到别的分类后可删，删除后仍不出现在统计里
	extra := f.created(f.post("/expense-categories", map[string]any{"id": uuid.NewString(), "name": "临时"}))
	extraID := extra["id"].(string)
	e4 := mk("expense", "100", extraID, "2026-10-04")
	expectStatus(t, f.del("/expense-categories/"+extraID, strongETag1), http.StatusConflict, "CATEGORY_IN_USE")
	expectStatus(t, f.del(f.entry(e4), strongETag1), http.StatusOK, "")
	expectStatus(t, f.del("/expense-categories/"+extraID, strongETag1), http.StatusOK, "")
	after := f.get(f.path("/statistics")).data()
	if len(after["by_category"].([]any)) != 6 || after["filtered_totals"].(map[string]any)["entry_count"] != float64(4) {
		t.Fatalf("after category delete: %v", after["by_category"])
	}
}
