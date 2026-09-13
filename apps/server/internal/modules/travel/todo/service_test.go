package todo_test

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
	"tripfolio/server/internal/modules/travel/todo"
)

type fixture struct {
	store  *todo.MemoryStore
	uow    *write.MemoryUnitOfWork[todo.Repo]
	svc    *todo.Service
	clock  *clock.Fake
	actor  actor.Actor
	tripID uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureInZone(t, "Asia/Shanghai", time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC))
}

func newFixtureInZone(t *testing.T, tz string, now time.Time) *fixture {
	t.Helper()
	store := todo.NewMemoryStore()
	uow := write.NewMemoryUnitOfWork[todo.Repo](store)
	store.SetMergeSource(uow)
	clk := clock.NewFake(now)
	svc := todo.NewService(uow, store, paging.InsecureCodec{}, clk)
	a := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"}
	tripID := uuid.New()
	store.PutTrip(a.AccountID, tripID, todo.TripInfo{Timezone: tz})
	return &fixture{store: store, uow: uow, svc: svc, clock: clk, actor: a, tripID: tripID}
}

func str(s string) *string { return &s }
func boolp(b bool) *bool   { return &b }

func (f *fixture) create(t *testing.T, cmd todo.CreateCommand) todo.Resource {
	t.Helper()
	if cmd.ID == uuid.Nil {
		cmd.ID = uuid.New()
	}
	if cmd.Title == "" {
		cmd.Title = "换日元"
	}
	res, err := f.svc.Create(context.Background(), f.actor, uuid.New(), f.tripID, cmd)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return res.Data.(todo.Resource)
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

func TestCreateAppliesDefaults(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res, err := f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, todo.CreateCommand{ID: uuid.New(), Title: "  换日元 "})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Data.(todo.Resource)
	if r.Title != "换日元" || r.DueOn != nil || r.Notes != "" || r.Completed || r.CompletedAt != nil || r.Version != 1 {
		t.Fatalf("defaults not applied: %+v", r)
	}
	if r.TripID != f.tripID {
		t.Fatalf("trip id: %+v", r)
	}
	changes := f.uow.Changes()
	if len(changes) != 1 || len(changes[0].ChangedFields) != len(todo.Fields) {
		t.Fatalf("change log: %+v", changes)
	}
}

func TestCreateCompletedWritesCompletedAt(t *testing.T) {
	f := newFixture(t)
	r := f.create(t, todo.CreateCommand{Title: "买保险", Completed: boolp(true), DueOn: str("2026-09-20")})
	if !r.Completed || r.CompletedAt == nil || !r.CompletedAt.Equal(f.clock.Now()) {
		t.Fatalf("completed_at not set by server: %+v", r)
	}
	if r.DueOn == nil || *r.DueOn != types.Date("2026-09-20") {
		t.Fatalf("due_on: %+v", r)
	}
}

func TestCreateValidation(t *testing.T) {
	base := func() todo.CreateCommand { return todo.CreateCommand{ID: uuid.New(), Title: "换日元"} }
	cases := []struct {
		name  string
		mut   func(c *todo.CreateCommand)
		codes string
	}{
		{"缺少 id", func(c *todo.CreateCommand) { c.ID = uuid.Nil }, "id:INVALID"},
		{"标题为空", func(c *todo.CreateCommand) { c.Title = "  " }, "title:INVALID"},
		{"标题过长", func(c *todo.CreateCommand) { c.Title = strings.Repeat("字", 201) }, "title:INVALID"},
		{"截止日期格式", func(c *todo.CreateCommand) { c.DueOn = str("2026-9-20") }, "due_on:INVALID"},
		{"备注过长", func(c *todo.CreateCommand) { c.Notes = str(strings.Repeat("字", 4001)) }, "notes:TOO_LONG"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			cmd := base()
			tc.mut(&cmd)
			_, err := f.svc.Create(context.Background(), f.actor, uuid.New(), f.tripID, cmd)
			e := expectCode(t, err, 422, "VALIDATION_FAILED")
			if got := fieldCodes(e); !strings.Contains(got, tc.codes) {
				t.Fatalf("field errors = %s, want %s", got, tc.codes)
			}
		})
	}
}

func TestCreateIdempotencyAndIDReuse(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	op := uuid.New()
	cmd := todo.CreateCommand{ID: uuid.New(), Title: "换日元"}
	if _, err := f.svc.Create(ctx, f.actor, op, f.tripID, cmd); err != nil {
		t.Fatal(err)
	}
	replay, err := f.svc.Create(ctx, f.actor, op, f.tripID, cmd)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	changed := cmd
	changed.Title = "买保险"
	_, err = f.svc.Create(ctx, f.actor, op, f.tripID, changed)
	expectCode(t, err, 409, "IDEMPOTENCY_CONFLICT")

	_, err = f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, cmd)
	expectCode(t, err, 409, "ID_ALREADY_USED")

	dead := uuid.New()
	f.store.AddTombstone(dead)
	_, err = f.svc.Create(ctx, f.actor, uuid.New(), f.tripID, todo.CreateCommand{ID: dead, Title: "x"})
	expectCode(t, err, 409, "ID_ALREADY_USED")
}

