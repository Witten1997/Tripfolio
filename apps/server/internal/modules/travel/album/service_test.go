package album

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/content"
)

type mediaTestRepo struct {
	Repo
	rows   map[uuid.UUID]Resource
	merge  write.MergeSource
	failID uuid.UUID
}

func (r *mediaTestRepo) Trip(context.Context, uuid.UUID, uuid.UUID) (content.TripInfo, bool, error) {
	return content.TripInfo{Timezone: "UTC"}, true, nil
}
func (r *mediaTestRepo) Asset(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (content.AssetInfo, bool, error) {
	return content.AssetInfo{MediaType: "image/png"}, true, nil
}
func (r *mediaTestRepo) Get(_ context.Context, _, _, id uuid.UUID) (Resource, bool, error) {
	v, ok := r.rows[id]
	return v, ok, nil
}
func (r *mediaTestRepo) MergeSource() write.MergeSource { return r.merge }
func (r *mediaTestRepo) IDExists(_ context.Context, id uuid.UUID) (bool, error) {
	_, used := r.rows[id]
	return used, nil
}
func (r *mediaTestRepo) Insert(_ context.Context, _ uuid.UUID, row Resource) (Resource, error) {
	r.rows[row.ID] = row
	return row, nil
}
func (r *mediaTestRepo) Update(_ context.Context, _ uuid.UUID, v Resource) (Resource, error) {
	if v.ID == r.failID {
		return Resource{}, errors.New("injected update failure")
	}
	v.Version++
	r.rows[v.ID] = v
	return v, nil
}
func (r *mediaTestRepo) SoftDelete(_ context.Context, _, _, id uuid.UUID, now time.Time) (Resource, error) {
	v := r.rows[id]
	v.DeletedAt = &now
	v.Version++
	r.rows[id] = v
	return v, nil
}
func (r *mediaTestRepo) ListForOrder(_ context.Context, _, trip uuid.UUID, day types.Date) ([]Resource, error) {
	out := []Resource{}
	for _, v := range r.rows {
		if v.TripID == trip && v.RecordedOn == day && v.DeletedAt == nil {
			out = append(out, v)
		}
	}
	return out, nil
}
func (r *mediaTestRepo) Snapshot() func() {
	before := maps.Clone(r.rows)
	return func() { r.rows = before }
}

func mediaServiceFixture() (*Service, *mediaTestRepo, *write.MemoryUnitOfWork[Repo], actor.Actor, Resource) {
	row := Resource{ID: uuid.New(), TripID: uuid.New(), AssetID: uuid.New(), RecordedOn: types.Date("2026-10-05"), Caption: "same", Version: 1}
	repo := &mediaTestRepo{rows: map[uuid.UUID]Resource{row.ID: row}}
	uow := write.NewMemoryUnitOfWork[Repo](repo)
	repo.merge = uow
	svc := NewService(uow, nil, nil, clock.NewFake(time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)), nil)
	return svc, repo, uow, actor.Actor{AccountID: uuid.New()}, row
}

type photoGuardScope struct {
	write.Scope
	required, affected [][]collectionguard.Scope
	beforeCheck        func()
	requireError       error
	recordError        error
}

func (s *photoGuardScope) RequireCollections(_ context.Context, required []collectionguard.Scope) error {
	s.beforeCheck()
	s.required = append(s.required, append([]collectionguard.Scope(nil), required...))
	return s.requireError
}
func (s *photoGuardScope) RecordCollections(_ context.Context, affected []collectionguard.Scope) error {
	s.beforeCheck()
	s.affected = append(s.affected, append([]collectionguard.Scope(nil), affected...))
	return s.recordError
}

type photoDecoratedUOW struct {
	*write.MemoryUnitOfWork[Repo]
	decorate func(write.Scope) write.Scope
}

func (u photoDecoratedUOW) Run(ctx context.Context, req write.Request, fn func(context.Context, write.Scope, Repo) error, reload func(context.Context, Repo) (any, error)) (write.Result, error) {
	return u.MemoryUnitOfWork.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		return fn(ctx, u.decorate(scope), repo)
	}, reload)
}

