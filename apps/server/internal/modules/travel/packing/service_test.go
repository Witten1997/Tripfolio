package packing_test

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
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/packing"
)

type fixture struct {
	store  *packing.MemoryStore
	uow    *write.MemoryUnitOfWork[packing.Repo]
	svc    *packing.Service
	clock  *clock.Fake
	actor  actor.Actor
	tripID uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := packing.NewMemoryStore()
	uow := write.NewMemoryUnitOfWork[packing.Repo](store)
	store.SetMergeSource(uow)
	clk := clock.NewFake(time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC))
	svc := packing.NewService(uow, store, paging.InsecureCodec{}, clk)
	a := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"}
	tripID := uuid.New()
	store.PutTrip(a.AccountID, tripID, packing.TripInfo{})
	return &fixture{store: store, uow: uow, svc: svc, clock: clk, actor: a, tripID: tripID}
}

func str(s string) *string { return &s }
func i32(v int32) *int32   { return &v }

func (f *fixture) create(t *testing.T, cmd packing.CreateCommand) packing.Resource {
	t.Helper()
	if cmd.ID == uuid.Nil {
		cmd.ID = uuid.New()
	}
	if cmd.Name == "" {
		cmd.Name = "身份证"
	}
	if cmd.Category == "" {
		cmd.Category = "documents"
	}
	res, err := f.svc.Create(context.Background(), f.actor, uuid.New(), f.tripID, cmd)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return res.Data.(packing.Resource)
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

func TestCreateAppliesDefaults(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res, err := f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, packing.CreateCommand{
		ID: uuid.New(), Name: "  护照 ", Category: "documents",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Data.(packing.Resource)
	if r.Name != "护照" || r.Quantity != 1 || r.Status != packing.StatusPending || r.Notes != "" || r.Version != 1 {
		t.Fatalf("defaults not applied: %+v", r)
	}
	if r.TripID != f.tripID || r.DeletedAt != nil {
		t.Fatalf("unexpected state: %+v", r)
	}
	changes := f.uow.Changes()
	if len(changes) != 1 || changes[0].Kind != write.ChangeUpsert || len(changes[0].ChangedFields) != len(packing.Fields) {
		t.Fatalf("change log: %+v", changes)
	}
}

func TestCreateRejectsDuplicateName(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, packing.CreateCommand{Name: "身份证", Category: "documents"})

	// 同分类同名（大小写、空格不敏感）
	_, err := f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, packing.CreateCommand{ID: uuid.New(), Name: "  身份证 ", Category: "documents"})
	expectCode(t, err, 422, "VALIDATION_FAILED")

	// 不同分类同名可以
	other := f.create(t, packing.CreateCommand{Name: "身份证", Category: "other"})
	if other.Category != packing.CategoryOther {
		t.Fatalf("cross-category same name should be allowed: %+v", other)
	}
}

func TestCreateIdempotencyAndIDReuse(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id, op := uuid.New(), uuid.New()
	cmd := packing.CreateCommand{ID: id, Name: "身份证", Category: "documents", Quantity: i32(2), Notes: str("放钱包")}
	first, err := f.svc.Create(ctx, f.actor, op, f.tripID, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if first.Data.(packing.Resource).Quantity != 2 {
		t.Fatalf("quantity: %+v", first.Data)
	}
	replay, err := f.svc.Create(ctx, f.actor, op, f.tripID, cmd)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	if len(f.uow.Changes()) != 1 {
		t.Fatal("replay must not append changes")
	}
	changed := cmd
	changed.Name = "护照"
	_, err = f.svc.Create(ctx, f.actor, op, f.tripID, changed)
	expectCode(t, err, 409, "IDEMPOTENCY_CONFLICT")

	_, err = f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, cmd)
	expectCode(t, err, 409, "ID_ALREADY_USED")

	dead := uuid.New()
	f.store.AddTombstone(dead)
	_, err = f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, packing.CreateCommand{ID: dead, Name: "护照", Category: "documents"})
	expectCode(t, err, 409, "ID_ALREADY_USED")
}

