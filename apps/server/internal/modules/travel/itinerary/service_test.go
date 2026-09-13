package itinerary_test

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
	"tripfolio/server/internal/modules/travel/itinerary"
)

type fixture struct {
	store  *itinerary.MemoryStore
	uow    *write.MemoryUnitOfWork[itinerary.Repo]
	svc    *itinerary.Service
	clock  *clock.Fake
	actor  actor.Actor
	tripID uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := itinerary.NewMemoryStore()
	uow := write.NewMemoryUnitOfWork[itinerary.Repo](store)
	store.SetMergeSource(uow)
	clk := clock.NewFake(time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC))
	svc := itinerary.NewService(uow, store, paging.InsecureCodec{}, clk)
	a := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"}
	tripID := uuid.New()
	store.PutTrip(a.AccountID, tripID, itinerary.TripInfo{CurrencyCode: "CNY", StartDate: "2026-10-01", EndDate: "2026-10-07"})
	return &fixture{store: store, uow: uow, svc: svc, clock: clk, actor: a, tripID: tripID}
}

func str(s string) *string   { return &s }
func i32(v int32) *int32     { return &v }
func f64(v float64) *float64 { return &v }

func (f *fixture) create(t *testing.T, cmd itinerary.CreateCommand) itinerary.Resource {
	t.Helper()
	if cmd.ID == uuid.Nil {
		cmd.ID = uuid.New()
	}
	if cmd.Title == "" {
		cmd.Title = "浅草寺"
	}
	if cmd.Kind == "" {
		cmd.Kind = "attraction"
	}
	if cmd.ScheduledOn == "" {
		cmd.ScheduledOn = "2026-10-02"
	}
	res, err := f.svc.Create(context.Background(), f.actor, uuid.New(), f.tripID, cmd)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return res.Data.(itinerary.Resource)
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

func hasWarning(res write.Result, code string) bool {
	for _, w := range res.Warnings {
		if w == code {
			return true
		}
	}
	return false
}

func TestCreateAppliesDefaultsAndAppendsToDay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := uuid.New()
	res, err := f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, itinerary.CreateCommand{
		ID: id, Title: "  浅草寺  ", Kind: "attraction", ScheduledOn: "2026-10-02",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Data.(itinerary.Resource)
	if r.Title != "浅草寺" || r.TripID != f.tripID || r.SortOrder != 0 || r.Version != 1 {
		t.Fatalf("unexpected resource: %+v", r)
	}
	if r.Status != itinerary.StatusPending || r.PlaceName != "" || r.Address != "" || r.Notes != "" || r.ActualNotes != "" {
		t.Fatalf("defaults not applied: %+v", r)
	}
	if r.EstimatedAmount != nil || r.CurrencyCode != nil || r.Latitude != nil || r.DeletedAt != nil {
		t.Fatalf("nullable defaults not applied: %+v", r)
	}
	if res.Primary == nil || res.Primary.Type != itinerary.EntityType || len(res.Affected) != 1 {
		t.Fatalf("result refs: %+v", res)
	}
	changes := f.uow.Changes()
	if len(changes) != 1 || changes[0].Kind != write.ChangeUpsert || changes[0].TripID == nil || *changes[0].TripID != f.tripID {
		t.Fatalf("change log: %+v", changes)
	}
	if len(changes[0].ChangedFields) != len(itinerary.CreateFields) {
		t.Fatalf("create should list all fields, got %v", changes[0].ChangedFields)
	}

	// 同一天追加到末尾，另一天从 0 开始
	second := f.create(t, itinerary.CreateCommand{Title: "晴空塔", ScheduledOn: "2026-10-02"})
	if second.SortOrder != 1 {
		t.Fatalf("second item sort_order = %d, want 1", second.SortOrder)
	}
	other := f.create(t, itinerary.CreateCommand{Title: "筑地", ScheduledOn: "2026-10-03"})
	if other.SortOrder != 0 {
		t.Fatalf("new day sort_order = %d, want 0", other.SortOrder)
	}
}

func TestCreateIdempotencyAndIDReuse(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id, op := uuid.New(), uuid.New()
	cmd := itinerary.CreateCommand{ID: id, Title: "浅草寺", Kind: "attraction", ScheduledOn: "2026-10-02", EstimatedAmount: str("12.5"), CurrencyCode: str("CNY")}
	first, err := f.svc.Create(ctx, f.actor, op, f.tripID, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if a := first.Data.(itinerary.Resource); a.EstimatedAmount == nil || *a.EstimatedAmount != "12.50" || a.CurrencyCode == nil || *a.CurrencyCode != "CNY" {
		t.Fatalf("amount not canonicalized: %+v", a)
	}

	replay, err := f.svc.Create(ctx, f.actor, op, f.tripID, cmd)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	if len(f.uow.Changes()) != 1 {
		t.Fatal("replay must not append changes")
	}

	// "12.5" 与 "12.50" 规范化后指纹相同
	equivalent := cmd
	equivalent.EstimatedAmount = str("12.50")
	if _, err := f.svc.Create(ctx, f.actor, op, f.tripID, equivalent); err != nil {
		t.Fatalf("equivalent amount must replay: %v", err)
	}

	changed := cmd
	changed.Title = "别的名字"
	_, err = f.svc.Create(ctx, f.actor, op, f.tripID, changed)
	expectCode(t, err, 409, "IDEMPOTENCY_CONFLICT")

	_, err = f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, cmd)
	expectCode(t, err, 409, "ID_ALREADY_USED")

	dead := uuid.New()
	f.store.AddTombstone(dead)
	_, err = f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, itinerary.CreateCommand{ID: dead, Title: "x", Kind: "other", ScheduledOn: "2026-10-02"})
	expectCode(t, err, 409, "ID_ALREADY_USED")
}