func TestTripScope(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cmd := todo.CreateCommand{ID: uuid.New(), Title: "换日元"}

	_, err := f.svc.Create(ctx, f.actor, uuid.New(), uuid.New(), cmd)
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")

	trashed := uuid.New()
	deletedAt := f.clock.Now()
	f.store.PutTrip(f.actor.AccountID, trashed, todo.TripInfo{Timezone: "Asia/Shanghai", DeletedAt: &deletedAt})
	_, err = f.svc.Create(ctx, f.actor, uuid.New(), trashed, cmd)
	expectCode(t, err, 410, "TRIP_DELETED")
	_, err = f.svc.List(ctx, f.actor, trashed, todo.Filters{})
	expectCode(t, err, 410, "TRIP_DELETED")
}

func TestListOrdersByDueDateWithNullsLast(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	late := f.create(t, todo.CreateCommand{Title: "晚", DueOn: str("2026-09-25")})
	none := f.create(t, todo.CreateCommand{Title: "无期限"})
	early := f.create(t, todo.CreateCommand{Title: "早", DueOn: str("2026-09-15")})

	page, err := f.svc.List(ctx, f.actor, f.tripID, todo.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].ID != early.ID || page.Items[1].ID != late.ID || page.Items[2].ID != none.ID {
		t.Fatalf("order: %+v", page.Items)
	}

	// 分页游标跨越空日期哨兵
	first, err := f.svc.List(ctx, f.actor, f.tripID, todo.Filters{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	next, err := f.svc.List(ctx, f.actor, f.tripID, todo.Filters{Limit: 2, Cursor: *first.NextCursor})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != none.ID {
		t.Fatalf("second page: %+v, %v", next, err)
	}
	_, err = f.svc.List(ctx, f.actor, f.tripID, todo.Filters{Limit: 2, State: "pending", Cursor: *first.NextCursor})
	expectCode(t, err, 400, "INVALID_CURSOR")
}

func TestListStatesAndOverdueUsesTripTimezone(t *testing.T) {
	// UTC 16:00 时东京已是次日 01:00，按旅行时区今天是 2026-09-14。
	f := newFixtureInZone(t, "Asia/Tokyo", time.Date(2026, 9, 13, 16, 0, 0, 0, time.UTC))
	ctx := context.Background()
	overdue := f.create(t, todo.CreateCommand{Title: "逾期", DueOn: str("2026-09-13")})
	today := f.create(t, todo.CreateCommand{Title: "今天", DueOn: str("2026-09-14")})
	done := f.create(t, todo.CreateCommand{Title: "已完成", DueOn: str("2026-09-01"), Completed: boolp(true)})

	all, err := f.svc.List(ctx, f.actor, f.tripID, todo.Filters{State: "all"})
	if err != nil || len(all.Items) != 3 {
		t.Fatalf("all: %+v, %v", all.Items, err)
	}
	byID := map[uuid.UUID]bool{}
	for _, it := range all.Items {
		byID[it.ID] = it.IsOverdue
	}
	if !byID[overdue.ID] {
		t.Fatal("due yesterday in trip timezone must be overdue")
	}
	if byID[today.ID] {
		t.Fatal("due today must not be overdue")
	}
	if byID[done.ID] {
		t.Fatal("completed todo must never be overdue")
	}

	pending, err := f.svc.List(ctx, f.actor, f.tripID, todo.Filters{State: "pending"})
	if err != nil || len(pending.Items) != 2 {
		t.Fatalf("pending: %+v, %v", pending.Items, err)
	}
	completed, err := f.svc.List(ctx, f.actor, f.tripID, todo.Filters{State: "completed"})
	if err != nil || len(completed.Items) != 1 || completed.Items[0].ID != done.ID {
		t.Fatalf("completed: %+v, %v", completed.Items, err)
	}
	overdueOnly, err := f.svc.List(ctx, f.actor, f.tripID, todo.Filters{State: "overdue"})
	if err != nil || len(overdueOnly.Items) != 1 || overdueOnly.Items[0].ID != overdue.ID {
		t.Fatalf("overdue: %+v, %v", overdueOnly.Items, err)
	}

	_, err = f.svc.List(ctx, f.actor, f.tripID, todo.Filters{State: "late"})
	expectCode(t, err, 422, "VALIDATION_FAILED")
}

func TestGet(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	item := f.create(t, todo.CreateCommand{Title: "换日元"})

	got, err := f.svc.Get(ctx, f.actor, f.tripID, item.ID)
	if err != nil || got.ID != item.ID {
		t.Fatalf("get: %+v, %v", got, err)
	}
	_, err = f.svc.Get(ctx, f.actor, f.tripID, uuid.New())
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")

	other := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"}
	_, err = f.svc.Get(ctx, other, f.tripID, item.ID)
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")
}

func TestUpdateCompletionAndClearing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	item := f.create(t, todo.CreateCommand{Title: "换日元", DueOn: str("2026-09-20"), Notes: str("机场柜台")})

	// 标记完成：completed_at 由服务端写入
	done, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 1, todo.Patch{Completed: boolp(true)})
	if err != nil {
		t.Fatal(err)
	}
	r := done.Data.(todo.Resource)
	if !r.Completed || r.CompletedAt == nil {
		t.Fatalf("not completed: %+v", r)
	}

	// 取消完成：completed_at 清空
	f.clock.Advance(time.Hour)
	undone, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, int64(r.Version), todo.Patch{Completed: boolp(false)})
	if err != nil {
		t.Fatal(err)
	}
	r2 := undone.Data.(todo.Resource)
	if r2.Completed || r2.CompletedAt != nil {
		t.Fatalf("not reopened: %+v", r2)
	}

	// 重复标记完成不改变已有 completed_at
	again, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, int64(r2.Version), todo.Patch{Completed: boolp(true)})
	if err != nil {
		t.Fatal(err)
	}
	first := again.Data.(todo.Resource).CompletedAt
	f.clock.Advance(time.Hour)
	stable, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, int64(again.Data.(todo.Resource).Version), todo.Patch{Completed: boolp(true)})
	if err != nil {
		t.Fatal(err)
	}
	if got := stable.Data.(todo.Resource).CompletedAt; got == nil || !got.Equal(*first) {
		t.Fatalf("completed_at must not move: %v vs %v", got, first)
	}

	// 显式清空截止日期
	cleared, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, int64(stable.Data.(todo.Resource).Version), todo.Patch{DueOnSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Data.(todo.Resource).DueOn != nil {
		t.Fatalf("due_on not cleared: %+v", cleared.Data)
	}
}

func TestUpdateMergeAndValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	item := f.create(t, todo.CreateCommand{Title: "换日元"})

	if _, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 1, todo.Patch{Title: str("换 5 万日元")}); err != nil {
		t.Fatal(err)
	}
	// 基线落后但字段不相交
	merged, err := f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 1, todo.Patch{Completed: boolp(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(merged, write.WarnMergedWithNewerVersion) {
		t.Fatalf("merge warning missing: %v", merged.Warnings)
	}
	// 相交字段
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 1, todo.Patch{Title: str("别的")})
	e := expectCode(t, err, 412, "VERSION_CONFLICT")
	if e.Conflict == nil || len(e.Conflict.ConflictingFields) != 1 || e.Conflict.ConflictingFields[0] != "title" {
		t.Fatalf("conflict: %+v", e.Conflict)
	}
	// 空补丁
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 3, todo.Patch{})
	e = expectCode(t, err, 422, "VALIDATION_FAILED")
	if got := fieldCodes(e); !strings.Contains(got, "EMPTY_PATCH") {
		t.Fatalf("field errors = %s", got)
	}
	// 非法截止日期
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 3, todo.Patch{DueOnSet: true, DueOn: str("20260920")})
	expectCode(t, err, 422, "VALIDATION_FAILED")
}

func TestDelete(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	item := f.create(t, todo.CreateCommand{Title: "换日元"})

	_, err := f.svc.Delete(ctx, f.actor, uuid.New(), f.tripID, item.ID, 99)
	expectCode(t, err, 412, "VERSION_CONFLICT")

	res, err := f.svc.Delete(ctx, f.actor, uuid.New(), f.tripID, item.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Data.(todo.Resource).DeletedAt == nil {
		t.Fatalf("not deleted: %+v", res.Data)
	}
	changes := f.uow.Changes()
	if last := changes[len(changes)-1]; last.Kind != write.ChangeDelete {
		t.Fatalf("delete change: %+v", last)
	}
	_, err = f.svc.Get(ctx, f.actor, f.tripID, item.ID)
	expectCode(t, err, 410, "RESOURCE_GONE")
	_, err = f.svc.Update(ctx, f.actor, uuid.New(), f.tripID, item.ID, 2, todo.Patch{Title: str("x")})
	expectCode(t, err, 410, "RESOURCE_GONE")

	page, err := f.svc.List(ctx, f.actor, f.tripID, todo.Filters{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("list after delete: %+v, %v", page.Items, err)
	}
}
