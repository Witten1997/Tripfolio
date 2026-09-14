package finance_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/modules/travel/trip"
)

type ledgerFixture struct {
	store    *finance.LedgerMemoryStore
	uow      *write.MemoryUnitOfWork[finance.LedgerRepo]
	svc      *finance.LedgerService
	stats    *finance.StatisticsService
	clock    *clock.Fake
	actor    actor.Actor
	tripID   uuid.UUID
	food     uuid.UUID
	lodging  uuid.UUID
	deleted  uuid.UUID
	otherAcc actor.Actor
}

func newLedgerFixture(t *testing.T) *ledgerFixture {
	t.Helper()
	store := finance.NewLedgerMemoryStore()
	uow := write.NewMemoryUnitOfWork[finance.LedgerRepo](store)
	store.SetMergeSource(uow)
	clk := clock.NewFake(time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC))
	svc := finance.NewLedgerService(uow, store, paging.InsecureCodec{}, clk)
	stats := finance.NewStatisticsService(store, paging.InsecureCodec{})
	a := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"}
	f := &ledgerFixture{
		store: store, uow: uow, svc: svc, stats: stats, clock: clk, actor: a, tripID: uuid.New(),
		food: uuid.New(), lodging: uuid.New(), deleted: uuid.New(),
		otherAcc: actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"},
	}
	now := clk.Now()
	store.PutTrip(a.AccountID, trip.Resource{
		ID: f.tripID, Name: "东京", StartDate: "2026-10-01", EndDate: "2026-10-05", Timezone: "Asia/Tokyo",
		CurrencyCode: "JPY", Version: 1, CreatedAt: now, UpdatedAt: now,
	})
	icon := func(s string) *string { return &s }
	store.PutCategory(a.AccountID, finance.CategoryResource{ID: f.food, Name: "美食", Icon: icon("food"), SortOrder: 2, IsPreset: true, Version: 1, CreatedAt: now, UpdatedAt: now})
	store.PutCategory(a.AccountID, finance.CategoryResource{ID: f.lodging, Name: "住宿", Icon: icon("lodging"), SortOrder: 1, IsPreset: true, Version: 1, CreatedAt: now, UpdatedAt: now})
	deletedAt := now.Add(-time.Hour)
	store.PutCategory(a.AccountID, finance.CategoryResource{ID: f.deleted, Name: "旧分类", SortOrder: 9, Version: 2, CreatedAt: now, UpdatedAt: now, DeletedAt: &deletedAt})
	return f
}

func str(s string) *string        { return &s }
func uid(id uuid.UUID) *uuid.UUID { return &id }

func (f *ledgerFixture) create(t *testing.T, cmd finance.CreateLedgerCommand) finance.LedgerResource {
	t.Helper()
	res, err := f.tryCreate(cmd)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return res.Data.(finance.LedgerResource)
}

func (f *ledgerFixture) tryCreate(cmd finance.CreateLedgerCommand) (write.Result, error) {
	if cmd.ID == uuid.Nil {
		cmd.ID = uuid.New()
	}
	if cmd.Kind == "" {
		cmd.Kind = "expense"
	}
	if cmd.Amount == "" {
		cmd.Amount = "1000"
	}
	if cmd.CategoryID == uuid.Nil {
		cmd.CategoryID = f.food
	}
	return f.svc.Create(context.Background(), f.actor, uuid.New(), f.tripID, cmd)
}

func (f *ledgerFixture) expense(t *testing.T, amount string, on string) finance.LedgerResource {
	t.Helper()
	return f.create(t, finance.CreateLedgerCommand{Amount: amount, OccurredOn: str(on)})
}

func (f *ledgerFixture) update(t *testing.T, id uuid.UUID, base int64, patch finance.LedgerPatch) write.Result {
	t.Helper()
	res, err := f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, id, base, patch)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	return res
}

func (f *ledgerFixture) get(t *testing.T, id uuid.UUID) finance.LedgerResource {
	t.Helper()
	r, err := f.svc.Get(context.Background(), f.actor, f.tripID, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return r
}

func expectCode(t *testing.T, err error, status int, code string) *apperr.Error {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("expected app error %d %s, got %v", status, code, err)
	}
	if e.Status != status || e.Code != code {
		t.Fatalf("expected %d %s, got %d %s (%v)", status, code, e.Status, e.Code, e)
	}
	return e
}

func fieldCodes(e *apperr.Error) string {
	var parts []string
	for _, f := range e.Fields {
		parts = append(parts, f.Field+":"+f.Code)
	}
	return strings.Join(parts, ",")
}

func hasAffected(res write.Result, entityType string, id uuid.UUID) bool {
	for _, ref := range res.Affected {
		if ref.Type == entityType && ref.ID == id {
			return true
		}
	}
	return false
}

func hasWarning(res write.Result, code string) bool {
	for _, w := range res.Warnings {
		if w == code {
			return true
		}
	}
	return false
}