func TestPhotoPatchCollectionScopesBeforeWrite(t *testing.T) {
	day := types.Date("2026-10-05")
	other := types.Date("2026-10-06")
	taken := types.LocalDateTime("2026-10-06T08:00:00")
	zero := int32(0)
	caption, place := "changed", "place"
	asset := uuid.New()
	for _, tc := range []struct {
		name     string
		patch    Patch
		required []string
		finalDay types.Date
	}{
		{"same date", Patch{RecordedOn: &day}, []string{string(day)}, day},
		{"same order", Patch{SortOrder: &zero}, []string{string(day)}, day},
		{"clear absent time", Patch{TakenAtLocalSet: true}, []string{string(day)}, day},
		{"explicit move", Patch{RecordedOn: &other}, []string{string(day), string(other)}, other},
		{"implicit move", Patch{TakenAtLocal: &taken, TakenAtLocalSet: true}, []string{string(day), string(other)}, other},
		{"explicit date wins", Patch{TakenAtLocal: &taken, TakenAtLocalSet: true, RecordedOn: &day}, []string{string(day)}, day},
		{"caption", Patch{Caption: &caption}, nil, day},
		{"place", Patch{PlaceName: &place}, nil, day},
		{"asset", Patch{AssetID: &asset}, nil, day},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, uow, a, row := mediaServiceFixture()
			spy := &photoGuardScope{beforeCheck: func() {
				if !reflect.DeepEqual(repo.rows[row.ID], row) || len(uow.Changes()) != 0 {
					t.Fatal("collection check ran after a write")
				}
			}}
			svc.uow = photoDecoratedUOW{uow, func(s write.Scope) write.Scope { spy.Scope = s; return spy }}
			_, err := svc.Update(context.Background(), a, uuid.New(), row.TripID, row.ID, 1, tc.patch)
			if err != nil {
				t.Fatal(err)
			}
			if len(spy.required) != 1 || len(spy.affected) != 1 {
				t.Fatalf("missing collection calls %+v", spy)
			}
			var want []collectionguard.Scope
			for _, d := range tc.required {
				want = append(want, collectionguard.Scope{Kind: "photo_day", ScopeID: row.TripID.String() + "/" + d})
			}
			if !reflect.DeepEqual(spy.required[0], want) {
				t.Fatalf("required %+v want %+v", spy.required[0], want)
			}
			wantAffected := []collectionguard.Scope{{Kind: "photo_day", ScopeID: row.TripID.String() + "/" + string(day)}}
			if tc.finalDay != day {
				wantAffected = append(wantAffected, collectionguard.Scope{Kind: "photo_day", ScopeID: row.TripID.String() + "/" + string(tc.finalDay)})
			}
			if !reflect.DeepEqual(spy.affected[0], wantAffected) || repo.rows[row.ID].RecordedOn != tc.finalDay {
				t.Fatalf("affected %+v row %+v", spy.affected, repo.rows[row.ID])
			}
		})
	}
}

func TestPhotoCollectionFailureAndReceiptReplay(t *testing.T) {
	svc, repo, uow, a, row := mediaServiceFixture()
	spy := &photoGuardScope{beforeCheck: func() {}, requireError: apperr.New(428, "COLLECTION_BASE_REQUIRED", "missing")}
	svc.uow = photoDecoratedUOW{uow, func(s write.Scope) write.Scope { spy.Scope = s; return spy }}
	key := uuid.New()
	p := Patch{RecordedOn: &row.RecordedOn}
	_, err := svc.Update(context.Background(), a, key, row.TripID, row.ID, 1, p)
	if e, ok := apperr.As(err); !ok || e.Code != "COLLECTION_BASE_REQUIRED" {
		t.Fatalf("no-op bypassed missing guard: %v", err)
	}
	if len(spy.affected) != 0 || !reflect.DeepEqual(repo.rows[row.ID], row) || len(uow.Changes()) != 0 {
		t.Fatal("failed check committed")
	}
	spy.requireError = nil
	first, err := svc.Update(context.Background(), a, key, row.TripID, row.ID, 1, p)
	if err != nil || first.Primary == nil || *first.Primary.Version != 1 {
		t.Fatalf("no-op: %+v %v", first, err)
	}
	changed := row
	changed.Version = 2
	changed.Caption = "later"
	repo.rows[row.ID] = changed
	spy.requireError = apperr.New(412, "COLLECTION_CONFLICT", "stale")
	replay, err := svc.Update(context.Background(), a, key, row.TripID, row.ID, 1, p)
	if err != nil || !replay.Replayed || *replay.Primary.Version != 1 || replay.Data.(Resource).Version != 2 || len(spy.required) != 2 {
		t.Fatalf("original receipt rechecked: %+v %v", replay, err)
	}
}

