package album

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/clock"
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
func (r *mediaTestRepo) Update(_ context.Context, _ uuid.UUID, v Resource) (Resource, error) {
	if v.ID == r.failID {
		return Resource{}, errors.New("injected update failure")
	}
	v.Version++
	r.rows[v.ID] = v
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
