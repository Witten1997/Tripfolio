package finance_test

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/finance"
)

type categoryPlainScope struct{ events *[]string }

func (s *categoryPlainScope) Record(write.Change)       { *s.events = append(*s.events, "change") }
func (*categoryPlainScope) Warn(string)                 {}
func (*categoryPlainScope) SetPrimary(write.EntityRef)  {}
func (*categoryPlainScope) AddAffected(write.EntityRef) {}
func (*categoryPlainScope) Enqueue(write.JobArgs) error { return nil }

type categoryGuardScope struct {
	*categoryPlainScope
	required, recorded    []collectionguard.Scope
	requireErr, recordErr error
}

func (s *categoryGuardScope) RequireCollections(_ context.Context, scopes []collectionguard.Scope) error {
	*s.events = append(*s.events, "require")
	s.required = append([]collectionguard.Scope{}, scopes...)
	return s.requireErr
}
func (s *categoryGuardScope) RecordCollections(_ context.Context, scopes []collectionguard.Scope) error {
	*s.events = append(*s.events, "record-scopes")
	s.recorded = append([]collectionguard.Scope{}, scopes...)
	return s.recordErr
}

type categoryRepoSpy struct {
	finance.CategoryRepo
	events  *[]string
	current *finance.CategoryResource
	max     int32
	writes  int
}

func (r *categoryRepoSpy) GetForUpdate(context.Context, uuid.UUID, uuid.UUID) (finance.CategoryResource, bool, error) {
	*r.events = append(*r.events, "get-for-update")
	if r.current == nil {
		return finance.CategoryResource{}, false, nil
	}
	return *r.current, true, nil
}
func (r *categoryRepoSpy) Get(context.Context, uuid.UUID, uuid.UUID) (finance.CategoryResource, bool, error) {
	if r.current == nil {
		return finance.CategoryResource{}, false, nil
	}
	return *r.current, true, nil
}
func (r *categoryRepoSpy) MergeSource() write.MergeSource {
	*r.events = append(*r.events, "merge-source")
	return nil // Equal-version tests must not need history.
}
func (r *categoryRepoSpy) IDExists(context.Context, uuid.UUID) (bool, error)      { return false, nil }
func (r *categoryRepoSpy) MaxSortOrder(context.Context, uuid.UUID) (int32, error) { return r.max, nil }
func (r *categoryRepoSpy) Insert(_ context.Context, _ uuid.UUID, c finance.CategoryResource) (finance.CategoryResource, error) {
	*r.events = append(*r.events, "insert")
	r.writes++
	r.current = &c
	return c, nil
}
func (r *categoryRepoSpy) Update(_ context.Context, _, _ uuid.UUID, name string, icon *string, order int32, now time.Time) (finance.CategoryResource, error) {
	*r.events = append(*r.events, "update")
	r.writes++
	c := *r.current
	c.Name, c.Icon, c.SortOrder, c.UpdatedAt = name, icon, order, now
	c.Version++
	r.current = &c
	return c, nil
}
func (*categoryRepoSpy) LedgerEntriesUsing(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	return 0, nil
}
func (r *categoryRepoSpy) SoftDelete(_ context.Context, _, _ uuid.UUID, now time.Time) (finance.CategoryResource, error) {
	*r.events = append(*r.events, "delete")
	r.writes++
	c := *r.current
	c.DeletedAt = &now
	c.Version++
	r.current = &c
	return c, nil
}
func (r *categoryRepoSpy) ListForOrder(context.Context, uuid.UUID) ([]finance.CategoryResource, error) {
	*r.events = append(*r.events, "list-order")
	if r.current == nil {
		return []finance.CategoryResource{}, nil
	}
	return []finance.CategoryResource{*r.current}, nil
}

type categoryTestUOW struct {
	scope write.Scope
	repo  *categoryRepoSpy
}