func TestCreateValidation(t *testing.T) {
	base := func() itinerary.CreateCommand {
		return itinerary.CreateCommand{ID: uuid.New(), Title: "浅草寺", Kind: "attraction", ScheduledOn: "2026-10-02"}
	}
	cases := []struct {
		name  string
		mut   func(c *itinerary.CreateCommand)
		codes string
	}{
		{"缺少 id", func(c *itinerary.CreateCommand) { c.ID = uuid.Nil }, "id:INVALID"},
		{"标题为空", func(c *itinerary.CreateCommand) { c.Title = "   " }, "title:INVALID"},
		{"标题过长", func(c *itinerary.CreateCommand) { c.Title = strings.Repeat("字", 201) }, "title:INVALID"},
		{"未知类型", func(c *itinerary.CreateCommand) { c.Kind = "sleep" }, "kind:INVALID"},
		{"日期格式", func(c *itinerary.CreateCommand) { c.ScheduledOn = "2026-1-2" }, "scheduled_on:INVALID"},
		{"未知状态", func(c *itinerary.CreateCommand) { c.Status = str("done") }, "status:INVALID"},
		{"结束与时长互斥", func(c *itinerary.CreateCommand) {
			c.PlannedEndLocal, c.PlannedDurationMinutes = str("2026-10-02T12:00:00"), i32(60)
		}, "planned_duration_minutes:EXCLUSIVE"},
		{"时长非正", func(c *itinerary.CreateCommand) { c.PlannedDurationMinutes = i32(0) }, "planned_duration_minutes:INVALID"},
		{"计划结束早于开始", func(c *itinerary.CreateCommand) {
			c.PlannedStartLocal, c.PlannedEndLocal = str("2026-10-02T12:00:00"), str("2026-10-02T11:00:00")
		}, "planned_end_local:TIME_ORDER"},
		{"实际结束早于开始", func(c *itinerary.CreateCommand) {
			c.ActualStartLocal, c.ActualEndLocal = str("2026-10-02T12:00:00"), str("2026-10-02T11:00:00")
		}, "actual_end_local:TIME_ORDER"},
		{"时间格式", func(c *itinerary.CreateCommand) { c.PlannedStartLocal = str("2026-10-02 12:00") }, "planned_start_local:INVALID"},
		{"只给纬度", func(c *itinerary.CreateCommand) { c.Latitude = f64(35.7148) }, "longitude:REQUIRED"},
		{"只给经度", func(c *itinerary.CreateCommand) { c.Longitude = f64(139.7967) }, "latitude:REQUIRED"},
		{"纬度越界", func(c *itinerary.CreateCommand) { c.Latitude, c.Longitude = f64(95), f64(139.7967) }, "latitude:INVALID"},
		{"经度越界", func(c *itinerary.CreateCommand) { c.Latitude, c.Longitude = f64(35.7148), f64(200) }, "longitude:INVALID"},
		{"坐标精度", func(c *itinerary.CreateCommand) { c.Latitude, c.Longitude = f64(35.71481234), f64(139.7967) }, "latitude:PRECISION"},
		{"金额缺币种", func(c *itinerary.CreateCommand) { c.EstimatedAmount = str("10") }, "currency_code:REQUIRED"},
		{"金额格式", func(c *itinerary.CreateCommand) { c.EstimatedAmount, c.CurrencyCode = str("-1"), str("CNY") }, "estimated_amount:INVALID"},
		{"地点过长", func(c *itinerary.CreateCommand) { c.PlaceName = str(strings.Repeat("字", 201)) }, "place_name:TOO_LONG"},
		{"地址过长", func(c *itinerary.CreateCommand) { c.Address = str(strings.Repeat("字", 501)) }, "address:TOO_LONG"},
		{"备注过长", func(c *itinerary.CreateCommand) { c.Notes = str(strings.Repeat("字", 10001)) }, "notes:TOO_LONG"},
		{"实际备注过长", func(c *itinerary.CreateCommand) { c.ActualNotes = str(strings.Repeat("字", 10001)) }, "actual_notes:TOO_LONG"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			cmd := base()
			tc.mut(&cmd)
			_, err := f.svc.Create(context.Background(), f.actor, uuid.New(), f.tripID, cmd)
			e := expectCode(t, err, 422, "VALIDATION_FAILED")
			if got := fieldCodes(e); !strings.Contains(got, tc.codes) {
				t.Fatalf("field errors = %s, want to contain %s", got, tc.codes)
			}
		})
	}
}