func TestLedgerCreateDefaultsLocksCurrencyAndReplays(t *testing.T) {
	f := newLedgerFixture(t)
	opID := uuid.New()
	id := uuid.New()
	cmd := finance.CreateLedgerCommand{ID: id, Kind: "expense", Amount: "0128", CategoryID: f.food, Notes: str("午餐")}
	res, err := f.svc.Create(context.Background(), f.actor, opID, f.tripID, cmd)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	r := res.Data.(finance.LedgerResource)
	if r.Amount != "128" || r.CurrencyCode != "JPY" || r.OccurredOn != "2026-09-13" || r.Version != 1 || r.Notes != "午餐" {
		t.Fatalf("unexpected defaults: %+v", r)
	}
	if r.AttachmentAssetIDs == nil || len(r.AttachmentAssetIDs) != 0 || r.RefundedEntryID != nil {
		t.Fatalf("attachments should be empty array and no link: %+v", r)
	}
	if res.Primary == nil || res.Primary.Type != finance.EntityTypeLedger || !hasAffected(res, trip.EntityType, f.tripID) {
		t.Fatalf("first entry should lock trip currency and list trip in affected: %+v", res.Affected)
	}
	tr, _ := f.store.TripResource(f.tripID)
	if tr.CurrencyLockedAt == nil || tr.Version != 2 {
		t.Fatalf("trip should be locked with version 2: %+v", tr)
	}
	changes := f.uow.Changes()
	if len(changes) != 2 || changes[0].EntityType != finance.EntityTypeLedger || changes[1].EntityType != trip.EntityType {
		t.Fatalf("expected ledger + trip changes, got %+v", changes)
	}
	if changes[0].TripID == nil || *changes[0].TripID != f.tripID || len(changes[0].ChangedFields) != len(finance.LedgerFields) {
		t.Fatalf("ledger change should carry trip_id and all fields: %+v", changes[0])
	}
	if len(changes[1].ChangedFields) != 1 || changes[1].ChangedFields[0] != "currency_locked_at" {
		t.Fatalf("trip change should only list currency_locked_at: %+v", changes[1])
	}

	// 同一操作重放：金额 "128.0" 与 "0128" 指纹相同；旅行不再重复锁定。
	cmd.Amount = "128.0"
	again, err := f.svc.Create(context.Background(), f.actor, opID, f.tripID, cmd)
	if err != nil || !again.Replayed {
		t.Fatalf("replay: %v %+v", err, again)
	}
	if got := len(f.uow.Changes()); got != 2 {
		t.Fatalf("replay must not record new changes, got %d", got)
	}
	cmd.Amount = "129"
	if _, err := f.svc.Create(context.Background(), f.actor, opID, f.tripID, cmd); err == nil {
		t.Fatal("different fingerprint should conflict")
	} else {
		expectCode(t, err, 409, "IDEMPOTENCY_CONFLICT")
	}
	if _, err := f.tryCreate(finance.CreateLedgerCommand{ID: id}); err == nil {
		t.Fatal("reused id should conflict")
	} else {
		expectCode(t, err, 409, "ID_ALREADY_USED")
	}

	// 第二条账目不再改动旅行。
	second := f.tryCreateResult(t, finance.CreateLedgerCommand{Amount: "50"})
	if hasAffected(second, trip.EntityType, f.tripID) {
		t.Fatal("second entry must not touch trip")
	}
}

func (f *ledgerFixture) tryCreateResult(t *testing.T, cmd finance.CreateLedgerCommand) write.Result {
	t.Helper()
	res, err := f.tryCreate(cmd)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return res
}

func TestLedgerCreateValidation(t *testing.T) {
	f := newLedgerFixture(t)
	cases := []struct {
		name string
		cmd  finance.CreateLedgerCommand
		want string
	}{
		{"kind", finance.CreateLedgerCommand{Kind: "income"}, "kind:INVALID"},
		{"zero amount", finance.CreateLedgerCommand{Amount: "0.00"}, "amount:INVALID"},
		{"negative", finance.CreateLedgerCommand{Amount: "-5"}, "amount:INVALID"},
		{"format", finance.CreateLedgerCommand{Amount: "1e3"}, "amount:INVALID"},
		{"currency format", finance.CreateLedgerCommand{CurrencyCode: str("XXX")}, "currency_code:INVALID"},
		{"date", finance.CreateLedgerCommand{OccurredOn: str("2026-1-5")}, "occurred_on:INVALID"},
		{"notes", finance.CreateLedgerCommand{Notes: str(strings.Repeat("字", 4001))}, "notes:TOO_LONG"},
		{"expense with link", finance.CreateLedgerCommand{RefundedEntryID: uid(uuid.New())}, "refunded_entry_id:NOT_ALLOWED"},
		{"too many attachments", finance.CreateLedgerCommand{AttachmentAssetIDs: make11()}, "attachment_asset_ids:TOO_MANY"},
		{"duplicate attachment", finance.CreateLedgerCommand{AttachmentAssetIDs: dupIDs()}, "attachment_asset_ids[1]:DUPLICATE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.tryCreate(c.cmd)
			e := expectCode(t, err, 422, "VALIDATION_FAILED")
			if got := fieldCodes(e); got != c.want {
				t.Fatalf("got %s want %s", got, c.want)
			}
		})
	}
	selfID := uuid.New()
	_, err := f.tryCreate(finance.CreateLedgerCommand{ID: selfID, Kind: "refund", RefundedEntryID: &selfID})
	if e := expectCode(t, err, 422, "VALIDATION_FAILED"); fieldCodes(e) != "refunded_entry_id:INVALID" {
		t.Fatalf("self link: %s", fieldCodes(e))
	}
	if got := len(f.uow.Changes()); got != 0 {
		t.Fatalf("validation failures must not write, got %d changes", got)
	}
}