func (u *categoryTestUOW) Run(ctx context.Context, _ write.Request, fn func(context.Context, write.Scope, finance.CategoryRepo) error, reload func(context.Context, finance.CategoryRepo) (any, error)) (write.Result, error) {
	if err := fn(ctx, u.scope, u.repo); err != nil {
		return write.Result{}, err
	}
	var result write.Result
	var err error
	if reload != nil {
		result.Data, err = reload(ctx, u.repo)
	}
	return result, err
}
func categoryServiceFixture() (*finance.CategoryService, *categoryTestUOW, *categoryGuardScope, actor.Actor) {
	events := []string{}
	scope := &categoryGuardScope{categoryPlainScope: &categoryPlainScope{events: &events}}
	repo := &categoryRepoSpy{events: &events, current: &finance.CategoryResource{ID: uuid.New(), Name: "交通", Version: 1}}
	uow := &categoryTestUOW{scope: scope, repo: repo}
	a := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb}
	return finance.NewCategoryService(uow, nil, clock.Real{}), uow, scope, a
}
func categoryErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}
func categoryExpected(a actor.Actor) []collectionguard.Scope {
	return []collectionguard.Scope{{Kind: "categories", ScopeID: a.AccountID.String()}}
}

func TestCategoryCollectionsUpdateChecksBeforeMergeAndNoop(t *testing.T) {
	zero, changedOrder := int32(0), int32(3)
	name := "交通"
	for _, tc := range []struct {
		name      string
		patch     finance.CategoryPatch
		sensitive bool
		writes    int
	}{
		{"explicit zero noop", finance.CategoryPatch{SortOrder: &zero}, true, 0},
		{"changed order", finance.CategoryPatch{SortOrder: &changedOrder}, true, 1},
		{"same name", finance.CategoryPatch{Name: &name}, false, 0},
		{"clear icon noop", finance.CategoryPatch{IconSet: true}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, uow, scope, a := categoryServiceFixture()
			_, err := svc.Update(context.Background(), a, uuid.New(), uow.repo.current.ID, 1, tc.patch)
			if err != nil {
				t.Fatal(err)
			}
			if uow.repo.writes != tc.writes {
				t.Fatal("unexpected writes")
			}
			want := []collectionguard.Scope{}
			if tc.sensitive {
				want = categoryExpected(a)
			} else if !reflect.DeepEqual(scope.recorded, categoryExpected(a)) {
				t.Fatal("non-sort update lost collection facts")
			}
			if !reflect.DeepEqual(scope.required, want) {
				t.Fatalf("required=%v", scope.required)
			}
			events := *scope.events
			if len(events) < 3 || events[0] != "get-for-update" || events[1] != "require" {
				t.Fatalf("ordering %v", events)
			}
		})
	}
	for _, client := range []actor.ClientKind{actor.ClientWeb, actor.ClientAndroid, actor.ClientHarmony} {
		svc, uow, scope, a := categoryServiceFixture()
		a.ClientKind = client
		scope.requireErr = apperr.New(428, "COLLECTION_BASE_REQUIRED", "")
		_, err := svc.Update(context.Background(), a, uuid.New(), uow.repo.current.ID, 1, finance.CategoryPatch{SortOrder: &zero})
		categoryErrorCode(t, err, "COLLECTION_BASE_REQUIRED")
		if uow.repo.writes != 0 || !reflect.DeepEqual(*scope.events, []string{"get-for-update", "require"}) {
			t.Fatal("guard checked after merge/write")
		}
	}
}