func TestPhotoCreateDeleteRecordWithoutRequiredGuard(t *testing.T) {
	for _, kind := range []string{"create", "delete"} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failure=%t", kind, fail), func(t *testing.T) {
				svc, repo, uow, a, row := mediaServiceFixture()
				spy := &photoGuardScope{beforeCheck: func() {
					if len(uow.Changes()) != 0 || len(repo.rows) != 1 || !reflect.DeepEqual(repo.rows[row.ID], row) {
						t.Fatal("record ran after write")
					}
				}}
				if fail {
					spy.recordError = apperr.New(503, "DEPENDENCY_UNAVAILABLE", "unavailable")
				}
				svc.uow = photoDecoratedUOW{uow, func(s write.Scope) write.Scope { spy.Scope = s; return spy }}
				var err error
				if kind == "create" {
					_, err = svc.Create(context.Background(), a, uuid.New(), row.TripID, CreateCommand{ID: uuid.New(), Patch: Patch{AssetID: &row.AssetID, RecordedOn: &row.RecordedOn}})
				} else {
					_, err = svc.Delete(context.Background(), a, uuid.New(), row.TripID, row.ID, 1)
				}
				if fail {
					if e, ok := apperr.As(err); !ok || e.Code != "DEPENDENCY_UNAVAILABLE" {
						t.Fatalf("record failure: %v", err)
					}
					if len(repo.rows) != 1 || !reflect.DeepEqual(repo.rows[row.ID], row) || len(uow.Changes()) != 0 {
						t.Fatal("record failure wrote data")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				want := []collectionguard.Scope{{Kind: "photo_day", ScopeID: row.TripID.String() + "/" + string(row.RecordedOn)}}
				if len(spy.required) != 1 || len(spy.required[0]) != 0 || len(spy.affected) != 1 || !reflect.DeepEqual(spy.affected[0], want) {
					t.Fatalf("calls %+v", spy)
				}
			})
		}
	}
}

func TestPhotoRejectsScopeWithoutCollectionCapabilities(t *testing.T) {
	svc, repo, uow, a, row := mediaServiceFixture()
	svc.uow = photoDecoratedUOW{uow, func(s write.Scope) write.Scope { return struct{ write.Scope }{s} }}
	caption := "changed"
	_, err := svc.Update(context.Background(), a, uuid.New(), row.TripID, row.ID, 1, Patch{Caption: &caption})
	if e, ok := apperr.As(err); !ok || e.Code != "DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("missing capability: %v", err)
	}
	if !reflect.DeepEqual(repo.rows[row.ID], row) || len(uow.Changes()) != 0 {
		t.Fatal("missing capability wrote data")
	}
}