func make11() []uuid.UUID {
	out := make([]uuid.UUID, 11)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

func dupIDs() []uuid.UUID {
	id := uuid.New()
	return []uuid.UUID{id, id}
}

func TestLedgerCreateCurrencyAndScale(t *testing.T) {
	f := newLedgerFixture(t)
	// JPY 没有小数位：小数金额被拒绝；币种与旅行不符返回 CURRENCY_MISMATCH。
	_, err := f.tryCreate(finance.CreateLedgerCommand{Amount: "10.5"})
	if e := expectCode(t, err, 422, "VALIDATION_FAILED"); fieldCodes(e) != "amount:INVALID" {
		t.Fatalf("scale: %s", fieldCodes(e))
	}
	_, err = f.tryCreate(finance.CreateLedgerCommand{Amount: "10", CurrencyCode: str("CNY")})
	expectCode(t, err, 422, "CURRENCY_MISMATCH")
	r := f.create(t, finance.CreateLedgerCommand{Amount: "10.00", CurrencyCode: str("JPY")})
	if r.Amount != "10" {
		t.Fatalf("JPY amount should canonicalize to integer, got %s", r.Amount)
	}
	tr, _ := f.store.TripResource(f.tripID)
	if tr.CurrencyLockedAt == nil {
		t.Fatal("trip should be locked")
	}
}

func TestLedgerCreateReferences(t *testing.T) {
	f := newLedgerFixture(t)
	_, err := f.tryCreate(finance.CreateLedgerCommand{CategoryID: uuid.New()})
	expectCode(t, err, 422, "INVALID_REFERENCE")
	_, err = f.tryCreate(finance.CreateLedgerCommand{CategoryID: f.deleted})
	expectCode(t, err, 422, "INVALID_REFERENCE")

	ok1, ok2, gone, foreign := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.store.PutAsset(f.actor.AccountID, f.tripID, ok1, false)
	f.store.PutAsset(f.actor.AccountID, f.tripID, ok2, false)
	f.store.PutAsset(f.actor.AccountID, f.tripID, gone, true)
	f.store.PutAsset(f.actor.AccountID, uuid.New(), foreign, false)
	for _, bad := range []uuid.UUID{gone, foreign, uuid.New()} {
		_, err := f.tryCreate(finance.CreateLedgerCommand{AttachmentAssetIDs: []uuid.UUID{ok1, bad}})
		expectCode(t, err, 422, "INVALID_REFERENCE")
	}
	r := f.create(t, finance.CreateLedgerCommand{AttachmentAssetIDs: []uuid.UUID{ok2, ok1}})
	if len(r.AttachmentAssetIDs) != 2 || r.AttachmentAssetIDs[0] != ok2 || r.AttachmentAssetIDs[1] != ok1 {
		t.Fatalf("attachment order must be preserved: %v", r.AttachmentAssetIDs)
	}
	if got := len(f.uow.Changes()); got != 2 {
		t.Fatalf("only the successful create should write (ledger + trip lock), got %d", got)
	}
	tr, _ := f.store.TripResource(f.tripID)
	if tr.Version != 2 {
		t.Fatalf("failed creates must roll back the trip lock, version %d", tr.Version)
	}
}

func TestLedgerTripGuards(t *testing.T) {
	f := newLedgerFixture(t)
	_, err := f.svc.Create(context.Background(), f.otherAcc, uuid.New(), f.tripID, finance.CreateLedgerCommand{ID: uuid.New(), Kind: "expense", Amount: "1", CategoryID: f.food})
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
	_, err = f.svc.List(context.Background(), f.actor, uuid.New(), finance.LedgerFilters{})
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
	r := f.expense(t, "100", "2026-10-01")
	_, err = f.svc.Get(context.Background(), f.otherAcc, f.tripID, r.ID)
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")

	trashed := uuid.New()
	deletedAt := f.clock.Now()
	f.store.PutTrip(f.actor.AccountID, trip.Resource{ID: trashed, Timezone: "Asia/Tokyo", CurrencyCode: "JPY", Version: 1, DeletedAt: &deletedAt})
	_, err = f.svc.Create(context.Background(), f.actor, uuid.New(), trashed, finance.CreateLedgerCommand{ID: uuid.New(), Kind: "expense", Amount: "1", CategoryID: f.food})
	expectCode(t, err, 410, "TRIP_DELETED")
	_, err = f.stats.Get(context.Background(), f.actor, trashed, finance.StatisticsFilters{})
	expectCode(t, err, 410, "TRIP_DELETED")
	_, err = f.stats.Get(context.Background(), f.otherAcc, f.tripID, finance.StatisticsFilters{})
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
}

func TestRefundRules(t *testing.T) {
	f := newLedgerFixture(t)
	hotel := f.create(t, finance.CreateLedgerCommand{Amount: "10000", CategoryID: f.lodging, OccurredOn: str("2026-10-01")})
	// 分类不一致
	_, err := f.tryCreate(finance.CreateLedgerCommand{Kind: "refund", Amount: "100", CategoryID: f.food, RefundedEntryID: uid(hotel.ID)})
	if e := expectCode(t, err, 422, "VALIDATION_FAILED"); fieldCodes(e) != "category_id:REFUND_CATEGORY_MISMATCH" {
		t.Fatalf("category mismatch: %s", fieldCodes(e))
	}
	// 原支出不存在、跨旅行、不是支出
	_, err = f.tryCreate(finance.CreateLedgerCommand{Kind: "refund", Amount: "100", CategoryID: f.lodging, RefundedEntryID: uid(uuid.New())})
	expectCode(t, err, 422, "INVALID_REFERENCE")
	standalone := f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "300", CategoryID: f.lodging})
	_, err = f.tryCreate(finance.CreateLedgerCommand{Kind: "refund", Amount: "100", CategoryID: f.lodging, RefundedEntryID: uid(standalone.ID)})
	expectCode(t, err, 422, "INVALID_REFERENCE")
	// 超额：6000 + 4001 > 10000
	r1 := f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "6000", CategoryID: f.lodging, RefundedEntryID: uid(hotel.ID)})
	_, err = f.tryCreate(finance.CreateLedgerCommand{Kind: "refund", Amount: "4001", CategoryID: f.lodging, RefundedEntryID: uid(hotel.ID)})
	expectCode(t, err, 422, "REFUND_AMOUNT_EXCEEDED")
	r2 := f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "4000", CategoryID: f.lodging, RefundedEntryID: uid(hotel.ID)})

	// 原支出减额须仍覆盖关联退款
	_, err = f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, hotel.ID, 1, finance.LedgerPatch{Amount: str("9999")})
	expectCode(t, err, 422, "REFUND_AMOUNT_EXCEEDED")
	res := f.update(t, hotel.ID, 1, finance.LedgerPatch{Amount: str("10000.0")})
	if res.Data.(finance.LedgerResource).Version != 2 || res.Data.(finance.LedgerResource).Amount != "10000" {
		t.Fatalf("no-op amount change should still bump version: %+v", res.Data)
	}

	// 修改原支出分类：关联退款同事务更新并进入 affected；独立退款不受影响
	res = f.update(t, hotel.ID, 2, finance.LedgerPatch{CategoryID: uid(f.food)})
	if !hasAffected(res, finance.EntityTypeLedger, r1.ID) || !hasAffected(res, finance.EntityTypeLedger, r2.ID) || hasAffected(res, finance.EntityTypeLedger, standalone.ID) {
		t.Fatalf("linked refunds must be affected: %+v", res.Affected)
	}
	if got := f.get(t, r1.ID); got.CategoryID != f.food || got.Version != 2 {
		t.Fatalf("linked refund should follow category with version bump: %+v", got)
	}
	if got := f.get(t, standalone.ID); got.CategoryID != f.lodging || got.Version != 1 {
		t.Fatalf("standalone refund must be untouched: %+v", got)
	}
	changes := f.uow.Changes()
	last := changes[len(changes)-1]
	if last.EntityID != hotel.ID {
		t.Fatalf("primary change should be recorded last: %+v", last)
	}
	for _, c := range changes[len(changes)-3 : len(changes)-1] {
		if c.EntityType != finance.EntityTypeLedger || len(c.ChangedFields) != 1 || c.ChangedFields[0] != "category_id" {
			t.Fatalf("refund change should list only category_id: %+v", c)
		}
	}

	// 退款改金额：排除自身后再校验；改分类使其不一致被拒绝；解除关联后可任意分类
	_, err = f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, r2.ID, 2, finance.LedgerPatch{Amount: str("4001")})
	expectCode(t, err, 422, "REFUND_AMOUNT_EXCEEDED")
	f.update(t, r2.ID, 2, finance.LedgerPatch{Amount: str("3999")})
	_, err = f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, r2.ID, 3, finance.LedgerPatch{CategoryID: uid(f.lodging)})
	if e := expectCode(t, err, 422, "VALIDATION_FAILED"); fieldCodes(e) != "category_id:REFUND_CATEGORY_MISMATCH" {
		t.Fatalf("refund category change: %s", fieldCodes(e))
	}
	res = f.update(t, r2.ID, 3, finance.LedgerPatch{RefundedSet: true, RefundedEntryID: nil, CategoryID: uid(f.lodging)})
	if got := res.Data.(finance.LedgerResource); got.RefundedEntryID != nil || got.CategoryID != f.lodging {
		t.Fatalf("unlink with category: %+v", got)
	}
	// 重新关联到另一笔支出时按新原支出校验
	other := f.create(t, finance.CreateLedgerCommand{Amount: "500", CategoryID: f.lodging})
	_, err = f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, r2.ID, 4, finance.LedgerPatch{RefundedSet: true, RefundedEntryID: uid(other.ID)})
	expectCode(t, err, 422, "REFUND_AMOUNT_EXCEEDED")
	f.update(t, r2.ID, 4, finance.LedgerPatch{RefundedSet: true, RefundedEntryID: uid(other.ID), Amount: str("500")})
	// 支出不能被设置关联
	_, err = f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, hotel.ID, 3, finance.LedgerPatch{RefundedSet: true, RefundedEntryID: uid(other.ID)})
	if e := expectCode(t, err, 422, "VALIDATION_FAILED"); fieldCodes(e) != "refunded_entry_id:NOT_ALLOWED" {
		t.Fatalf("expense link: %s", fieldCodes(e))
	}
}

