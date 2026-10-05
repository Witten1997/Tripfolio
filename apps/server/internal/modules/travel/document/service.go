package document

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"reflect"
	"time"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/content"
)

type Patch struct {
	Title            *string    `json:"title"`
	Notes            *string    `json:"notes"`
	AssetID          *uuid.UUID `json:"asset_id"`
	ReservationID    *uuid.UUID `json:"reservation_id"`
	ReservationIDSet bool       `json:"reservation_id_set"`
}
type CreateCommand struct {
	ID uuid.UUID `json:"id"`
	Patch
}
type Filters struct {
	Kind             string
	ReservationID    *uuid.UUID
	DateFrom, DateTo string
	Cursor           string
	Limit            int
}
type Position struct {
	ID   uuid.UUID  `json:"id"`
	Time time.Time  `json:"time"`
	Day  types.Date `json:"day"`
	Sort int32      `json:"sort"`
}
type ListQuery struct {
	Filters
	After *Position
}
type Reader interface {
	content.TripReader
	Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (Resource, bool, error)
	List(context.Context, uuid.UUID, uuid.UUID, ListQuery) ([]Resource, error)
}
type Repo interface {
	content.TripReader
	Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (Resource, bool, error)
	IDExists(context.Context, uuid.UUID) (bool, error)
	Insert(context.Context, uuid.UUID, Resource) (Resource, error)
	Update(context.Context, uuid.UUID, Resource) (Resource, error)
	SoftDelete(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) (Resource, error)
	MergeSource() write.MergeSource
	content.AssetReader
	Reservation(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error)
}
type Service struct {
	uow     write.UnitOfWork[Repo]
	reader  Reader
	cursors paging.Codec
	clock   clock.Clock
}