func TestCategoryCollectionsCreateDeleteAndRecordFailure(t *testing.T) {
	zero := int32(0)
	for _, tc := range []struct {
		name     string
		max      int32
		explicit *int32
		want     int32
		code     string
	}{
		{"append", 5, nil, 6, ""}, {"empty", -1, nil, 0, ""}, {"explicit zero", 5, &zero, 0, ""}, {"overflow", math.MaxInt32, nil, 0, "SORT_ORDER_EXHAUSTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, uow, scope, a := categoryServiceFixture()
			uow.repo.max = tc.max
			_, err := svc.Create(context.Background(), a, uuid.New(), finance.CreateCategoryCommand{ID: uuid.New(), Name: "新分类", SortOrder: tc.explicit})
			if tc.code != "" {
				categoryErrorCode(t, err, tc.code)
				if uow.repo.writes != 0 {
					t.Fatal("overflow wrote")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(scope.required) != 0 || !reflect.DeepEqual(scope.recorded, categoryExpected(a)) || uow.repo.current.SortOrder != tc.want {
				t.Fatal("create scope/order changed")
			}
		})
	}
	for _, action := range []string{"create", "delete", "name"} {
		for _, fail := range []string{"", "require", "record"} {
			t.Run(action+"/"+fail, func(t *testing.T) {
				svc, uow, scope, a := categoryServiceFixture()
				if fail == "require" {
					scope.requireErr = apperr.Unprocessable("INVALID_REFERENCE", "")
				}
				if fail == "record" {
					scope.recordErr = apperr.New(503, "DEPENDENCY_UNAVAILABLE", "")
				}
				var err error
				switch action {
				case "create":
					_, err = svc.Create(context.Background(), a, uuid.New(), finance.CreateCategoryCommand{ID: uuid.New(), Name: "新增"})
				case "delete":
					_, err = svc.Delete(context.Background(), a, uuid.New(), uow.repo.current.ID, 1)
				case "name":
					name := "新名称"
					_, err = svc.Update(context.Background(), a, uuid.New(), uow.repo.current.ID, 1, finance.CategoryPatch{Name: &name})
				}
				if fail != "" {
					if err == nil || uow.repo.writes != 0 {
						t.Fatal("failure allowed write")
					}
					return
				}
				if err != nil || len(scope.required) != 0 || !reflect.DeepEqual(scope.recorded, categoryExpected(a)) {
					t.Fatalf("empty require/facts %v", err)
				}
			})
		}
	}
}

func TestCategoryCollectionsReorderChecksBeforeReadIncludingEmpty(t *testing.T) {
	for _, empty := range []bool{false, true} {
		for _, reject := range []bool{false, true} {
			svc, uow, scope, a := categoryServiceFixture()
			ids := []uuid.UUID{uow.repo.current.ID}
			if empty {
				uow.repo.current = nil
				ids = nil
			}
			if reject {
				scope.requireErr = apperr.New(412, "COLLECTION_CONFLICT", "")
			}
			_, err := svc.Reorder(context.Background(), a, uuid.New(), ids)
			if !reflect.DeepEqual(scope.required, categoryExpected(a)) {
				t.Fatal("reorder missing full scope")
			}
			if reject {
				categoryErrorCode(t, err, "COLLECTION_CONFLICT")
				if !reflect.DeepEqual(*scope.events, []string{"require"}) {
					t.Fatal("read before guard")
				}
			} else if err != nil || (*scope.events)[0] != "require" || (*scope.events)[1] != "list-order" {
				t.Fatalf("bad order %v %v", *scope.events, err)
			}
			if uow.repo.writes != 0 {
				t.Fatal("no-op wrote")
			}
		}
	}
}

func TestCategoryCollectionsUnknownCapabilityAndEntityErrors(t *testing.T) {
	for _, action := range []string{"create", "update", "delete", "reorder"} {
		svc, uow, scope, a := categoryServiceFixture()
		uow.scope = scope.categoryPlainScope
		id := uow.repo.current.ID
		zero := int32(0)
		var err error
		switch action {
		case "create":
			_, err = svc.Create(context.Background(), a, uuid.New(), finance.CreateCategoryCommand{ID: uuid.New(), Name: "新增"})
		case "update":
			_, err = svc.Update(context.Background(), a, uuid.New(), id, 1, finance.CategoryPatch{SortOrder: &zero})
		case "delete":
			_, err = svc.Delete(context.Background(), a, uuid.New(), id, 1)
		case "reorder":
			_, err = svc.Reorder(context.Background(), a, uuid.New(), []uuid.UUID{id})
		}
		categoryErrorCode(t, err, "DEPENDENCY_UNAVAILABLE")
		if uow.repo.writes != 0 {
			t.Fatal("missing capability wrote")
		}
	}
	for _, deleted := range []bool{false, true} {
		svc, uow, scope, a := categoryServiceFixture()
		id := uow.repo.current.ID
		code := "RESOURCE_NOT_FOUND"
		if deleted {
			now := time.Now()
			uow.repo.current.DeletedAt = &now
			code = "RESOURCE_GONE"
		} else {
			uow.repo.current = nil
		}
		zero := int32(0)
		_, err := svc.Update(context.Background(), a, uuid.New(), id, 1, finance.CategoryPatch{SortOrder: &zero})
		categoryErrorCode(t, err, code)
		if len(*scope.events) != 1 {
			t.Fatal("invalid entity reached guard")
		}
	}
}