func TestLedgerUpdateMergeAndConflicts(t *testing.T) {
	f := newLedgerFixture(t)
	r := f.expense(t, "100", "2026-10-01")
	f.update(t, r.ID, 1, finance.LedgerPatch{Notes: str("A")}) // v2
	// 基线落后但字段不相交：合并并警告
	res := f.update(t, r.ID, 1, finance.LedgerPatch{OccurredOn: str("2026-10-02")})
	if !hasWarning(res, write.WarnMergedWithNewerVersion) || res.Data.(finance.LedgerResource).Version != 3 {
		t.Fatalf("expected merge warning and v3: %+v", res)
	}
	// 相交字段：412 带 conflicting_fields 与当前资源
	_, err := f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, r.ID, 1, finance.LedgerPatch{Notes: str("B")})
	e := expectCode(t, err, 412, "VERSION_CONFLICT")
	if e.Conflict == nil || len(e.Conflict.ConflictingFields) != 1 || e.Conflict.ConflictingFields[0] != "notes" || e.Conflict.CurrentVersion != 3 {
		t.Fatalf("conflict payload: %+v", e.Conflict)
	}
	if cur, ok := e.Conflict.Current.(finance.LedgerResource); !ok || cur.Version != 3 {
		t.Fatalf("conflict current: %+v", e.Conflict.Current)
	}
	// 基线超前
	_, err = f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, r.ID, 9, finance.LedgerPatch{Notes: str("C")})
	expectCode(t, err, 412, "VERSION_CONFLICT")
	// 空补丁、币种不符、无效值
	_, err = f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, r.ID, 3, finance.LedgerPatch{})
	expectCode(t, err, 422, "VALIDATION_FAILED")
	_, err = f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, r.ID, 3, finance.LedgerPatch{Amount: str("150"), CurrencyCode: str("CNY")})
	expectCode(t, err, 422, "CURRENCY_MISMATCH")
	_, err = f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, r.ID, 3, finance.LedgerPatch{Amount: str("0")})
	expectCode(t, err, 422, "VALIDATION_FAILED")
	_, err = f.svc.Update(context.Background(), f.actor, uuid.New(), f.tripID, r.ID, 3, finance.LedgerPatch{CategoryID: uid(f.deleted)})
	expectCode(t, err, 422, "INVALID_REFERENCE")
	// 附件整体替换
	a1 := uuid.New()
	f.store.PutAsset(f.actor.AccountID, f.tripID, a1, false)
	res = f.update(t, r.ID, 3, finance.LedgerPatch{AttachmentAssetIDs: &[]uuid.UUID{a1}})
	if got := res.Data.(finance.LedgerResource); len(got.AttachmentAssetIDs) != 1 {
		t.Fatalf("attachments: %+v", got)
	}
	res = f.update(t, r.ID, 4, finance.LedgerPatch{AttachmentAssetIDs: &[]uuid.UUID{}})
	if got := res.Data.(finance.LedgerResource); len(got.AttachmentAssetIDs) != 0 || got.AttachmentAssetIDs == nil {
		t.Fatalf("cleared attachments should be empty array: %+v", got)
	}
	// 幂等重放
	opID := uuid.New()
	first, err := f.svc.Update(context.Background(), f.actor, opID, f.tripID, r.ID, 5, finance.LedgerPatch{Amount: str("200.00")})
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.svc.Update(context.Background(), f.actor, opID, f.tripID, r.ID, 5, finance.LedgerPatch{Amount: str("200")})
	if err != nil || !again.Replayed || again.Data.(finance.LedgerResource).Version != first.Data.(finance.LedgerResource).Version {
		t.Fatalf("replay: %v %+v", err, again)
	}
	// 跨账号 404、已删除 410
	_, err = f.svc.Update(context.Background(), f.otherAcc, uuid.New(), f.tripID, r.ID, 6, finance.LedgerPatch{Notes: str("x")})
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
}