func NewService(uow write.UnitOfWork[Repo], reader Reader, cursors paging.Codec, clk clock.Clock) *Service {
	return &Service{uow: uow, reader: reader, cursors: cursors, clock: clk}
}
func (p Patch) fields() []string {
	f := []string{}
	if p.Title != nil {
		f = append(f, "title")
	}
	if p.Notes != nil {
		f = append(f, "notes")
	}
	if p.AssetID != nil {
		f = append(f, "asset_id")
	}
	if p.ReservationIDSet {
		f = append(f, "reservation_id")
	}
	return f
}
func (p Patch) apply(v *Resource) {
	if p.Title != nil {
		v.Title = *p.Title
	}
	if p.Notes != nil {
		v.Notes = *p.Notes
	}
	if p.AssetID != nil {
		v.AssetID = *p.AssetID
	}
	if p.ReservationIDSet {
		v.ReservationID = p.ReservationID
	}
}
func (p Patch) validate() error {
	if err := content.Text("title", p.Title, 1, 200); err != nil {
		return err
	}
	if err := content.Text("notes", p.Notes, 0, 4000); err != nil {
		return err
	}
	if p.AssetID != nil && *p.AssetID == uuid.Nil {
		return apperr.Validation(apperr.Field("asset_id", "INVALID", "UUID 无效"))
	}
	if p.ReservationID != nil && *p.ReservationID == uuid.Nil {
		return apperr.Validation(apperr.Field("reservation_id", "INVALID", "UUID 无效"))
	}
	return nil
}
func validateResource(v Resource) error {
	return nil
}
func record(scope write.Scope, r Resource, kind write.ChangeKind, fields []string) {
	c := write.Change{EntityType: EntityType, EntityID: r.ID, TripID: &r.TripID, Version: int64(r.Version), Kind: kind}
	if kind == write.ChangeUpsert {
		c.Snapshot = r
		c.ChangedFields = fields
	}
	scope.Record(c)
	scope.SetPrimary(write.Ref(EntityType, r.ID, int64(r.Version)))
}
func (s *Service) Get(ctx context.Context, a actor.Actor, tripID, id uuid.UUID) (Resource, error) {
	if _, err := content.LoadTrip(ctx, s.reader, a.AccountID, tripID); err != nil {
		return Resource{}, err
	}
	v, ok, err := s.reader.Get(ctx, a.AccountID, tripID, id)
	if err != nil {
		if _, ok := apperr.As(err); ok {
			return v, err
		}
		return v, apperr.Internal(err)
	}
	if !ok {
		return v, apperr.NotFound()
	}
	if v.DeletedAt != nil {
		return v, content.Gone()
	}
	return v, nil
}
func (s *Service) List(ctx context.Context, a actor.Actor, tripID uuid.UUID, f Filters) (paging.Page[Resource], error) {
	empty := paging.Page[Resource]{Items: []Resource{}}
	if _, err := content.LoadTrip(ctx, s.reader, a.AccountID, tripID); err != nil {
		return empty, err
	}
	limit, err := paging.Limit(f.Limit)
	if err != nil {
		return empty, err
	}
	scope := fmt.Sprintf("documents|trip=%s|kind=%s|reservation=%v|from=%s|to=%s", tripID, f.Kind, f.ReservationID, f.DateFrom, f.DateTo)
	var pos *Position
	if f.Cursor != "" {
		var v Position
		if err := s.cursors.Decode(a.AccountID, scope, f.Cursor, &v); err != nil {
			return empty, paging.InvalidCursor()
		}
		pos = &v
	}
	f.Limit = limit + 1
	rows, err := s.reader.List(ctx, a.AccountID, tripID, ListQuery{Filters: f, After: pos})
	if err != nil {
		return empty, apperr.Internal(err)
	}
	if len(rows) > limit {
		last := rows[limit-1]
		p := Position{ID: last.ID}
		p.Time = last.CreatedAt
		token, err := s.cursors.Encode(a.AccountID, scope, p)
		if err != nil {
			return empty, apperr.Internal(err)
		}
		empty.NextCursor = &token
		rows = rows[:limit]
	}
	empty.Items = append(empty.Items, rows...)
	return empty, nil
}
func (s *Service) Create(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID, cmd CreateCommand) (write.Result, error) {
	empty := write.Result{}
	if cmd.ID == uuid.Nil {
		return empty, apperr.Validation(apperr.Field("id", "REQUIRED", "ID 必填"))
	}
	if err := cmd.Patch.validate(); err != nil {
		return empty, err
	}
	if cmd.Title == nil {
		return empty, apperr.Validation(apperr.Field("title", "REQUIRED", "字段必填"))
	}
	if cmd.AssetID == nil {
		return empty, apperr.Validation(apperr.Field("assetid", "REQUIRED", "字段必填"))
	}
	req := write.Request{AccountID: a.AccountID, OperationID: operationID, OperationType: EntityType + ".create", Fingerprint: write.Fingerprint(EntityType+".create", tripID.String()+"/"+cmd.ID.String(), nil, cmd)}
	result, err := s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		info, err := content.LoadTrip(ctx, repo, a.AccountID, tripID)
		if err != nil {
			return err
		}
		_ = info
		used, err := repo.IDExists(ctx, cmd.ID)
		if err != nil {
			return err
		}
		if used {
			return apperr.Conflicted("ID_ALREADY_USED", "ID 已被使用")
		}
		now := s.clock.Now()
		v := Resource{ID: cmd.ID, TripID: tripID, Version: 1, CreatedAt: now, UpdatedAt: now}
		cmd.Patch.apply(&v)
		if err := validateResource(v); err != nil {
			return err
		}
		if err := content.CheckAsset(ctx, repo, a.AccountID, tripID, v.AssetID, false); err != nil {
			return err
		}
		if v.ReservationID != nil {
			if err := checkReservation(ctx, repo, a.AccountID, tripID, *v.ReservationID); err != nil {
				return err
			}
		}
		created, err := repo.Insert(ctx, a.AccountID, v)
		if err != nil {
			return err
		}
		record(scope, created, write.ChangeUpsert, Fields)
		return nil
	}, s.reload(a, tripID, cmd.ID))
	return result, err
}
func (s *Service) Update(ctx context.Context, a actor.Actor, operationID, tripID, id uuid.UUID, base int64, p Patch) (write.Result, error) {
	if err := p.validate(); err != nil {
		return write.Result{}, err
	}
	fields := p.fields()
	if len(fields) == 0 {
		return write.Result{}, apperr.Validation(apperr.Field("", "EMPTY_PATCH", "没有可更新的字段"))
	}
	req := write.Request{AccountID: a.AccountID, OperationID: operationID, OperationType: EntityType + ".update", Fingerprint: write.Fingerprint(EntityType+".update", tripID.String()+"/"+id.String(), &base, p)}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if _, err := content.LoadTrip(ctx, repo, a.AccountID, tripID); err != nil {
			return err
		}
		v, ok, err := repo.Get(ctx, a.AccountID, tripID, id)
		if err != nil {
			return err
		}
		if !ok {
			return apperr.NotFound()
		}
		if v.DeletedAt != nil {
			return content.Gone()
		}
		d, err := write.ResolvePatch(ctx, repo.MergeSource(), a.AccountID, EntityType, id, base, int64(v.Version), fields)
		if err != nil {
			return err
		}
		if !d.Merge {
			return apperr.VersionConflict(&apperr.Conflict{EntityType: EntityType, EntityID: id, ExpectedVersion: base, CurrentVersion: int64(v.Version), ConflictingFields: d.Conflicting, Current: v})
		}
		if d.Merged {
			scope.Warn(write.WarnMergedWithNewerVersion)
		}
		before := v
		p.apply(&v)
		if err := validateResource(v); err != nil {
			return err
		}
		if err := content.CheckAsset(ctx, repo, a.AccountID, tripID, v.AssetID, false); err != nil {
			return err
		}
		if v.ReservationID != nil {
			if err := checkReservation(ctx, repo, a.AccountID, tripID, *v.ReservationID); err != nil {
				return err
			}
		}
		if reflect.DeepEqual(before, v) {
			scope.SetPrimary(write.Ref(EntityType, v.ID, int64(v.Version)))
			return nil
		}
		fields = actualChangedFields(before, v, fields)
		v.UpdatedAt = s.clock.Now()
		updated, err := repo.Update(ctx, a.AccountID, v)
		if err != nil {
			return err
		}
		record(scope, updated, write.ChangeUpsert, fields)
		return nil
	}, s.reload(a, tripID, id))
}
func (s *Service) Delete(ctx context.Context, a actor.Actor, operationID, tripID, id uuid.UUID, base int64) (write.Result, error) {
	req := write.Request{AccountID: a.AccountID, OperationID: operationID, OperationType: EntityType + ".delete", Fingerprint: write.Fingerprint(EntityType+".delete", tripID.String()+"/"+id.String(), &base, struct{}{})}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if _, err := content.LoadTrip(ctx, repo, a.AccountID, tripID); err != nil {
			return err
		}
		v, ok, err := repo.Get(ctx, a.AccountID, tripID, id)
		if err != nil {
			return err
		}
		if !ok {
			return apperr.NotFound()
		}
		if v.DeletedAt != nil {
			return content.Gone()
		}
		if int64(v.Version) != base {
			return apperr.VersionConflict(&apperr.Conflict{EntityType: EntityType, EntityID: id, ExpectedVersion: base, CurrentVersion: int64(v.Version), Current: v})
		}
		now := s.clock.Now()
		deleted, err := repo.SoftDelete(ctx, a.AccountID, tripID, id, now)
		if err != nil {
			return err
		}
		record(scope, deleted, write.ChangeDelete, nil)
		return nil
	}, s.reload(a, tripID, id))
}
func (s *Service) reload(a actor.Actor, tripID, id uuid.UUID) func(context.Context, Repo) (any, error) {
	return func(ctx context.Context, repo Repo) (any, error) {
		v, ok, err := repo.Get(ctx, a.AccountID, tripID, id)
		if err != nil || !ok {
			return nil, err
		}
		return v, nil
	}
}
func checkReservation(ctx context.Context, r Repo, accountID, tripID, id uuid.UUID) error {
	ok, err := r.Reservation(ctx, accountID, tripID, id)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.NotFound()
	}
	return nil
}

func actualChangedFields(before, after Resource, fields []string) []string {
	out := []string{}
	for _, field := range fields {
		same := false
		switch field {
		case "title":
			same = reflect.DeepEqual(before.Title, after.Title)
		case "notes":
			same = reflect.DeepEqual(before.Notes, after.Notes)
		case "asset_id":
			same = reflect.DeepEqual(before.AssetID, after.AssetID)
		case "reservation_id":
			same = reflect.DeepEqual(before.ReservationID, after.ReservationID)
		}
		if !same {
			out = append(out, field)
		}
	}
	return out
}