func TestCreateRejectsForeignCurrency(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Create(context.Background(), f.actor, uuid.New(), f.tripID, itinerary.CreateCommand{
		ID: uuid.New(), Title: "浅草寺", Kind: "attraction", ScheduledOn: "2026-10-02",
		EstimatedAmount: str("10"), CurrencyCode: str("USD"),
	})
	expectCode(t, err, 422, "CURRENCY_MISMATCH")
}

func TestCreateWarnsOutsideTripDates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	inside, err := f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, itinerary.CreateCommand{
		ID: uuid.New(), Title: "浅草寺", Kind: "attraction", ScheduledOn: "2026-10-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasWarning(inside, write.WarnItineraryOutsideTripDates) {
		t.Fatalf("in-range date must not warn: %v", inside.Warnings)
	}
	outside, err := f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, itinerary.CreateCommand{
		ID: uuid.New(), Title: "机场", Kind: "transport", ScheduledOn: "2026-09-30",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(outside, write.WarnItineraryOutsideTripDates) {
		t.Fatalf("out-of-range date must warn: %v", outside.Warnings)
	}
}

func TestTripScope(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cmd := itinerary.CreateCommand{ID: uuid.New(), Title: "浅草寺", Kind: "attraction", ScheduledOn: "2026-10-02"}

	// 不存在或非本人的旅行
	_, err := f.svc.Create(ctx, f.actor, uuid.New(), uuid.New(), cmd)
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
	_, err = f.svc.List(ctx, f.actor, uuid.New(), itinerary.Filters{})
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")

	// 回收站中的旅行
	trashed := uuid.New()
	deletedAt := f.clock.Now()
	f.store.PutTrip(f.actor.AccountID, trashed, itinerary.TripInfo{CurrencyCode: "CNY", StartDate: "2026-10-01", EndDate: "2026-10-07", DeletedAt: &deletedAt})
	_, err = f.svc.Create(ctx, f.actor, uuid.New(), trashed, cmd)
	expectCode(t, err, 410, "TRIP_DELETED")
	_, err = f.svc.List(ctx, f.actor, trashed, itinerary.Filters{})
	expectCode(t, err, 410, "TRIP_DELETED")

	// 别的账号的旅行
	other := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"}
	_, err = f.svc.Create(ctx, other, uuid.New(), f.tripID, cmd)
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
}