func TestLedgerDeleteUnlinksRefundsAndUnlocksCurrency(t *testing.T) {
	f := newLedgerFixture(t)
	hotel := f.create(t, finance.CreateLedgerCommand{Amount: "10000", CategoryID: f.lodging})
	r1 := f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "1000", CategoryID: f.lodging, RefundedEntryID: uid(hotel.ID)})
	standalone := f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "5", CategoryID: f.lodging})

	_, err := f.svc.Delete(context.Background(), f.actor, uuid.New(), f.tripID, hotel.ID, 2)
	expectCode(t, err, 412, "VERSION_CONFLICT")
	res, err := f.svc.Delete(context.Background(), f.actor, uuid.New(), f.tripID, hotel.ID, 1)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !hasWarning(res, write.WarnRefundsUnlinked) || !hasAffected(res, finance.EntityTypeLedger, r1.ID) || hasAffected(res, finance.EntityTypeLedger, standalone.ID) {
		t.Fatalf("delete result: %+v", res)
	}
	if res.Affected[0].ID != hotel.ID || res.Data.(finance.LedgerResource).DeletedAt == nil {
		t.Fatalf("primary first and data deleted: %+v", res)
	}
	if got := f.get(t, r1.ID); got.RefundedEntryID != nil || got.Version != 2 {
		t.Fatalf("refund should be unlinked: %+v", got)
	}
	if hasAffected(res, trip.EntityType, f.tripID) {
		t.Fatal("trip still has entries; must not unlock")
	}
	changes := f.uow.Changes()
	unlink := changes[len(changes)-2]
	if unlink.EntityID != r1.ID || len(unlink.ChangedFields) != 1 || unlink.ChangedFields[0] != "refunded_entry_id" {
		t.Fatalf("unlink change: %+v", unlink)
	}
	if del := changes[len(changes)-1]; del.Kind != write.ChangeDelete || del.EntityID != hotel.ID {
		t.Fatalf("delete change: %+v", del)
	}
	_, err = f.svc.Get(context.Background(), f.actor, f.tripID, hotel.ID)
	expectCode(t, err, 410, "RESOURCE_GONE")
	_, err = f.svc.Delete(context.Background(), f.actor, uuid.New(), f.tripID, hotel.ID, 2)
	expectCode(t, err, 410, "RESOURCE_GONE")
	// 删除的支出 ID 不可复用；退款不能再关联它
	_, err = f.tryCreate(finance.CreateLedgerCommand{ID: hotel.ID})
	expectCode(t, err, 409, "ID_ALREADY_USED")
	_, err = f.tryCreate(finance.CreateLedgerCommand{Kind: "refund", Amount: "1", CategoryID: f.lodging, RefundedEntryID: uid(hotel.ID)})
	expectCode(t, err, 422, "INVALID_REFERENCE")

	// 删除剩余账目后解锁币种
	for _, r := range []finance.LedgerResource{f.get(t, r1.ID), standalone} {
		res, err := f.svc.Delete(context.Background(), f.actor, uuid.New(), f.tripID, r.ID, int64(r.Version))
		if err != nil {
			t.Fatalf("delete %s: %v", r.ID, err)
		}
		if hasWarning(res, write.WarnRefundsUnlinked) {
			t.Fatal("deleting refunds must not warn")
		}
		tr, _ := f.store.TripResource(f.tripID)
		if r.ID == standalone.ID {
			if tr.CurrencyLockedAt != nil || !hasAffected(res, trip.EntityType, f.tripID) || tr.Version != 3 {
				t.Fatalf("last delete should unlock trip: %+v %+v", tr, res.Affected)
			}
		} else if tr.CurrencyLockedAt == nil || hasAffected(res, trip.EntityType, f.tripID) {
			t.Fatalf("intermediate delete must keep lock: %+v", tr)
		}
	}
	// 解锁后重新记账再次锁定
	f.expense(t, "1", "2026-10-03")
	if tr, _ := f.store.TripResource(f.tripID); tr.CurrencyLockedAt == nil || tr.Version != 4 {
		t.Fatalf("relock: %+v", tr)
	}
}

