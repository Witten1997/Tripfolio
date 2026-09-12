package trip_test

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
	"tripfolio/server/internal/modules/travel/trip"
)

type fixture struct {
	store *trip.MemoryStore
	uow   *write.MemoryUnitOfWork[trip.Repo]
	svc   *trip.Service
	clock *clock.Fake
	actor actor.Actor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := trip.NewMemoryStore()
	uow := write.NewMemoryUnitOfWork[trip.Repo](store)
	store.SetMergeSource(uow)
	clk := clock.NewFake(time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC))
	svc := trip.NewService(uow, store, paging.InsecureCodec{}, clk, 5*time.Minute)
	return &fixture{store: store, uow: uow, svc: svc, clock: clk, actor: actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"}}
}

func str(s string) *string { return &s }

func (f *fixture) create(t *testing.T, cmd trip.CreateCommand) trip.Resource {
	t.Helper()
	if cmd.ID == uuid.Nil {
		cmd.ID = uuid.New()
	}
	if cmd.Name == "" {
		cmd.Name = "东京之旅"
	}
	if cmd.StartDate == "" {
		cmd.StartDate = "2026-10-01"
	}
	if cmd.EndDate == "" {
		cmd.EndDate = "2026-10-07"
	}
	res, err := f.svc.Create(context.Background(), f.actor, uuid.New(), cmd)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return res.Data.(trip.Resource)
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

func TestCreateAppliesDefaultsAndRecordsChange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := uuid.New()
	op := uuid.New()
	cmd := trip.CreateCommand{ID: id, Name: "  京都  ", StartDate: "2026-11-01", EndDate: "2026-11-03", BudgetAmount: str("1234.5")}
	res, err := f.svc.Create(ctx, f.actor, op, cmd)
	if err != nil {
		t.Fatal(err)
	}
	r := res.Data.(trip.Resource)
	if r.Name != "京都" || r.Timezone != "Asia/Shanghai" || r.CurrencyCode != "CNY" || r.BudgetAmount == nil || *r.BudgetAmount != "1234.50" {
		t.Fatalf("defaults not applied: %+v", r)
	}
	if r.Version != 1 || r.Destination != "" || r.Notes != "" || r.ArchivedAt != nil || r.DeletedAt != nil {
		t.Fatalf("unexpected initial state: %+v", r)
	}
	if res.Primary == nil || res.Primary.Type != trip.EntityType || len(res.Affected) != 1 || res.Replayed {
		t.Fatalf("result refs: %+v", res)
	}
	changes := f.uow.Changes()
	if len(changes) != 1 || changes[0].Kind != write.ChangeUpsert || changes[0].TripID == nil || *changes[0].TripID != id {
		t.Fatalf("change log: %+v", changes)
	}
	if len(changes[0].ChangedFields) != len(trip.CreateFields) {
		t.Fatalf("create should list all fields, got %v", changes[0].ChangedFields)
	}

	// 同键同内容重放：replayed=true，不重复写入；同键不同内容 409
	replay, err := f.svc.Create(ctx, f.actor, op, cmd)
	if err != nil || !replay.Replayed || replay.Data.(trip.Resource).ID != id {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	if len(f.uow.Changes()) != 1 {
		t.Fatal("replay must not append changes")
	}
	cmd2 := cmd
	cmd2.Name = "别的名字"
	_, err = f.svc.Create(ctx, f.actor, op, cmd2)
	expectCode(t, err, 409, "IDEMPOTENCY_CONFLICT")

	// 金额 "1234.50" 与 "1234.5" 指纹相同
	cmd3 := cmd
	cmd3.BudgetAmount = str("1234.50")
	if _, err := f.svc.Create(ctx, f.actor, op, cmd3); err != nil {
		t.Fatalf("equivalent amount must replay: %v", err)
	}

	// 新键但 ID 已用 → 409 ID_ALREADY_USED；墓碑同样
	_, err = f.svc.Create(ctx, f.actor, uuid.New(), cmd)
	expectCode(t, err, 409, "ID_ALREADY_USED")
	dead := uuid.New()
	f.store.AddTombstone(dead)
	_, err = f.svc.Create(ctx, f.actor, uuid.New(), trip.CreateCommand{ID: dead, Name: "x", StartDate: "2026-01-01", EndDate: "2026-01-02"})
	expectCode(t, err, 409, "ID_ALREADY_USED")
}

func TestCreateValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, err := f.svc.Create(ctx, f.actor, uuid.New(), trip.CreateCommand{
		Name: "   ", StartDate: "2026-1-5", EndDate: "2026-01-01", Timezone: str("Mars/Olympus"), CurrencyCode: str("XXX"), BudgetAmount: str("-1"),
	})
	e := expectCode(t, err, 422, "VALIDATION_FAILED")
	got := fieldCodes(e)
	for _, want := range []string{"id:INVALID", "name:INVALID", "start_date:INVALID", "timezone:INVALID", "currency_code:INVALID", "budget_amount:INVALID"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	_, err = f.svc.Create(ctx, f.actor, uuid.New(), trip.CreateCommand{ID: uuid.New(), Name: "x", StartDate: "2026-01-05", EndDate: "2026-01-01"})
	e = expectCode(t, err, 422, "VALIDATION_FAILED")
	if !strings.Contains(fieldCodes(e), "end_date:DATE_ORDER") {
		t.Fatalf("date order: %s", fieldCodes(e))
	}
	// JPY 不允许小数
	_, err = f.svc.Create(ctx, f.actor, uuid.New(), trip.CreateCommand{ID: uuid.New(), Name: "x", StartDate: "2026-01-01", EndDate: "2026-01-02", CurrencyCode: str("JPY"), BudgetAmount: str("100.5")})
	e = expectCode(t, err, 422, "VALIDATION_FAILED")
	if !strings.Contains(fieldCodes(e), "budget_amount:INVALID") {
		t.Fatalf("scale: %s", fieldCodes(e))
	}
	r := f.create(t, trip.CreateCommand{CurrencyCode: str("JPY"), BudgetAmount: str("0100")})
	if *r.BudgetAmount != "100" {
		t.Fatalf("JPY budget should be integer string, got %s", *r.BudgetAmount)
	}
}

func TestUpdateFieldLevelMerge(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.create(t, trip.CreateCommand{})

	// 基线 1 改名 → 版本 2
	res, err := f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 1, trip.Patch{Name: str("大阪")})
	if err != nil {
		t.Fatal(err)
	}
	if v := res.Data.(trip.Resource); v.Version != 2 || v.Name != "大阪" || len(res.Warnings) != 0 {
		t.Fatalf("update: %+v", res)
	}
	changes := f.uow.Changes()
	if last := changes[len(changes)-1]; len(last.ChangedFields) != 1 || last.ChangedFields[0] != "name" {
		t.Fatalf("changed_fields: %v", last.ChangedFields)
	}

	// 仍以基线 1 改备注：字段不相交 → 合并，版本 3，带警告
	res, err = f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 1, trip.Patch{Notes: str("带雨伞")})
	if err != nil {
		t.Fatal(err)
	}
	if v := res.Data.(trip.Resource); v.Version != 3 || v.Name != "大阪" || v.Notes != "带雨伞" || len(res.Warnings) != 1 || res.Warnings[0] != write.WarnMergedWithNewerVersion {
		t.Fatalf("merge: %+v", res)
	}

	// 以基线 1 改名：与版本 2 的 name 相交 → 412，conflicting_fields=[name]，current 为当前资源
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 1, trip.Patch{Name: str("神户"), Destination: str("关西")})
	e := expectCode(t, err, 412, "VERSION_CONFLICT")
	if e.Conflict == nil || e.Conflict.CurrentVersion != 3 || len(e.Conflict.ConflictingFields) != 1 || e.Conflict.ConflictingFields[0] != "name" {
		t.Fatalf("conflict: %+v", e.Conflict)
	}
	if cur, ok := e.Conflict.Current.(trip.Resource); !ok || cur.Version != 3 {
		t.Fatalf("conflict current: %+v", e.Conflict.Current)
	}

	// 基线超前 → 412 且无法判断字段
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 9, trip.Patch{Name: str("x")})
	e = expectCode(t, err, 412, "VERSION_CONFLICT")
	if e.Conflict.ConflictingFields != nil {
		t.Fatalf("base ahead should not list fields: %+v", e.Conflict)
	}

	// 空补丁 422；不存在 404；他人 404
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 3, trip.Patch{})
	expectCode(t, err, 422, "VALIDATION_FAILED")
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), uuid.New(), 1, trip.Patch{Name: str("x")})
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
	other := f.actor
	other.AccountID = uuid.New()
	_, err = f.svc.Update(ctx, other, uuid.New(), r.ID, 3, trip.Patch{Name: str("x")})
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
}

