package member_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
	"tripfolio/server/internal/foundation/collectionguard"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/member"
)

type fixture struct {
	store  *member.MemoryStore
	uow    *write.MemoryUnitOfWork[member.Repo]
	svc    *member.Service
	actor  actor.Actor
	tripID uuid.UUID
	self   member.Resource
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := member.NewMemoryStore()
	uow := write.NewMemoryUnitOfWork[member.Repo](store)
	clk := clock.NewFake(time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC))
	svc := member.NewService(uow, store, clk)
	a := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"}
	tripID := uuid.New()
	store.PutTrip(a.AccountID, tripID, member.TripInfo{})
	self := member.NewSelf(uuid.New(), tripID, clk.Now())
	store.Put(a.AccountID, self)
	return &fixture{store: store, uow: uow, svc: svc, actor: a, tripID: tripID, self: self}
}

func (f *fixture) save(t *testing.T, inputs ...member.Input) write.Result {
	t.Helper()
	res, err := f.svc.Save(context.Background(), f.actor, uuid.New(), f.tripID, member.SaveCommand{Members: inputs})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	return res
}

func (f *fixture) list(t *testing.T) []member.Resource {
	t.Helper()
	rows, err := f.svc.List(context.Background(), f.actor, f.tripID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return rows
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

func TestListStartsWithSelf(t *testing.T) {
	f := newFixture(t)
	rows := f.list(t)
	if len(rows) != 1 || !rows[0].IsSelf || rows[0].Name != "我" || rows[0].SharePercent != "100" {
		t.Fatalf("unexpected members: %+v", rows)
	}
}

func TestSaveCreatesUpdatesAndOrders(t *testing.T) {
	f := newFixture(t)
	friend := uuid.New()
	res := f.save(t,
		member.Input{ID: friend, Name: " 小王 ", SharePercent: "60.50"},
		member.Input{ID: f.self.ID, Name: "我自己", SharePercent: "39.5"},
	)
	if res.Primary != nil || res.Data != nil {
		t.Fatalf("expected no primary/data, got %+v", res)
	}
	if len(res.Affected) != 2 {
		t.Fatalf("expected 2 affected, got %+v", res.Affected)
	}
	rows := f.list(t)
	if len(rows) != 2 || rows[0].ID != friend || rows[0].Name != "小王" || rows[0].SharePercent != "60.5" || rows[0].SortOrder != 0 {
		t.Fatalf("unexpected first row: %+v", rows[0])
	}
	if rows[1].ID != f.self.ID || rows[1].Name != "我自己" || rows[1].SharePercent != "39.5" || rows[1].SortOrder != 1 || rows[1].Version != 2 {
		t.Fatalf("unexpected self row: %+v", rows[1])
	}
	changes := f.uow.Changes()
	if len(changes) != 2 || changes[0].EntityType != member.EntityType || changes[0].Kind != write.ChangeUpsert {
		t.Fatalf("unexpected changes: %+v", changes)
	}
	if got := strings.Join(changes[1].ChangedFields, ","); got != "name,share_percent,sort_order" {
		t.Fatalf("unexpected changed fields for self: %s", got)
	}

	// 未改动的成员不写变更日志
	res = f.save(t,
		member.Input{ID: friend, Name: "小王", SharePercent: "60.5"},
		member.Input{ID: f.self.ID, Name: "我自己", SharePercent: "39.50"},
	)
	if len(res.Affected) != 2 || len(f.uow.Changes()) != 2 {
		t.Fatalf("expected no-op save, got affected=%v changes=%d", res.Affected, len(f.uow.Changes()))
	}
}

func TestSaveDeletesUnlistedAndProtectsSelf(t *testing.T) {
	f := newFixture(t)
	friend := uuid.New()
	f.save(t, member.Input{ID: f.self.ID, Name: "我", SharePercent: "50"}, member.Input{ID: friend, Name: "小王", SharePercent: "50"})

	_, err := f.svc.Save(context.Background(), f.actor, uuid.New(), f.tripID, member.SaveCommand{Members: []member.Input{{ID: friend, Name: "小王", SharePercent: "100"}}})
	e := expectCode(t, err, 422, "VALIDATION_FAILED")
	if fieldCodes(e) != "members:SELF_REQUIRED" {
		t.Fatalf("unexpected fields: %s", fieldCodes(e))
	}

	f.store.SetReferences(friend, 2)
	_, err = f.svc.Save(context.Background(), f.actor, uuid.New(), f.tripID, member.SaveCommand{Members: []member.Input{{ID: f.self.ID, Name: "我", SharePercent: "100"}}})
	expectCode(t, err, 409, "MEMBER_IN_USE")
	if rows := f.list(t); len(rows) != 2 {
		t.Fatalf("failed save must roll back, got %d members", len(rows))
	}

	f.store.SetReferences(friend, 0)
	res := f.save(t, member.Input{ID: f.self.ID, Name: "我", SharePercent: "100"})
	if rows := f.list(t); len(rows) != 1 || rows[0].ID != f.self.ID {
		t.Fatalf("expected only self after delete, got %+v", rows)
	}
	changes := f.uow.Changes()
	last := changes[len(changes)-1]
	if last.Kind != write.ChangeDelete || last.EntityID != friend || len(res.Affected) != 2 {
		t.Fatalf("expected delete change for friend and self reordered, got %+v / %+v", last, res.Affected)
	}
}

func TestSaveValidation(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name   string
		inputs []member.Input
		status int
		code   string
		fields string
	}{
		{"empty", nil, 422, "VALIDATION_FAILED", "members:INVALID"},
		{"sum", []member.Input{{ID: f.self.ID, Name: "我", SharePercent: "40"}, {ID: uuid.New(), Name: "A", SharePercent: "40"}}, 422, "SHARE_PERCENT_SUM", ""},
		{"percent", []member.Input{{ID: f.self.ID, Name: "我", SharePercent: "100.001"}}, 422, "VALIDATION_FAILED", "members[0].share_percent:INVALID"},
		{"name", []member.Input{{ID: f.self.ID, Name: "   ", SharePercent: "100"}}, 422, "VALIDATION_FAILED", "members[0].name:INVALID"},
		{"dupName", []member.Input{{ID: f.self.ID, Name: "Tom", SharePercent: "50"}, {ID: uuid.New(), Name: "tom", SharePercent: "50"}}, 422, "VALIDATION_FAILED", "members[1].name:DUPLICATE"},
		{"dupID", []member.Input{{ID: f.self.ID, Name: "我", SharePercent: "50"}, {ID: f.self.ID, Name: "他", SharePercent: "50"}}, 422, "VALIDATION_FAILED", "members[1].id:DUPLICATE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.svc.Save(context.Background(), f.actor, uuid.New(), f.tripID, member.SaveCommand{Members: c.inputs})
			e := expectCode(t, err, c.status, c.code)
			if c.fields != "" && fieldCodes(e) != c.fields {
				t.Fatalf("unexpected fields: %s", fieldCodes(e))
			}
		})
	}
	tooMany := make([]member.Input, 0, member.MaxMembers+1)
	tooMany = append(tooMany, member.Input{ID: f.self.ID, Name: "我", SharePercent: "100"})
	for i := 0; i < member.MaxMembers; i++ {
		tooMany = append(tooMany, member.Input{ID: uuid.New(), Name: "m" + uuid.NewString()[:6], SharePercent: "0"})
	}
	_, err := f.svc.Save(context.Background(), f.actor, uuid.New(), f.tripID, member.SaveCommand{Members: tooMany})
	expectCode(t, err, 422, "VALIDATION_FAILED")
}