func TestLedgerListFiltersAndCursor(t *testing.T) {
	f := newLedgerFixture(t)
	e1 := f.create(t, finance.CreateLedgerCommand{Amount: "100", OccurredOn: str("2026-10-01"), CategoryID: f.food})
	e2 := f.create(t, finance.CreateLedgerCommand{Amount: "200", OccurredOn: str("2026-10-02"), CategoryID: f.lodging})
	e3 := f.create(t, finance.CreateLedgerCommand{Amount: "300", OccurredOn: str("2026-10-02"), CategoryID: f.food})
	r1 := f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "50", OccurredOn: str("2026-09-30"), CategoryID: f.food, RefundedEntryID: uid(e1.ID)})
	list := func(fl finance.LedgerFilters) []finance.LedgerResource {
		t.Helper()
		page, err := f.svc.List(context.Background(), f.actor, f.tripID, fl)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		return page.Items
	}
	all := list(finance.LedgerFilters{})
	if len(all) != 4 || all[3].ID != r1.ID || all[2].ID != e1.ID {
		t.Fatalf("default order should be occurred_on desc: %v", ids(all))
	}
	if all[0].OccurredOn != "2026-10-02" || all[1].OccurredOn != "2026-10-02" || strings.Compare(all[0].ID.String(), all[1].ID.String()) < 0 {
		t.Fatalf("same day should be id desc: %v", ids(all))
	}
	if got := list(finance.LedgerFilters{Kind: "refund"}); len(got) != 1 || got[0].ID != r1.ID {
		t.Fatalf("kind filter: %v", ids(got))
	}
	if got := list(finance.LedgerFilters{CategoryID: uid(f.lodging)}); len(got) != 1 || got[0].ID != e2.ID {
		t.Fatalf("category filter: %v", ids(got))
	}
	if got := list(finance.LedgerFilters{RefundedEntryID: uid(e1.ID)}); len(got) != 1 || got[0].ID != r1.ID {
		t.Fatalf("refunded filter: %v", ids(got))
	}
	if got := list(finance.LedgerFilters{DateFrom: "2026-10-01", DateTo: "2026-10-01"}); len(got) != 1 || got[0].ID != e1.ID {
		t.Fatalf("date filter: %v", ids(got))
	}
	_ = e3
	_, err := f.svc.List(context.Background(), f.actor, f.tripID, finance.LedgerFilters{DateFrom: "2026-10-02", DateTo: "2026-10-01"})
	if e := expectCode(t, err, 422, "VALIDATION_FAILED"); fieldCodes(e) != "date_to:DATE_ORDER" {
		t.Fatalf("range: %s", fieldCodes(e))
	}
	_, err = f.svc.List(context.Background(), f.actor, f.tripID, finance.LedgerFilters{Kind: "x", Limit: 101})
	if e := expectCode(t, err, 422, "VALIDATION_FAILED"); fieldCodes(e) != "kind:INVALID,limit:INVALID" {
		t.Fatalf("invalid filters: %s", fieldCodes(e))
	}

	page1, err := f.svc.List(context.Background(), f.actor, f.tripID, finance.LedgerFilters{Limit: 3})
	if err != nil || len(page1.Items) != 3 || page1.NextCursor == nil {
		t.Fatalf("page1: %v %+v", err, page1)
	}
	page2, err := f.svc.List(context.Background(), f.actor, f.tripID, finance.LedgerFilters{Limit: 3, Cursor: *page1.NextCursor})
	if err != nil || len(page2.Items) != 1 || page2.Items[0].ID != r1.ID || page2.NextCursor != nil {
		t.Fatalf("page2: %v %+v", err, page2)
	}
	// 游标绑定筛选与账号
	_, err = f.svc.List(context.Background(), f.actor, f.tripID, finance.LedgerFilters{Limit: 3, Cursor: *page1.NextCursor, Kind: "expense"})
	expectCode(t, err, 400, "INVALID_CURSOR")
	_, err = f.svc.List(context.Background(), f.actor, f.tripID, finance.LedgerFilters{Cursor: "garbage"})
	expectCode(t, err, 400, "INVALID_CURSOR")
}

func ids(rows []finance.LedgerResource) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.OccurredOn.String() + "/" + r.ID.String()[:8]
	}
	return out
}

func (f *ledgerFixture) statistics(t *testing.T, fl finance.StatisticsFilters) finance.Statistics {
	t.Helper()
	s, err := f.stats.Get(context.Background(), f.actor, f.tripID, fl)
	if err != nil {
		t.Fatalf("statistics: %v", err)
	}
	return s
}

func (f *ledgerFixture) setBudget(budget *string) {
	tr, _ := f.store.TripResource(f.tripID)
	tr.BudgetAmount = budget
	f.store.PutTrip(f.actor.AccountID, tr)
}