func TestUpdateDatesTimezoneAndWarnings(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.create(t, trip.CreateCommand{StartDate: "2026-10-01", EndDate: "2026-10-07"})
	f.store.ItineraryDates[r.ID] = []types.Date{"2026-10-06"}
	f.store.LocalTimes[r.ID] = true

	// 缩短日期使行程落在范围外 → 警告；同时改时区 → 第二个警告
	res, err := f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 1, trip.Patch{EndDate: str("2026-10-05"), Timezone: str("Asia/Tokyo")})
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(res.Warnings, ",")
	if !strings.Contains(warnings, write.WarnItineraryOutsideTripDates) || !strings.Contains(warnings, write.WarnTimezoneInterpretationChange) {
		t.Fatalf("warnings: %v", res.Warnings)
	}
	// 只改开始日期使其晚于结束日期 → 422 start_date
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 2, trip.Patch{StartDate: str("2026-10-09")})
	e := expectCode(t, err, 422, "VALIDATION_FAILED")
	if !strings.Contains(fieldCodes(e), "start_date:DATE_ORDER") {
		t.Fatalf("fields: %s", fieldCodes(e))
	}
}

func TestUpdateCurrencyRules(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.create(t, trip.CreateCommand{BudgetAmount: str("100")})

	// 有预算时改币种 → 409 CURRENCY_AMOUNTS_EXIST
	_, err := f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 1, trip.Patch{CurrencyCode: str("JPY")})
	expectCode(t, err, 409, "CURRENCY_AMOUNTS_EXIST")
	// 同一补丁清空预算并改币种 → 通过
	res, err := f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 1, trip.Patch{CurrencyCode: str("JPY"), BudgetSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if v := res.Data.(trip.Resource); v.CurrencyCode != "JPY" || v.BudgetAmount != nil {
		t.Fatalf("currency change: %+v", v)
	}
	// 同一补丁给新币种设预算：按新币种校验小数位
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 2, trip.Patch{CurrencyCode: str("USD"), BudgetSet: true, BudgetAmount: str("10.123")})
	expectCode(t, err, 422, "VALIDATION_FAILED")
	res, err = f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 2, trip.Patch{CurrencyCode: str("USD"), BudgetSet: true, BudgetAmount: str("10.1")})
	if err != nil || *res.Data.(trip.Resource).BudgetAmount != "10.10" {
		t.Fatalf("budget with new currency: %+v %v", res.Data, err)
	}
	// 行程有预计费用 → 409
	f.store.EstimatedAmounts[r.ID] = true
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 3, trip.Patch{CurrencyCode: str("EUR"), BudgetSet: true})
	expectCode(t, err, 409, "CURRENCY_AMOUNTS_EXIST")
	f.store.EstimatedAmounts[r.ID] = false
	// 已有账目锁定 → 409 CURRENCY_LOCKED，即便预算已清空
	locked := f.clock.Now()
	if _, err := f.store.Update(ctx, f.actor.AccountID, r.ID, res.Data.(trip.Resource).Values(), locked); err != nil {
		t.Fatal(err)
	}
	cur, _, _ := f.store.Get(ctx, f.actor.AccountID, r.ID)
	cur.CurrencyLockedAt = &locked
	_, _ = f.store.Insert(ctx, f.actor.AccountID, cur)
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), r.ID, int64(cur.Version), trip.Patch{CurrencyCode: str("EUR"), BudgetSet: true})
	expectCode(t, err, 409, "CURRENCY_LOCKED")
	// 不改币种时预算按当前币种规范化
	res, err = f.svc.Update(ctx, f.actor, uuid.New(), r.ID, int64(cur.Version), trip.Patch{BudgetSet: true, BudgetAmount: str("5")})
	if err != nil || *res.Data.(trip.Resource).BudgetAmount != "5.00" {
		t.Fatalf("budget: %+v %v", res.Data, err)
	}
}