func TestGetAndList(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.create(t, itinerary.CreateCommand{Title: "浅草寺", ScheduledOn: "2026-10-02"})
	b := f.create(t, itinerary.CreateCommand{Title: "晴空塔", ScheduledOn: "2026-10-02", Status: str("completed")})
	c := f.create(t, itinerary.CreateCommand{Title: "筑地", ScheduledOn: "2026-10-01"})

	got, err := f.svc.Get(ctx, f.actor, f.tripID, a.ID)
	if err != nil || got.ID != a.ID {
		t.Fatalf("get: %+v, %v", got, err)
	}
	_, err = f.svc.Get(ctx, f.actor, f.tripID, uuid.New())
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")

	// 列表按日期、顺序、ID 升序
	page, err := f.svc.List(ctx, f.actor, f.tripID, itinerary.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].ID != c.ID || page.Items[1].ID != a.ID || page.Items[2].ID != b.ID {
		t.Fatalf("list order: %+v", page.Items)
	}
	if page.NextCursor != nil {
		t.Fatal("no more pages expected")
	}

	// 日期区间与状态筛选
	page, err = f.svc.List(ctx, f.actor, f.tripID, itinerary.Filters{DateFrom: "2026-10-02", DateTo: "2026-10-02"})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("date filter: %+v, %v", page.Items, err)
	}
	page, err = f.svc.List(ctx, f.actor, f.tripID, itinerary.Filters{Status: "completed"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != b.ID {
		t.Fatalf("status filter: %+v, %v", page.Items, err)
	}

	// 分页与游标
	first, err := f.svc.List(ctx, f.actor, f.tripID, itinerary.Filters{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	next, err := f.svc.List(ctx, f.actor, f.tripID, itinerary.Filters{Limit: 2, Cursor: *first.NextCursor})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != b.ID || next.NextCursor != nil {
		t.Fatalf("second page: %+v, %v", next, err)
	}
	// 游标绑定筛选范围
	_, err = f.svc.List(ctx, f.actor, f.tripID, itinerary.Filters{Limit: 2, Status: "pending", Cursor: *first.NextCursor})
	expectCode(t, err, 400, "INVALID_CURSOR")

	// 查询校验
	_, err = f.svc.List(ctx, f.actor, f.tripID, itinerary.Filters{Limit: 101})
	expectCode(t, err, 422, "VALIDATION_FAILED")
	_, err = f.svc.List(ctx, f.actor, f.tripID, itinerary.Filters{DateFrom: "2026-10-05", DateTo: "2026-10-01"})
	e := expectCode(t, err, 422, "VALIDATION_FAILED")
	if got := fieldCodes(e); !strings.Contains(got, "date_to:DATE_ORDER") {
		t.Fatalf("field errors = %s", got)
	}
	_, err = f.svc.List(ctx, f.actor, f.tripID, itinerary.Filters{Status: "done"})
	expectCode(t, err, 422, "VALIDATION_FAILED")
}

func TestUpdateMergesNonOverlappingFields(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	item := f.create(t, itinerary.CreateCommand{Title: "浅草寺"})

	updated, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 1, itinerary.Patch{Title: str("浅草寺雷门")})
	if err != nil {
		t.Fatal(err)
	}
	r := updated.Data.(itinerary.Resource)
	if r.Title != "浅草寺雷门" || r.Version != 2 || r.SortOrder != item.SortOrder || r.ScheduledOn != item.ScheduledOn {
		t.Fatalf("update: %+v", r)
	}

	// 基线落后但字段不相交 → 合并并给出警告
	merged, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 1, itinerary.Patch{Notes: str("提前买票")})
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(merged, write.WarnMergedWithNewerVersion) {
		t.Fatalf("merge warning missing: %v", merged.Warnings)
	}
	if got := merged.Data.(itinerary.Resource); got.Title != "浅草寺雷门" || got.Notes != "提前买票" || got.Version != 3 {
		t.Fatalf("merged resource: %+v", got)
	}

	// 相交字段 → 412 并给出冲突字段
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 1, itinerary.Patch{Title: str("别的")})
	e := expectCode(t, err, 412, "VERSION_CONFLICT")
	if e.Conflict == nil || len(e.Conflict.ConflictingFields) != 1 || e.Conflict.ConflictingFields[0] != "title" {
		t.Fatalf("conflict: %+v", e.Conflict)
	}

	// 基线超前 → 412，无法判断相交字段
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 99, itinerary.Patch{Title: str("别的")})
	e = expectCode(t, err, 412, "VERSION_CONFLICT")
	if e.Conflict == nil || e.Conflict.ConflictingFields != nil {
		t.Fatalf("stale baseline conflict: %+v", e.Conflict)
	}

	// 空补丁
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 3, itinerary.Patch{})
	e = expectCode(t, err, 422, "VALIDATION_FAILED")
	if got := fieldCodes(e); !strings.Contains(got, "EMPTY_PATCH") {
		t.Fatalf("field errors = %s", got)
	}
}