func TestSaveIDReuseAndTripGuards(t *testing.T) {
	f := newFixture(t)
	reused := uuid.New()
	f.store.AddTombstone(reused)
	_, err := f.svc.Save(context.Background(), f.actor, uuid.New(), f.tripID, member.SaveCommand{Members: []member.Input{
		{ID: f.self.ID, Name: "我", SharePercent: "50"}, {ID: reused, Name: "A", SharePercent: "50"},
	}})
	expectCode(t, err, 409, "ID_ALREADY_USED")

	other := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"}
	_, err = f.svc.List(context.Background(), other, f.tripID)
	expectCode(t, err, 404, "RESOURCE_NOT_FOUND")

	deleted := time.Now()
	f.store.PutTrip(f.actor.AccountID, f.tripID, member.TripInfo{DeletedAt: &deleted})
	_, err = f.svc.List(context.Background(), f.actor, f.tripID)
	expectCode(t, err, 410, "TRIP_DELETED")
}

func TestSaveReplaysWithSameOperation(t *testing.T) {
	f := newFixture(t)
	op := uuid.New()
	cmd := member.SaveCommand{Members: []member.Input{{ID: f.self.ID, Name: "我", SharePercent: "100.00"}}}
	first, err := f.svc.Save(context.Background(), f.actor, op, f.tripID, cmd)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	cmd.Members[0].SharePercent = "100"
	again, err := f.svc.Save(context.Background(), f.actor, op, f.tripID, cmd)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !again.Replayed || first.Replayed {
		t.Fatalf("expected replay flag, got first=%v again=%v", first.Replayed, again.Replayed)
	}
}