func TestArchiveTrashRestore(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.create(t, trip.CreateCommand{})

	// 归档：版本 2；重复归档不变；取消归档：版本 3；版本不等 412
	res, err := f.svc.SetArchived(ctx, f.actor, uuid.New(), r.ID, 1, true)
	if err != nil || res.Data.(trip.Resource).ArchivedAt == nil || res.Data.(trip.Resource).Version != 2 {
		t.Fatalf("archive: %+v %v", res.Data, err)
	}
	res, err = f.svc.SetArchived(ctx, f.actor, uuid.New(), r.ID, 2, true)
	if err != nil || res.Data.(trip.Resource).Version != 2 || len(res.Affected) != 1 {
		t.Fatalf("archive again: %+v %v", res, err)
	}
	_, err = f.svc.SetArchived(ctx, f.actor, uuid.New(), r.ID, 1, false)
	expectCode(t, err, 412, "VERSION_CONFLICT")
	res, err = f.svc.SetArchived(ctx, f.actor, uuid.New(), r.ID, 2, false)
	if err != nil || res.Data.(trip.Resource).ArchivedAt != nil || res.Data.(trip.Resource).Version != 3 {
		t.Fatalf("unarchive: %+v %v", res.Data, err)
	}

	// 删除到回收站：delete 事件；GET 410；GetTrashed 可读；列表不含
	res, err = f.svc.Trash(ctx, f.actor, uuid.New(), r.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	trashed := res.Data.(trip.Resource)
	if trashed.DeletedAt == nil || trashed.PurgeAfterAt == nil || trashed.PurgeAfterAt.Sub(*trashed.DeletedAt) != trip.RecycleBinRetention || trashed.Version != 4 {
		t.Fatalf("trash: %+v", trashed)
	}
	changes := f.uow.Changes()
	if last := changes[len(changes)-1]; last.Kind != write.ChangeDelete || last.Snapshot != nil {
		t.Fatalf("delete change: %+v", last)
	}
	_, err = f.svc.Get(ctx, f.actor, r.ID)
	expectCode(t, err, 410, "TRIP_DELETED")
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), r.ID, 4, trip.Patch{Name: str("x")})
	expectCode(t, err, 410, "TRIP_DELETED")
	_, err = f.svc.Trash(ctx, f.actor, uuid.New(), r.ID, 4)
	expectCode(t, err, 410, "TRIP_DELETED")
	if got, err := f.svc.GetTrashed(ctx, f.actor, r.ID); err != nil || got.ID != r.ID {
		t.Fatalf("get trashed: %+v %v", got, err)
	}
	page, err := f.svc.List(ctx, f.actor, trip.Filters{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("list should hide trashed: %+v %v", page, err)
	}
	bin, err := f.svc.ListTrashed(ctx, f.actor, 0, "")
	if err != nil || len(bin.Items) != 1 || bin.NextCursor != nil {
		t.Fatalf("recycle bin: %+v %v", bin, err)
	}

	// 恢复：版本 5，upsert 事件带 requires_snapshot；之后 GetTrashed 404
	res, err = f.svc.Restore(ctx, f.actor, uuid.New(), r.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	restored := res.Data.(trip.Resource)
	if restored.DeletedAt != nil || restored.PurgeAfterAt != nil || restored.Version != 5 {
		t.Fatalf("restore: %+v", restored)
	}
	changes = f.uow.Changes()
	if last := changes[len(changes)-1]; last.Kind != write.ChangeUpsert || !last.RequiresSnapshot {
		t.Fatalf("restore change: %+v", last)
	}
	_, err = f.svc.GetTrashed(ctx, f.actor, r.ID)
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
	_, err = f.svc.Restore(ctx, f.actor, uuid.New(), r.ID, 5)
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")

	// 过了保留期不能恢复
	if _, err := f.svc.Trash(ctx, f.actor, uuid.New(), r.ID, 5); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(trip.RecycleBinRetention)
	_, err = f.svc.Restore(ctx, f.actor, uuid.New(), r.ID, 6)
	expectCode(t, err, 409, "RESTORE_UNAVAILABLE")
}

func TestPurgeRequiresReauthAndEnqueuesJob(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.create(t, trip.CreateCommand{})
	if _, err := f.svc.Trash(ctx, f.actor, uuid.New(), r.ID, 1); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.Purge(ctx, f.actor, uuid.New(), r.ID, 2, false)
	expectCode(t, err, 422, "VALIDATION_FAILED")
	_, err = f.svc.Purge(ctx, f.actor, uuid.New(), r.ID, 2, true)
	expectCode(t, err, 403, "REAUTH_REQUIRED")

	stale := f.clock.Now().Add(-10 * time.Minute)
	f.actor.ReauthenticatedAt = &stale
	_, err = f.svc.Purge(ctx, f.actor, uuid.New(), r.ID, 2, true)
	expectCode(t, err, 403, "REAUTH_REQUIRED")

	recent := f.clock.Now().Add(-time.Minute)
	f.actor.ReauthenticatedAt = &recent
	res, err := f.svc.Purge(ctx, f.actor, uuid.New(), r.ID, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	purged := res.Data.(trip.Resource)
	if purged.PurgeRequestedAt == nil || purged.Version != 3 {
		t.Fatalf("purge: %+v", purged)
	}
	if len(res.Affected) != 2 || res.Affected[1].Type != trip.EntityTypeDeletionJob || res.Affected[1].Version != nil {
		t.Fatalf("affected: %+v", res.Affected)
	}
	jobs := f.uow.Jobs()
	if len(jobs) != 1 || jobs[0].Kind() != "trip_purge" {
		t.Fatalf("jobs: %+v", jobs)
	}
	changes := f.uow.Changes()
	if last := changes[len(changes)-1]; last.Kind != write.ChangePurge {
		t.Fatalf("purge change: %+v", last)
	}

	// 恢复被拒；再次请求（新操作编号、当前版本）返回同一任务且不再入队
	_, err = f.svc.Restore(ctx, f.actor, uuid.New(), r.ID, 3)
	expectCode(t, err, 409, "RESTORE_UNAVAILABLE")
	again, err := f.svc.Purge(ctx, f.actor, uuid.New(), r.ID, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Affected[1].ID != res.Affected[1].ID || len(f.uow.Jobs()) != 1 {
		t.Fatalf("second purge should reuse job: %+v", again.Affected)
	}
	// 未删除的旅行不能永久清理
	active := f.create(t, trip.CreateCommand{})
	_, err = f.svc.Purge(ctx, f.actor, uuid.New(), active.ID, 1, true)
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
}

func TestListFiltersSortAndPaging(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// 今天 2026-09-12（Asia/Shanghai 16:00）
	ended := f.create(t, trip.CreateCommand{Name: "去年冲绳", StartDate: "2025-12-01", EndDate: "2025-12-05", Destination: str("Okinawa")})
	ongoing := f.create(t, trip.CreateCommand{Name: "正在进行", StartDate: "2026-09-10", EndDate: "2026-09-15"})
	planned := f.create(t, trip.CreateCommand{Name: "国庆东京", StartDate: "2026-10-01", EndDate: "2026-10-07"})
	planned2 := f.create(t, trip.CreateCommand{Name: "元旦冲绳", StartDate: "2027-01-01", EndDate: "2027-01-03", Destination: str("冲绳")})
	f.clock.Advance(time.Minute)
	if _, err := f.svc.SetArchived(ctx, f.actor, uuid.New(), ended.ID, 1, true); err != nil {
		t.Fatal(err)
	}

	page, err := f.svc.List(ctx, f.actor, trip.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 || page.Items[0].ID != planned2.ID || page.Items[3].ID != ended.ID || page.NextCursor != nil {
		t.Fatalf("default order: %+v", page)
	}
	if page.Items[0].Phase != trip.PhasePlanned || page.Items[1].Phase != trip.PhasePlanned || page.Items[2].Phase != trip.PhaseOngoing || page.Items[3].Phase != trip.PhaseEnded {
		t.Fatalf("phases: %+v", page.Items)
	}

	ph := trip.PhasePlanned
	page, _ = f.svc.List(ctx, f.actor, trip.Filters{Phase: &ph})
	if len(page.Items) != 2 || page.Items[1].ID != planned.ID {
		t.Fatalf("phase filter: %+v", page.Items)
	}
	page, _ = f.svc.List(ctx, f.actor, trip.Filters{Archived: trip.ArchivedOnly})
	if len(page.Items) != 1 || page.Items[0].ID != ended.ID {
		t.Fatalf("archived filter: %+v", page.Items)
	}
	page, _ = f.svc.List(ctx, f.actor, trip.Filters{Archived: trip.ArchivedNone})
	if len(page.Items) != 3 {
		t.Fatalf("unarchived filter: %+v", page.Items)
	}
	page, _ = f.svc.List(ctx, f.actor, trip.Filters{Query: "冲绳"})
	if len(page.Items) != 2 {
		t.Fatalf("query by name/destination: %+v", page.Items)
	}
	page, _ = f.svc.List(ctx, f.actor, trip.Filters{Query: "okinawa"})
	if len(page.Items) != 1 || page.Items[0].ID != ended.ID {
		t.Fatalf("case-insensitive query: %+v", page.Items)
	}

	// updated_at 排序：刚归档的 ended 最新
	page, _ = f.svc.List(ctx, f.actor, trip.Filters{Sort: trip.SortUpdatedAtDesc})
	if page.Items[0].ID != ended.ID {
		t.Fatalf("updated sort: %+v", page.Items)
	}
	_ = ongoing

	// 分页：limit 2 → 两页 + 空游标；游标跨筛选无效；乱码 400
	first, err := f.svc.List(ctx, f.actor, trip.Filters{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page: %+v %v", first, err)
	}
	second, err := f.svc.List(ctx, f.actor, trip.Filters{Limit: 2, Cursor: *first.NextCursor})
	if err != nil || len(second.Items) != 2 || second.NextCursor != nil || second.Items[0].ID != ongoing.ID {
		t.Fatalf("second page: %+v %v", second, err)
	}
	_, err = f.svc.List(ctx, f.actor, trip.Filters{Limit: 2, Cursor: *first.NextCursor, Archived: trip.ArchivedNone})
	expectCode(t, err, 400, "INVALID_CURSOR")
	_, err = f.svc.List(ctx, f.actor, trip.Filters{Cursor: "garbage"})
	expectCode(t, err, 400, "INVALID_CURSOR")
	other := f.actor
	other.AccountID = uuid.New()
	_, err = f.svc.List(ctx, other, trip.Filters{Limit: 2, Cursor: *first.NextCursor})
	expectCode(t, err, 400, "INVALID_CURSOR")
	_, err = f.svc.List(ctx, f.actor, trip.Filters{Limit: 101})
	expectCode(t, err, 422, "VALIDATION_FAILED")
	_, err = f.svc.List(ctx, f.actor, trip.Filters{Query: strings.Repeat("长", 101)})
	expectCode(t, err, 422, "VALIDATION_FAILED")
	bad := trip.Phase("someday")
	_, err = f.svc.List(ctx, f.actor, trip.Filters{Phase: &bad})
	expectCode(t, err, 422, "VALIDATION_FAILED")

	// 回收站分页
	for _, r := range []trip.Resource{planned, planned2} {
		cur, _, _ := f.store.Get(ctx, f.actor.AccountID, r.ID)
		if _, err := f.svc.Trash(ctx, f.actor, uuid.New(), r.ID, int64(cur.Version)); err != nil {
			t.Fatal(err)
		}
		f.clock.Advance(time.Minute)
	}
	bin, err := f.svc.ListTrashed(ctx, f.actor, 1, "")
	if err != nil || len(bin.Items) != 1 || bin.Items[0].ID != planned2.ID || bin.NextCursor == nil {
		t.Fatalf("bin first: %+v %v", bin, err)
	}
	bin2, err := f.svc.ListTrashed(ctx, f.actor, 1, *bin.NextCursor)
	if err != nil || len(bin2.Items) != 1 || bin2.Items[0].ID != planned.ID || bin2.NextCursor != nil {
		t.Fatalf("bin second: %+v %v", bin2, err)
	}
	_, err = f.svc.List(ctx, f.actor, trip.Filters{Cursor: *bin.NextCursor})
	expectCode(t, err, 400, "INVALID_CURSOR")
}

func TestPhaseOfUsesTripTimezone(t *testing.T) {
	// UTC 2026-09-12T20:00 在 Asia/Tokyo 已是 09-13
	now := time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)
	r := trip.Resource{StartDate: "2026-09-13", EndDate: "2026-09-14", Timezone: "Asia/Tokyo"}
	if got := trip.PhaseOf(r, now); got != trip.PhaseOngoing {
		t.Fatalf("Tokyo phase = %s", got)
	}
	r.Timezone = "America/Los_Angeles"
	if got := trip.PhaseOf(r, now); got != trip.PhasePlanned {
		t.Fatalf("LA phase = %s", got)
	}
	r.Timezone = "Nowhere/Invalid"
	if got := trip.PhaseOf(r, now); got != trip.PhasePlanned {
		t.Fatalf("invalid tz falls back to UTC: %s", got)
	}
}