func TestCreateValidation(t *testing.T) {
	base := func() packing.CreateCommand {
		return packing.CreateCommand{ID: uuid.New(), Name: "身份证", Category: "documents"}
	}
	cases := []struct {
		name  string
		mut   func(c *packing.CreateCommand)
		codes string
	}{
		{"缺少 id", func(c *packing.CreateCommand) { c.ID = uuid.Nil }, "id:INVALID"},
		{"名称为空", func(c *packing.CreateCommand) { c.Name = "   " }, "name:INVALID"},
		{"名称过长", func(c *packing.CreateCommand) { c.Name = strings.Repeat("字", 121) }, "name:INVALID"},
		{"未知分类", func(c *packing.CreateCommand) { c.Category = "gear" }, "category:INVALID"},
		{"数量为零", func(c *packing.CreateCommand) { c.Quantity = i32(0) }, "quantity:INVALID"},
		{"数量过大", func(c *packing.CreateCommand) { c.Quantity = i32(10000) }, "quantity:INVALID"},
		{"未知状态", func(c *packing.CreateCommand) { c.Status = str("done") }, "status:INVALID"},
		{"备注过长", func(c *packing.CreateCommand) { c.Notes = str(strings.Repeat("字", 2001)) }, "notes:TOO_LONG"},
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

func TestTripScope(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cmd := packing.CreateCommand{ID: uuid.New(), Name: "身份证", Category: "documents"}

	_, err := f.svc.Create(ctx, f.actor, uuid.New(), uuid.New(), cmd)
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")

	trashed := uuid.New()
	deletedAt := f.clock.Now()
	f.store.PutTrip(f.actor.AccountID, trashed, packing.TripInfo{DeletedAt: &deletedAt})
	_, err = f.svc.Create(ctx, f.actor, uuid.New(), trashed, cmd)
	expectCode(t, err, 410, "TRIP_DELETED")
	_, err = f.svc.List(ctx, f.actor, trashed, packing.Filters{})
	expectCode(t, err, 410, "TRIP_DELETED")
}

func TestGetAndList(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	doc := f.create(t, packing.CreateCommand{Name: "身份证", Category: "documents"})
	f.clock.Advance(time.Second)
	cloth := f.create(t, packing.CreateCommand{Name: "外套", Category: "clothing", Status: str("ready")})
	f.clock.Advance(time.Second)
	elec := f.create(t, packing.CreateCommand{Name: "充电宝", Category: "electronics"})

	got, err := f.svc.Get(ctx, f.actor, f.tripID, doc.ID)
	if err != nil || got.ID != doc.ID {
		t.Fatalf("get: %+v, %v", got, err)
	}

	// 列表按 category、created_at、id 升序：clothing < documents < electronics
	page, err := f.svc.List(ctx, f.actor, f.tripID, packing.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].ID != cloth.ID || page.Items[1].ID != doc.ID || page.Items[2].ID != elec.ID {
		t.Fatalf("list order: %+v", page.Items)
	}

	// 分类与状态筛选
	page, err = f.svc.List(ctx, f.actor, f.tripID, packing.Filters{Category: "clothing"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != cloth.ID {
		t.Fatalf("category filter: %+v, %v", page.Items, err)
	}
	page, err = f.svc.List(ctx, f.actor, f.tripID, packing.Filters{Status: "ready"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != cloth.ID {
		t.Fatalf("status filter: %+v, %v", page.Items, err)
	}

	// 分页
	first, err := f.svc.List(ctx, f.actor, f.tripID, packing.Filters{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	next, err := f.svc.List(ctx, f.actor, f.tripID, packing.Filters{Limit: 2, Cursor: *first.NextCursor})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != elec.ID {
		t.Fatalf("second page: %+v, %v", next, err)
	}
	_, err = f.svc.List(ctx, f.actor, f.tripID, packing.Filters{Limit: 2, Category: "documents", Cursor: *first.NextCursor})
	expectCode(t, err, 400, "INVALID_CURSOR")

	// 查询校验
	_, err = f.svc.List(ctx, f.actor, f.tripID, packing.Filters{Category: "gear"})
	expectCode(t, err, 422, "VALIDATION_FAILED")
	_, err = f.svc.List(ctx, f.actor, f.tripID, packing.Filters{Limit: 101})
	expectCode(t, err, 422, "VALIDATION_FAILED")
}

func TestUpdate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	item := f.create(t, packing.CreateCommand{Name: "身份证", Category: "documents"})

	// 状态推进
	res, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 1, packing.Patch{Status: statusPtr("packed")})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Data.(packing.Resource); got.Status != packing.StatusPacked || got.Version != 2 {
		t.Fatalf("update status: %+v", got)
	}

	// 字段级合并：改名与改数量不相交
	f.create(t, packing.CreateCommand{Name: "护照", Category: "documents"})
	merged, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 1, packing.Patch{Quantity: i32(3)})
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(merged, write.WarnMergedWithNewerVersion) {
		t.Fatalf("merge warning missing: %v", merged.Warnings)
	}

	// 改名撞车
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, int64(merged.Data.(packing.Resource).Version), packing.Patch{Name: str("护照")})
	expectCode(t, err, 422, "VALIDATION_FAILED")

	// 空补丁
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, int64(merged.Data.(packing.Resource).Version), packing.Patch{})
	e := expectCode(t, err, 422, "VALIDATION_FAILED")
	if got := fieldCodes(e); !strings.Contains(got, "EMPTY_PATCH") {
		t.Fatalf("field errors = %s", got)
	}
}

func TestDelete(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	item := f.create(t, packing.CreateCommand{Name: "身份证", Category: "documents"})

	_, err := f.svc.Delete(ctx, f.actor, uuid.New(), f.tripID, item.ID, 99)
	expectCode(t, err, 412, "VERSION_CONFLICT")

	res, err := f.svc.Delete(ctx, f.actor, uuid.New(), f.tripID, item.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Data.(packing.Resource).DeletedAt == nil {
		t.Fatalf("not deleted: %+v", res.Data)
	}
	_, err = f.svc.Get(ctx, f.actor, f.tripID, item.ID)
	expectCode(t, err, 410, "RESOURCE_GONE")

	// 删除后名称可复用
	reused := f.create(t, packing.CreateCommand{Name: "身份证", Category: "documents"})
	if reused.ID == item.ID {
		t.Fatal("expected new id")
	}
}

func TestBatchCreate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, packing.CreateCommand{Name: "身份证", Category: "documents"})

	op := uuid.New()
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cmd := packing.BatchCommand{Items: []packing.BatchItem{
		{ID: a, Name: "护照", Category: "documents"},
		{ID: b, Name: "  身份证 ", Category: "documents"}, // 与既有重复
		{ID: c, Name: "充电宝", Category: "electronics", Quantity: i32(2)},
		{ID: d, Name: "护照", Category: "documents"}, // 与请求内 a 重复
	}}
	res, err := f.svc.CreateBatch(ctx, f.actor, op, f.tripID, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.CreatedIDs) != 2 || len(res.Skipped) != 2 {
		t.Fatalf("created=%v skipped=%v", res.CreatedIDs, res.Skipped)
	}
	if res.Primary != nil || res.Data != nil {
		t.Fatalf("batch has no primary/data: %+v", res)
	}
	if len(res.Affected) != 2 {
		t.Fatalf("affected: %+v", res.Affected)
	}
	for _, s := range res.Skipped {
		if s.Reason != packing.ReasonDuplicate {
			t.Fatalf("skip reason: %+v", s)
		}
	}
	// 已创建物品在库中且状态 pending
	got, err := f.svc.Get(ctx, f.actor, f.tripID, c)
	if err != nil || got.Status != packing.StatusPending || got.Quantity != 2 {
		t.Fatalf("created item: %+v, %v", got, err)
	}

	// 幂等重放：created_ids 与 skipped 由 affected 重新推导，不重复写入
	replay, err := f.svc.CreateBatch(ctx, f.actor, op, f.tripID, cmd)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	if len(replay.CreatedIDs) != 2 || len(replay.Skipped) != 2 {
		t.Fatalf("replay detail: created=%v skipped=%v", replay.CreatedIDs, replay.Skipped)
	}
	if replay.CreatedIDs[0] != a || replay.CreatedIDs[1] != c {
		t.Fatalf("replay created order: %v", replay.CreatedIDs)
	}
	changesForA := 0
	for _, ch := range f.uow.Changes() {
		if ch.EntityID == a || ch.EntityID == c {
			changesForA++
		}
	}
	if changesForA != 2 {
		t.Fatalf("replay must not re-create, changes for created=%d", changesForA)
	}
}

func TestBatchCreateAllSkipped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, packing.CreateCommand{Name: "身份证", Category: "documents"})

	res, err := f.svc.CreateBatch(ctx, f.actor, uuid.New(), f.tripID, packing.BatchCommand{Items: []packing.BatchItem{
		{ID: uuid.New(), Name: "身份证", Category: "documents"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.CreatedIDs) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("all should be skipped: %+v", res)
	}
	if len(res.Affected) != 0 {
		t.Fatalf("affected should be empty: %+v", res.Affected)
	}
}

func TestBatchValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	_, err := f.svc.CreateBatch(ctx, f.actor, uuid.New(), f.tripID, packing.BatchCommand{})
	e := expectCode(t, err, 422, "VALIDATION_FAILED")
	if got := fieldCodes(e); !strings.Contains(got, "items:REQUIRED") {
		t.Fatalf("field errors = %s", got)
	}

	many := make([]packing.BatchItem, 101)
	for i := range many {
		many[i] = packing.BatchItem{ID: uuid.New(), Name: "物品", Category: "other"}
	}
	_, err = f.svc.CreateBatch(ctx, f.actor, uuid.New(), f.tripID, packing.BatchCommand{Items: many})
	expectCode(t, err, 422, "VALIDATION_FAILED")

	_, err = f.svc.CreateBatch(ctx, f.actor, uuid.New(), f.tripID, packing.BatchCommand{Items: []packing.BatchItem{
		{ID: uuid.New(), Name: "  ", Category: "documents"},
	}})
	expectCode(t, err, 422, "VALIDATION_FAILED")
}

func TestLibrary(t *testing.T) {
	f := newFixture(t)
	lib := f.svc.Library()
	if lib.Version != packing.LibraryVersion || len(lib.Categories) == 0 {
		t.Fatalf("library: %+v", lib)
	}
	for _, c := range lib.Categories {
		if !c.Category.Valid() || len(c.Items) == 0 {
			t.Fatalf("category: %+v", c)
		}
	}
}

func statusPtr(s string) *packing.Status {
	v := packing.Status(s)
	return &v
}

func hasWarning(res write.Result, code string) bool {
	for _, w := range res.Warnings {
		if w == code {
			return true
		}
	}
	return false
}