// The wrapper runs inside the real memory transaction and can reject before mutations.
type memberGuardUOW struct {
	inner    write.UnitOfWork[member.Repo]
	required []collectionguard.Scope
	err      error
	unknown  bool
	calls    int
}
type memberGuardScope struct {
	write.Scope
	owner *memberGuardUOW
}

func (s memberGuardScope) RequireCollections(_ context.Context, required []collectionguard.Scope) error {
	s.owner.calls++
	s.owner.required = append([]collectionguard.Scope(nil), required...)
	return s.owner.err
}
func (u *memberGuardUOW) Run(ctx context.Context, req write.Request, fn func(context.Context, write.Scope, member.Repo) error, reload func(context.Context, member.Repo) (any, error)) (write.Result, error) {
	return u.inner.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo member.Repo) error {
		if u.unknown {
			return fn(ctx, struct{ write.Scope }{scope}, repo)
		}
		return fn(ctx, memberGuardScope{scope, u}, repo)
	}, reload)
}

func TestMemberSaveRequiresBaselineBeforeNoOp(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmtBool(unknown), func(t *testing.T) {
			f := newFixture(t)
			u := &memberGuardUOW{inner: f.uow, err: apperr.New(428, "COLLECTION_BASE_REQUIRED", "required"), unknown: unknown}
			svc := member.NewService(u, f.store, clock.Real{})
			_, err := svc.Save(context.Background(), f.actor, uuid.New(), f.tripID, member.SaveCommand{Members: []member.Input{{ID: f.self.ID, Name: f.self.Name, SharePercent: f.self.SharePercent}}})
			if unknown {
				expectCode(t, err, 503, "DEPENDENCY_UNAVAILABLE")
			} else {
				expectCode(t, err, 428, "COLLECTION_BASE_REQUIRED")
				if u.calls != 1 || !reflect.DeepEqual(u.required, []collectionguard.Scope{{Kind: "members", ScopeID: f.tripID.String()}}) {
					t.Fatalf("required: %+v", u)
				}
			}
			if len(f.uow.Changes()) != 0 || !reflect.DeepEqual(f.list(t), []member.Resource{f.self}) {
				t.Fatal("rejected write changed members")
			}
		})
	}
}
func fmtBool(v bool) string {
	if v {
		return "unknown capability"
	}
	return "required baseline"
}

type memberSnapshotReader struct {
	member.Reader
	baseline member.ListBaseline
	err      error
}

func (r memberSnapshotReader) ListWithBaseline(context.Context, uuid.UUID, uuid.UUID) (member.ListBaseline, error) {
	return r.baseline, r.err
}
func TestMemberSnapshotCapability(t *testing.T) {
	// An embedded nil legacy Reader panics if the new path falls back to Trip/List.
	a := actor.Actor{AccountID: uuid.New()}
	svc := member.NewService(nil, struct{ member.Reader }{}, nil)
	_, err := svc.ListWithBaseline(context.Background(), a, uuid.New())
	expectCode(t, err, 503, "DEPENDENCY_UNAVAILABLE")
	expected := member.ListBaseline{Members: []member.Resource{{ID: uuid.New()}}, ScopeRevisions: []write.ScopeRevision{{Kind: "members", Revision: "snapshot"}}}
	svc = member.NewService(nil, memberSnapshotReader{baseline: expected}, nil)
	actual, err := svc.ListWithBaseline(context.Background(), a, uuid.New())
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("snapshot: %+v %v", actual, err)
	}
	svc = member.NewService(nil, memberSnapshotReader{}, nil)
	actual, err = svc.ListWithBaseline(context.Background(), a, uuid.New())
	if err != nil || actual.Members == nil {
		t.Fatalf("empty list: %+v %v", actual, err)
	}
	svc = member.NewService(nil, memberSnapshotReader{err: apperr.Gone("TRIP_DELETED", "deleted")}, nil)
	_, err = svc.ListWithBaseline(context.Background(), a, uuid.New())
	expectCode(t, err, 410, "TRIP_DELETED")
}