func TestUpdateClearsNullableFields(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	item := f.create(t, itinerary.CreateCommand{
		Title: "浅草寺", PlannedStartLocal: str("2026-10-02T09:00:00"), PlannedDurationMinutes: i32(90),
		Latitude: f64(35.714800), Longitude: f64(139.796700), EstimatedAmount: str("100"), CurrencyCode: str("CNY"),
	})
	if item.EstimatedAmount == nil || *item.EstimatedAmount != "100.00" || item.CurrencyCode == nil {
		t.Fatalf("created: %+v", item)
	}

	res, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, int64(item.Version), itinerary.Patch{
		PlannedStartSet: true, PlannedDurationSet: true,
		LatitudeSet: true, LongitudeSet: true,
		EstimatedAmountSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Data.(itinerary.Resource)
	if r.PlannedStartLocal != nil || r.PlannedDurationMinutes != nil || r.Latitude != nil || r.Longitude != nil {
		t.Fatalf("nullable fields not cleared: %+v", r)
	}
	if r.EstimatedAmount != nil || r.CurrencyCode != nil {
		t.Fatalf("amount not cleared: %+v", r)
	}

	// 重新设置金额必须带币种
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, int64(r.Version), itinerary.Patch{
		EstimatedAmountSet: true, EstimatedAmount: str("8"),
	})
	e := expectCode(t, err, 422, "VALIDATION_FAILED")
	if got := fieldCodes(e); !strings.Contains(got, "currency_code:REQUIRED") {
		t.Fatalf("field errors = %s", got)
	}
	res, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, int64(r.Version), itinerary.Patch{
		EstimatedAmountSet: true, EstimatedAmount: str("8"), CurrencyCode: str("CNY"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Data.(itinerary.Resource); got.EstimatedAmount == nil || *got.EstimatedAmount != "8.00" || got.CurrencyCode == nil || *got.CurrencyCode != "CNY" {
		t.Fatalf("amount not set: %+v", got)
	}
}

func TestUpdateValidatesAgainstMergedValues(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	withEnd := f.create(t, itinerary.CreateCommand{
		Title: "浅草寺", PlannedStartLocal: str("2026-10-02T09:00:00"), PlannedEndLocal: str("2026-10-02T11:00:00"),
	})

	// 已有计划结束时再设时长 → 互斥
	_, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, withEnd.ID, int64(withEnd.Version), itinerary.Patch{
		PlannedDurationSet: true, PlannedDurationMinutes: i32(60),
	})
	e := expectCode(t, err, 422, "VALIDATION_FAILED")
	if got := fieldCodes(e); !strings.Contains(got, "planned_duration_minutes:EXCLUSIVE") {
		t.Fatalf("field errors = %s", got)
	}

	// 只改开始时间但晚于既有结束时间 → 顺序错误
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, withEnd.ID, int64(withEnd.Version), itinerary.Patch{
		PlannedStartSet: true, PlannedStartLocal: str("2026-10-02T12:00:00"),
	})
	expectCode(t, err, 422, "VALIDATION_FAILED")

	// 坐标必须成对提交
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, withEnd.ID, int64(withEnd.Version), itinerary.Patch{
		LatitudeSet: true, Latitude: f64(35.7148),
	})
	e = expectCode(t, err, 422, "VALIDATION_FAILED")
	if got := fieldCodes(e); !strings.Contains(got, "longitude:REQUIRED") {
		t.Fatalf("field errors = %s", got)
	}
}