func TestPhotoUpdateNoopAndImplicitDate(t *testing.T) {
	svc, repo, uow, a, row := mediaServiceFixture()
	noopID := uuid.New()
	caption := "same"
	noop, err := svc.Update(context.Background(), a, noopID, row.TripID, row.ID, 1, Patch{Caption: &caption})
	if err != nil || noop.Primary == nil || len(uow.Changes()) != 0 || repo.rows[row.ID].Version != 1 {
		t.Fatalf("no-op %+v %v", noop, err)
	}
	taken := types.LocalDateTime("2026-10-06T10:00:00")
	_, err = svc.Update(context.Background(), a, uuid.New(), row.TripID, row.ID, 1, Patch{Caption: &caption, TakenAtLocal: &taken, TakenAtLocalSet: true})
	if err != nil {
		t.Fatal(err)
	}
	changes := uow.Changes()
	if len(changes) != 1 || !reflect.DeepEqual(changes[0].ChangedFields, []string{"taken_at_local", "recorded_on"}) || repo.rows[row.ID].RecordedOn != "2026-10-06" {
		t.Fatalf("derived fields %+v", changes)
	}
	replay, err := svc.Update(context.Background(), a, noopID, row.TripID, row.ID, 1, Patch{Caption: &caption})
	if err != nil || !replay.Replayed || *replay.Primary.Version != 1 || replay.Data.(Resource).Version != 2 {
		t.Fatalf("original no-op ref %+v %v", replay, err)
	}
}

func TestPhotoReorderCompleteSetAndOriginalReferences(t *testing.T) {
	svc, repo, uow, a, row := mediaServiceFixture()
	second := row
	second.ID = uuid.New()
	second.SortOrder = 1
	repo.rows[second.ID] = second
	for _, ids := range [][]uuid.UUID{{row.ID}, {row.ID, row.ID}, {row.ID, uuid.New()}} {
		if _, err := svc.Reorder(context.Background(), a, uuid.New(), row.TripID, row.RecordedOn, ids); err == nil {
			t.Fatal("accepted incomplete/duplicate/foreign set")
		}
	}
	noopID := uuid.New()
	noop, err := svc.Reorder(context.Background(), a, noopID, row.TripID, row.RecordedOn, []uuid.UUID{row.ID, second.ID})
	if err != nil || len(noop.Affected) != 2 || len(uow.Changes()) != 0 {
		t.Fatalf("no-op refs %+v %v", noop, err)
	}
	_, err = svc.Reorder(context.Background(), a, uuid.New(), row.TripID, row.RecordedOn, []uuid.UUID{second.ID, row.ID})
	if err != nil || len(uow.Changes()) != 2 {
		t.Fatalf("reorder %v", err)
	}
	replay, err := svc.Reorder(context.Background(), a, noopID, row.TripID, row.RecordedOn, []uuid.UUID{row.ID, second.ID})
	if err != nil || !replay.Replayed {
		t.Fatal(err)
	}
	for _, ref := range replay.Affected {
		if *ref.Version != 1 {
			t.Fatal("replay references drifted")
		}
	}
	for _, change := range uow.Changes() {
		if !reflect.DeepEqual(change.ChangedFields, []string{"sort_order"}) {
			t.Fatal("spurious reorder fields")
		}
	}
}

func TestPhotoReorderRollsBackMidBatchFailure(t *testing.T) {
	svc, repo, uow, a, row := mediaServiceFixture()
	second := row
	second.ID = uuid.New()
	second.SortOrder = 1
	repo.rows[second.ID] = second
	repo.failID = row.ID
	_, err := svc.Reorder(context.Background(), a, uuid.New(), row.TripID, row.RecordedOn, []uuid.UUID{second.ID, row.ID})
	if err == nil || repo.rows[second.ID].Version != 1 || repo.rows[second.ID].SortOrder != 1 || len(uow.Changes()) != 0 {
		t.Fatalf("partial mutation %v", err)
	}
}

func TestPhotoNoopAtStoredCoordinatePrecision(t *testing.T) {
	svc, repo, uow, a, row := mediaServiceFixture()
	lat, lon := 12.123456, 30.0
	row.Latitude, row.Longitude = &lat, &lon
	repo.rows[row.ID] = row
	input := 12.1234564
	result, err := svc.Update(context.Background(), a, uuid.New(), row.TripID, row.ID, 1, Patch{Latitude: &input, LatitudeSet: true})
	if err != nil || result.Primary == nil || *result.Primary.Version != 1 || len(uow.Changes()) != 0 {
		t.Fatalf("rounded no-op %+v %v", result, err)
	}
}