func TestStatisticsEmptyTrip(t *testing.T) {
	f := newLedgerFixture(t)
	s := f.statistics(t, finance.StatisticsFilters{})
	if s.CurrencyCode != "JPY" || s.FilteredTotals.NetAmount != "0" || s.FilteredTotals.EntryCount != 0 || s.RatioAvailable {
		t.Fatalf("empty totals: %+v", s.FilteredTotals)
	}
	if s.TripBudget.BudgetAmount != nil || s.TripBudget.RemainingAmount != nil || s.TripBudget.OverspentAmount != nil || s.TripBudget.TripNetAmount != "0" {
		t.Fatalf("no budget: %+v", s.TripBudget)
	}
	// 只列有效分类（已删除且未被引用的不出现），按 sort_order
	if len(s.ByCategory) != 2 || s.ByCategory[0].CategoryID != f.lodging || s.ByCategory[1].CategoryID != f.food || s.ByCategory[0].Share != nil {
		t.Fatalf("categories: %+v", s.ByCategory)
	}
	if len(s.Daily.Items) != 0 || s.Daily.NextCursor != nil || s.Scope.DateFrom != nil || s.Scope.CategoryID != nil {
		t.Fatalf("daily/scope: %+v %+v", s.Daily, s.Scope)
	}
}

func TestStatisticsTotalsBudgetAndShares(t *testing.T) {
	f := newLedgerFixture(t)
	f.setBudget(str("5000"))
	f.create(t, finance.CreateLedgerCommand{Amount: "3000", CategoryID: f.lodging, OccurredOn: str("2026-10-01")})
	f.create(t, finance.CreateLedgerCommand{Amount: "1000", CategoryID: f.food, OccurredOn: str("2026-10-02")})
	f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "500", CategoryID: f.lodging, OccurredOn: str("2026-10-03")})
	// 被引用的已删除分类也要出现
	f.store.PutCategory(f.actor.AccountID, finance.CategoryResource{ID: f.deleted, Name: "旧分类", SortOrder: 9, Version: 1})
	f.create(t, finance.CreateLedgerCommand{Amount: "2500", CategoryID: f.deleted, OccurredOn: str("2026-09-20")})
	gone := f.clock.Now()
	f.store.PutCategory(f.actor.AccountID, finance.CategoryResource{ID: f.deleted, Name: "旧分类", SortOrder: 9, Version: 2, DeletedAt: &gone})

	s := f.statistics(t, finance.StatisticsFilters{})
	if s.FilteredTotals.ExpenseAmount != "6500" || s.FilteredTotals.RefundAmount != "500" || s.FilteredTotals.NetAmount != "6000" || s.FilteredTotals.EntryCount != 4 {
		t.Fatalf("totals: %+v", s.FilteredTotals)
	}
	if *s.TripBudget.BudgetAmount != "5000" || s.TripBudget.TripNetAmount != "6000" || *s.TripBudget.RemainingAmount != "-1000" || *s.TripBudget.OverspentAmount != "1000" {
		t.Fatalf("budget: %+v", s.TripBudget)
	}
	if !s.RatioAvailable || len(s.ByCategory) != 3 {
		t.Fatalf("ratio/categories: %v %+v", s.RatioAvailable, s.ByCategory)
	}
	lodging, food, old := s.ByCategory[0], s.ByCategory[1], s.ByCategory[2]
	if lodging.NetAmount != "2500" || lodging.ExpenseAmount != "3000" || lodging.RefundAmount != "500" || *lodging.Share != 0.4167 || lodging.TripCategoryNetAmount != "2500" {
		t.Fatalf("lodging: %+v", lodging)
	}
	if food.NetAmount != "1000" || *food.Share != 0.1667 {
		t.Fatalf("food: %+v", food)
	}
	if old.CategoryID != f.deleted || old.NetAmount != "2500" || *old.Share != 0.4167 || old.Name != "旧分类" {
		t.Fatalf("deleted-but-referenced: %+v", old)
	}
	if len(s.Daily.Items) != 4 || s.Daily.Items[0].Date != "2026-10-03" || s.Daily.Items[0].NetAmount != "-500" || s.Daily.Items[3].Date != "2026-09-20" {
		t.Fatalf("daily: %+v", s.Daily.Items)
	}

	// 预算充足时剩余为正、超支为 0
	f.setBudget(str("9000"))
	s = f.statistics(t, finance.StatisticsFilters{})
	if *s.TripBudget.RemainingAmount != "3000" || *s.TripBudget.OverspentAmount != "0" {
		t.Fatalf("budget surplus: %+v", s.TripBudget)
	}

	// 日期筛选只影响筛选合计、分类筛选金额与每日；预算对比不变
	s = f.statistics(t, finance.StatisticsFilters{DateFrom: "2026-10-02", DateTo: "2026-10-03"})
	if s.FilteredTotals.NetAmount != "500" || s.FilteredTotals.EntryCount != 2 || s.TripBudget.TripNetAmount != "6000" {
		t.Fatalf("date filtered: %+v %+v", s.FilteredTotals, s.TripBudget)
	}
	if *s.Scope.DateFrom != "2026-10-02" || *s.Scope.DateTo != "2026-10-03" {
		t.Fatalf("scope: %+v", s.Scope)
	}
	// 住宿在筛选范围内只有退款：净额为负 → 占比不可用，但整趟分类净额仍为 2500
	if s.RatioAvailable || s.ByCategory[0].NetAmount != "-500" || s.ByCategory[0].TripCategoryNetAmount != "2500" || s.ByCategory[0].Share != nil || s.ByCategory[1].Share != nil {
		t.Fatalf("negative category should disable ratio: %v %+v", s.RatioAvailable, s.ByCategory)
	}
	if len(s.Daily.Items) != 2 {
		t.Fatalf("daily filtered: %+v", s.Daily.Items)
	}

	// 分类筛选
	s = f.statistics(t, finance.StatisticsFilters{CategoryID: uid(f.food)})
	if s.FilteredTotals.NetAmount != "1000" || s.FilteredTotals.EntryCount != 1 || !s.RatioAvailable || *s.ByCategory[1].Share != 1 || *s.ByCategory[0].Share != 0 {
		t.Fatalf("category filtered: %+v %+v", s.FilteredTotals, s.ByCategory)
	}
	if *s.Scope.CategoryID != f.food || len(s.Daily.Items) != 1 {
		t.Fatalf("category scope/daily: %+v %+v", s.Scope, s.Daily.Items)
	}

	// 总净额不大于 0 时占比不可用
	f.create(t, finance.CreateLedgerCommand{Kind: "refund", Amount: "6000", CategoryID: f.food, OccurredOn: str("2026-10-04")})
	s = f.statistics(t, finance.StatisticsFilters{})
	if s.FilteredTotals.NetAmount != "0" || s.RatioAvailable || s.ByCategory[1].Share != nil {
		t.Fatalf("zero net: %+v %v", s.FilteredTotals, s.RatioAvailable)
	}
}