func TestDelete(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	item := f.create(t, itinerary.CreateCommand{Title: "浅草寺"})

	_, err := f.svc.Delete(ctx, f.actor, uuid.New(), f.tripID, item.ID, 99)
	expectCode(t, err, 412, "VERSION_CONFLICT")

	res, err := f.svc.Delete(ctx, f.actor, uuid.New(), f.tripID, item.ID, int64(item.Version))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Data.(itinerary.Resource)
	if r.DeletedAt == nil || r.Version != item.Version+1 {
		t.Fatalf("deleted resource: %+v", r)
	}
	changes := f.uow.Changes()
	last := changes[len(changes)-1]
	if last.Kind != write.ChangeDelete || last.EntityID != item.ID {
		t.Fatalf("delete change: %+v", last)
	}

	_, err = f.svc.Get(ctx, f.actor, f.tripID, item.ID)
	expectCode(t, err, 410, "RESOURCE_GONE")
	_, err = f.svc.Delete(ctx, f.actor, uuid.New(), f.tripID, item.ID, int64(r.Version))
	expectCode(t, err, 410, "RESOURCE_GONE")

	// 删除后不再出现在列表，同一天新建项目的顺序从剩余最大值继续
	page, err := f.svc.List(ctx, f.actor, f.tripID, itinerary.Filters{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("list after delete: %+v, %v", page.Items, err)
	}
}

func TestReorderMovesItemsAcrossDays(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.create(t, itinerary.CreateCommand{Title: "A", ScheduledOn: "2026-10-02"})
	b := f.create(t, itinerary.CreateCommand{Title: "B", ScheduledOn: "2026-10-02"})
	c := f.create(t, itinerary.CreateCommand{Title: "C", ScheduledOn: "2026-10-02"})
	d := f.create(t, itinerary.CreateCommand{Title: "D", ScheduledOn: "2026-10-03"})

	before := len(f.uow.Changes())
	cmd := itinerary.ReorderCommand{Days: []itinerary.ReorderDay{
		{Date: "2026-10-02", Items: []itinerary.ReorderItem{{ID: c.ID, BaseVersion: 1}, {ID: a.ID, BaseVersion: 1}}},
		{Date: "2026-10-03", Items: []itinerary.ReorderItem{{ID: d.ID, BaseVersion: 1}, {ID: b.ID, BaseVersion: 1}}},
	}}
	res, err := f.svc.Reorder(ctx, f.actor, uuid.New(), f.tripID, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if res.Primary != nil || res.Data != nil {
		t.Fatalf("reorder has no primary resource: %+v", res)
	}
	// 只有实际变化的三条进入 affected，D 未变
	if len(res.Affected) != 3 {
		t.Fatalf("affected = %+v", res.Affected)
	}
	for _, ref := range res.Affected {
		if ref.ID == d.ID {
			t.Fatal("unchanged item must not be rewritten")
		}
	}
	changes := f.uow.Changes()[before:]
	if len(changes) != 3 {
		t.Fatalf("change count = %d", len(changes))
	}
	for _, ch := range changes {
		if len(ch.ChangedFields) != len(itinerary.PositionFields) {
			t.Fatalf("reorder changed_fields = %v", ch.ChangedFields)
		}
	}

	page, err := f.svc.List(ctx, f.actor, f.tripID, itinerary.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id    uuid.UUID
		day   types.Date
		order int32
	}{
		{c.ID, "2026-10-02", 0}, {a.ID, "2026-10-02", 1}, {d.ID, "2026-10-03", 0}, {b.ID, "2026-10-03", 1},
	}
	if len(page.Items) != len(want) {
		t.Fatalf("list size = %d", len(page.Items))
	}
	for i, w := range want {
		got := page.Items[i]
		if got.ID != w.id || got.ScheduledOn != w.day || got.SortOrder != w.order {
			t.Fatalf("item %d = %s %s %d, want %s %d", i, got.Title, got.ScheduledOn, got.SortOrder, w.day, w.order)
		}
	}
	if got := page.Items[2]; got.Version != 1 {
		t.Fatalf("unchanged item version = %d", got.Version)
	}
}

func TestReorderRejectsIncompleteSets(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.create(t, itinerary.CreateCommand{Title: "A", ScheduledOn: "2026-10-02"})
	b := f.create(t, itinerary.CreateCommand{Title: "B", ScheduledOn: "2026-10-02"})

	// 缺少当天的一项
	_, err := f.svc.Reorder(ctx, f.actor, uuid.New(), f.tripID, itinerary.ReorderCommand{Days: []itinerary.ReorderDay{
		{Date: "2026-10-02", Items: []itinerary.ReorderItem{{ID: a.ID, BaseVersion: 1}}},
	}})
	expectCode(t, err, 409, "ORDER_CHANGED")

	// 携带不属于这些日期的 ID
	_, err = f.svc.Reorder(ctx, f.actor, uuid.New(), f.tripID, itinerary.ReorderCommand{Days: []itinerary.ReorderDay{
		{Date: "2026-10-02", Items: []itinerary.ReorderItem{{ID: a.ID, BaseVersion: 1}, {ID: b.ID, BaseVersion: 1}, {ID: uuid.New(), BaseVersion: 1}}},
	}})
	expectCode(t, err, 409, "ORDER_CHANGED")

	// 同一 ID 出现两次
	_, err = f.svc.Reorder(ctx, f.actor, uuid.New(), f.tripID, itinerary.ReorderCommand{Days: []itinerary.ReorderDay{
		{Date: "2026-10-02", Items: []itinerary.ReorderItem{{ID: a.ID, BaseVersion: 1}, {ID: b.ID, BaseVersion: 1}}},
		{Date: "2026-10-03", Items: []itinerary.ReorderItem{{ID: a.ID, BaseVersion: 1}}},
	}})
	e := expectCode(t, err, 422, "VALIDATION_FAILED")
	if got := fieldCodes(e); !strings.Contains(got, "DUPLICATE") {
		t.Fatalf("field errors = %s", got)
	}

	// 版本不符
	_, err = f.svc.Reorder(ctx, f.actor, uuid.New(), f.tripID, itinerary.ReorderCommand{Days: []itinerary.ReorderDay{
		{Date: "2026-10-02", Items: []itinerary.ReorderItem{{ID: b.ID, BaseVersion: 1}, {ID: a.ID, BaseVersion: 7}}},
	}})
	conflict := expectCode(t, err, 412, "VERSION_CONFLICT")
	if conflict.Conflict == nil || conflict.Conflict.EntityID != a.ID || conflict.Conflict.ExpectedVersion != 7 {
		t.Fatalf("conflict: %+v", conflict.Conflict)
	}

	// 没有任何写入
	if got, _ := f.svc.Get(ctx, f.actor, f.tripID, a.ID); got.Version != 1 || got.SortOrder != 0 {
		t.Fatalf("failed reorder must not write: %+v", got)
	}
}

func TestReorderValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	_, err := f.svc.Reorder(ctx, f.actor, uuid.New(), f.tripID, itinerary.ReorderCommand{})
	e := expectCode(t, err, 422, "VALIDATION_FAILED")
	if got := fieldCodes(e); !strings.Contains(got, "days:REQUIRED") {
		t.Fatalf("field errors = %s", got)
	}

	_, err = f.svc.Reorder(ctx, f.actor, uuid.New(), f.tripID, itinerary.ReorderCommand{Days: []itinerary.ReorderDay{{Date: "2026-1-2"}}})
	expectCode(t, err, 422, "VALIDATION_FAILED")

	_, err = f.svc.Reorder(ctx, f.actor, uuid.New(), f.tripID, itinerary.ReorderCommand{Days: []itinerary.ReorderDay{
		{Date: "2026-10-02"}, {Date: "2026-10-02"},
	}})
	e = expectCode(t, err, 422, "VALIDATION_FAILED")
	if got := fieldCodes(e); !strings.Contains(got, "DUPLICATE") {
		t.Fatalf("field errors = %s", got)
	}
}

func TestReorderIsIdempotentAndWarnsOutsideTripDates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.create(t, itinerary.CreateCommand{Title: "A", ScheduledOn: "2026-10-02"})

	op := uuid.New()
	cmd := itinerary.ReorderCommand{Days: []itinerary.ReorderDay{
		{Date: "2026-10-02", Items: nil},
		{Date: "2026-10-20", Items: []itinerary.ReorderItem{{ID: a.ID, BaseVersion: 1}}},
	}}
	res, err := f.svc.Reorder(ctx, f.actor, op, f.tripID, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(res, write.WarnItineraryOutsideTripDates) {
		t.Fatalf("moving outside trip dates must warn: %v", res.Warnings)
	}
	replay, err := f.svc.Reorder(ctx, f.actor, op, f.tripID, cmd)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	if got, _ := f.svc.Get(ctx, f.actor, f.tripID, a.ID); got.ScheduledOn != types.Date("2026-10-20") || got.Version != 2 {
		t.Fatalf("replay must not rewrite: %+v", got)
	}
}