func TestPhotoCreateAppendsWithinFinalDay(t *testing.T) {
	zero := int32(0)
	for _, tc := range []struct {
		name                                  string
		activeOrder                           int32
		empty, deleted, otherDay, implicitDay bool
		explicit                              *int32
		want                                  int32
		overflow                              bool
	}{
		{name: "empty", empty: true, want: 0},
		{name: "existing sparse order", activeOrder: 7, want: 8},
		{name: "all deleted", activeOrder: math.MaxInt32, deleted: true, want: 0},
		{name: "other day ignored", activeOrder: math.MaxInt32, otherDay: true, want: 0},
		{name: "implicit final day", activeOrder: 4, implicitDay: true, want: 5},
		{name: "explicit zero retained", activeOrder: math.MaxInt32, explicit: &zero, want: 0},
		{name: "overflow", activeOrder: math.MaxInt32, overflow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, uow, a, seed := mediaServiceFixture()
			seed.SortOrder = tc.activeOrder
			if tc.deleted {
				now := time.Now()
				seed.DeletedAt = &now
			}
			if tc.otherDay || tc.implicitDay {
				seed.RecordedOn = types.Date("2026-10-06")
			}
			repo.rows[seed.ID] = seed
			if tc.empty {
				delete(repo.rows, seed.ID)
			}
			// A deleted maximum never participates in the next active order.
			removed := seed
			removed.ID = uuid.New()
			removed.SortOrder = math.MaxInt32
			now := time.Now()
			removed.DeletedAt = &now
			repo.rows[removed.ID] = removed
			day := types.Date("2026-10-05")
			patch := Patch{AssetID: &seed.AssetID, RecordedOn: &day, SortOrder: tc.explicit}
			if tc.implicitDay {
				taken := types.LocalDateTime("2026-10-06T12:00:00")
				patch.RecordedOn = nil
				patch.TakenAtLocal = &taken
				patch.TakenAtLocalSet = true
			}
			id := uuid.New()
			_, err := svc.Create(context.Background(), a, uuid.New(), seed.TripID, CreateCommand{ID: id, Patch: patch})
			if tc.overflow {
				e, ok := apperr.As(err)
				if !ok || e.Code != "SORT_ORDER_OVERFLOW" || len(uow.Changes()) != 0 {
					t.Fatalf("overflow %v", err)
				}
				if _, exists := repo.rows[id]; exists {
					t.Fatal("overflow inserted photo")
				}
				return
			}
			if err != nil || repo.rows[id].SortOrder != tc.want {
				t.Fatalf("order=%d want=%d err=%v", repo.rows[id].SortOrder, tc.want, err)
			}
			if tc.implicitDay && repo.rows[id].RecordedOn != "2026-10-06" {
				t.Fatal("order used unresolved date")
			}
		})
	}
}

func TestPhotoCreateOmittedOrderReceiptDiffersFromExplicitZero(t *testing.T) {
	svc, repo, uow, a, seed := mediaServiceFixture()
	cmd := CreateCommand{ID: uuid.New(), Patch: Patch{AssetID: &seed.AssetID, RecordedOn: &seed.RecordedOn}}
	op := uuid.New()
	first, err := svc.Create(context.Background(), a, op, seed.TripID, cmd)
	if err != nil || repo.rows[cmd.ID].SortOrder != 1 {
		t.Fatalf("append %+v %v", first, err)
	}
	replay, err := svc.Create(context.Background(), a, op, seed.TripID, cmd)
	if err != nil || !replay.Replayed || len(uow.Changes()) != 1 {
		t.Fatal("append replay mutated collection")
	}
	zero := int32(0)
	cmd.SortOrder = &zero
	_, err = svc.Create(context.Background(), a, op, seed.TripID, cmd)
	if e, ok := apperr.As(err); !ok || e.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("explicit zero conflated with missing: %v", err)
	}
}