func TestStatisticsDailyPagingAndValidation(t *testing.T) {
	f := newLedgerFixture(t)
	for i := 1; i <= 5; i++ {
		f.create(t, finance.CreateLedgerCommand{Amount: "100", OccurredOn: str("2026-10-0" + string(rune('0'+i)))})
	}
	s := f.statistics(t, finance.StatisticsFilters{DailyLimit: 2})
	if len(s.Daily.Items) != 2 || s.Daily.NextCursor == nil || s.Daily.Items[0].Date != "2026-10-05" || s.Daily.Items[1].Date != "2026-10-04" {
		t.Fatalf("page1: %+v", s.Daily)
	}
	if s.FilteredTotals.EntryCount != 5 {
		t.Fatalf("totals must not be paged: %+v", s.FilteredTotals)
	}
	s2 := f.statistics(t, finance.StatisticsFilters{DailyLimit: 2, DailyCursor: *s.Daily.NextCursor})
	if len(s2.Daily.Items) != 2 || s2.Daily.Items[0].Date != "2026-10-03" || s2.Daily.NextCursor == nil {
		t.Fatalf("page2: %+v", s2.Daily)
	}
	s3 := f.statistics(t, finance.StatisticsFilters{DailyLimit: 2, DailyCursor: *s2.Daily.NextCursor})
	if len(s3.Daily.Items) != 1 || s3.Daily.Items[0].Date != "2026-10-01" || s3.Daily.NextCursor != nil {
		t.Fatalf("page3: %+v", s3.Daily)
	}
	// 游标绑定筛选
	_, err := f.stats.Get(context.Background(), f.actor, f.tripID, finance.StatisticsFilters{DailyLimit: 2, DailyCursor: *s.Daily.NextCursor, DateFrom: "2026-10-01"})
	expectCode(t, err, 400, "INVALID_CURSOR")
	_, err = f.stats.Get(context.Background(), f.actor, f.tripID, finance.StatisticsFilters{DailyLimit: 101})
	if e := expectCode(t, err, 422, "VALIDATION_FAILED"); fieldCodes(e) != "daily_limit:INVALID" {
		t.Fatalf("daily_limit: %s", fieldCodes(e))
	}
	_, err = f.stats.Get(context.Background(), f.actor, f.tripID, finance.StatisticsFilters{DateFrom: "2026-13-01", DateTo: "x"})
	if e := expectCode(t, err, 422, "VALIDATION_FAILED"); fieldCodes(e) != "date_from:INVALID,date_to:INVALID" {
		t.Fatalf("dates: %s", fieldCodes(e))
	}
	_, err = f.stats.Get(context.Background(), f.actor, f.tripID, finance.StatisticsFilters{DateFrom: "2026-10-05", DateTo: "2026-10-01"})
	expectCode(t, err, 422, "VALIDATION_FAILED")
	// 默认页大小 31
	s = f.statistics(t, finance.StatisticsFilters{})
	if len(s.Daily.Items) != 5 || s.Daily.NextCursor != nil {
		t.Fatalf("default daily: %+v", s.Daily)
	}
}

func TestStatisticsCNYFormatting(t *testing.T) {
	f := newLedgerFixture(t)
	cny := uuid.New()
	now := f.clock.Now()
	f.store.PutTrip(f.actor.AccountID, trip.Resource{ID: cny, Timezone: "Asia/Shanghai", CurrencyCode: "CNY", BudgetAmount: str("100.00"), Version: 1, CreatedAt: now, UpdatedAt: now})
	for _, c := range []struct {
		kind, amount string
	}{{"expense", "30.5"}, {"expense", "0.01"}, {"refund", "10"}} {
		if _, err := f.svc.Create(context.Background(), f.actor, uuid.New(), cny, finance.CreateLedgerCommand{ID: uuid.New(), Kind: c.kind, Amount: c.amount, CategoryID: f.food}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := f.stats.Get(context.Background(), f.actor, cny, finance.StatisticsFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if s.FilteredTotals.ExpenseAmount != "30.51" || s.FilteredTotals.RefundAmount != "10.00" || s.FilteredTotals.NetAmount != "20.51" {
		t.Fatalf("cny totals: %+v", s.FilteredTotals)
	}
	if *s.TripBudget.RemainingAmount != "79.49" || *s.TripBudget.OverspentAmount != "0.00" || s.ByCategory[1].NetAmount != "20.51" || *s.ByCategory[1].Share != 1 {
		t.Fatalf("cny budget/category: %+v %+v", s.TripBudget, s.ByCategory)
	}
	if s.Daily.Items[0].Date != types.Date("2026-09-13") || s.Daily.Items[0].NetAmount != "20.51" {
		t.Fatalf("daily default date should be trip-local today: %+v", s.Daily.Items)
	}
}
