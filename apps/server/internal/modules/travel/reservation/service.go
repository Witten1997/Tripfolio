package reservation

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
	"tripfolio/server/internal/modules/travel/document"
)

type Patch struct {
	Kind               *string              `json:"kind"`
	Title              *string              `json:"title"`
	BookingReference   *string              `json:"booking_reference"`
	TransportNumber    *string              `json:"transport_number"`
	TransportNumberSet bool                 `json:"transport_number_set"`
	ProviderName       *string              `json:"provider_name"`
	ProviderNameSet    bool                 `json:"provider_name_set"`
	StartLocal         *types.LocalDateTime `json:"start_local"`
	StartLocalSet      bool                 `json:"start_local_set"`
	EndLocal           *types.LocalDateTime `json:"end_local"`
	EndLocalSet        bool                 `json:"end_local_set"`
	Origin             *string              `json:"origin"`
	OriginSet          bool                 `json:"origin_set"`
	Destination        *string              `json:"destination"`
	DestinationSet     bool                 `json:"destination_set"`
	Address            *string              `json:"address"`
	ContactName        *string              `json:"contact_name"`
	ContactNameSet     bool                 `json:"contact_name_set"`
	ContactPhone       *string              `json:"contact_phone"`
	ContactPhoneSet    bool                 `json:"contact_phone_set"`
	Notes              *string              `json:"notes"`
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
	UnlinkDocuments(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) ([]document.Resource, error)
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
	if p.Kind != nil {
		f = append(f, "kind")
	}
	if p.Title != nil {
		f = append(f, "title")
	}
	if p.BookingReference != nil {
		f = append(f, "booking_reference")
	}
	if p.TransportNumberSet {
		f = append(f, "transport_number")
	}
	if p.ProviderNameSet {
		f = append(f, "provider_name")
	}
	if p.StartLocalSet {
		f = append(f, "start_local")
	}
	if p.EndLocalSet {
		f = append(f, "end_local")
	}
	if p.OriginSet {
		f = append(f, "origin")
	}
	if p.DestinationSet {
		f = append(f, "destination")
	}
	if p.Address != nil {
		f = append(f, "address")
	}
	if p.ContactNameSet {
		f = append(f, "contact_name")
	}
	if p.ContactPhoneSet {
		f = append(f, "contact_phone")
	}
	if p.Notes != nil {
		f = append(f, "notes")
	}
	return f
}
func (p Patch) apply(v *Resource) {
	if p.Kind != nil {
		v.Kind = *p.Kind
	}
	if p.Title != nil {
		v.Title = *p.Title
	}
	if p.BookingReference != nil {
		v.BookingReference = *p.BookingReference
	}
	if p.TransportNumberSet {
		v.TransportNumber = p.TransportNumber
	}
	if p.ProviderNameSet {
		v.ProviderName = p.ProviderName
	}
	if p.StartLocalSet {
		v.StartLocal = p.StartLocal
	}
	if p.EndLocalSet {
		v.EndLocal = p.EndLocal
	}
	if p.OriginSet {
		v.Origin = p.Origin
	}
	if p.DestinationSet {
		v.Destination = p.Destination
	}
	if p.Address != nil {
		v.Address = *p.Address
	}
	if p.ContactNameSet {
		v.ContactName = p.ContactName
	}
	if p.ContactPhoneSet {
		v.ContactPhone = p.ContactPhone
	}
	if p.Notes != nil {
		v.Notes = *p.Notes
	}
}
func (p Patch) validate() error {
	if err := content.Text("title", p.Title, 1, 200); err != nil {
		return err
	}
	if err := content.Text("booking_reference", p.BookingReference, 0, 120); err != nil {
		return err
	}
	if err := content.Text("transport_number", p.TransportNumber, 0, 80); err != nil {
		return err
	}
	if err := content.Text("provider_name", p.ProviderName, 0, 200); err != nil {
		return err
	}
	if err := content.Local("start_local", p.StartLocal); err != nil {
		return err
	}
	if err := content.Local("end_local", p.EndLocal); err != nil {
		return err
	}
	if err := content.Text("origin", p.Origin, 0, 300); err != nil {
		return err
	}
	if err := content.Text("destination", p.Destination, 0, 300); err != nil {
		return err
	}
	if err := content.Text("address", p.Address, 0, 500); err != nil {
		return err
	}
	if err := content.Text("contact_name", p.ContactName, 0, 120); err != nil {
		return err
	}
	if err := content.Text("contact_phone", p.ContactPhone, 0, 64); err != nil {
		return err
	}
	if err := content.Text("notes", p.Notes, 0, 10000); err != nil {
		return err
	}
	if p.Kind != nil && !validKind(*p.Kind) {
		return apperr.Validation(apperr.Field("kind", "INVALID", "预订类型无效"))
	}
	return nil
}
func validKind(k string) bool {
	return k == "transport" || k == "lodging" || k == "attraction" || k == "other"
}
func validateResource(v Resource) error {
	if v.Kind != "transport" && (v.TransportNumber != nil || v.Origin != nil || v.Destination != nil) {
		return apperr.Validation(apperr.Field("kind", "INVALID", "非交通预订不允许交通专属字段"))
	}
	if v.StartLocal != nil && v.EndLocal != nil && *v.EndLocal < *v.StartLocal {
		return apperr.Validation(apperr.Field("end_local", "INVALID", "结束时间不能早于开始时间"))
	}
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
	if f.Kind != "" && !validKind(f.Kind) {
		return empty, apperr.Validation(apperr.Field("kind", "INVALID", "预订类型无效"))
	}
	scope := fmt.Sprintf("reservations|trip=%s|kind=%s|reservation=%v|from=%s|to=%s", tripID, f.Kind, f.ReservationID, f.DateFrom, f.DateTo)
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
		p.Time = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		if last.StartLocal != nil {
			p.Time = last.StartLocal.Time()
		}
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
	if cmd.Kind == nil {
		return empty, apperr.Validation(apperr.Field("kind", "REQUIRED", "字段必填"))
	}
	if cmd.Title == nil {
		return empty, apperr.Validation(apperr.Field("title", "REQUIRED", "字段必填"))
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
		if p.Kind != nil && *p.Kind != v.Kind && *p.Kind != "transport" && (!p.TransportNumberSet || p.TransportNumber != nil || !p.OriginSet || p.Origin != nil || !p.DestinationSet || p.Destination != nil) {
			return apperr.Validation(apperr.Field("kind", "INVALID", "修改为非交通类型须显式清空交通专属字段"))
		}
		before := v
		p.apply(&v)
		if err := validateResource(v); err != nil {
			return err
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
		docs, err := repo.UnlinkDocuments(ctx, a.AccountID, tripID, id, now)
		if err != nil {
			return err
		}
		for _, d := range docs {
			scope.Record(write.Change{EntityType: document.EntityType, EntityID: d.ID, TripID: &tripID, Version: int64(d.Version), Kind: write.ChangeUpsert, Snapshot: d, ChangedFields: []string{"reservation_id"}})
		}
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

func actualChangedFields(before, after Resource, fields []string) []string {
	out := []string{}
	for _, field := range fields {
		same := false
		switch field {
		case "kind":
			same = reflect.DeepEqual(before.Kind, after.Kind)
		case "title":
			same = reflect.DeepEqual(before.Title, after.Title)
		case "booking_reference":
			same = reflect.DeepEqual(before.BookingReference, after.BookingReference)
		case "transport_number":
			same = reflect.DeepEqual(before.TransportNumber, after.TransportNumber)
		case "provider_name":
			same = reflect.DeepEqual(before.ProviderName, after.ProviderName)
		case "start_local":
			same = reflect.DeepEqual(before.StartLocal, after.StartLocal)
		case "end_local":
			same = reflect.DeepEqual(before.EndLocal, after.EndLocal)
		case "origin":
			same = reflect.DeepEqual(before.Origin, after.Origin)
		case "destination":
			same = reflect.DeepEqual(before.Destination, after.Destination)
		case "address":
			same = reflect.DeepEqual(before.Address, after.Address)
		case "contact_name":
			same = reflect.DeepEqual(before.ContactName, after.ContactName)
		case "contact_phone":
			same = reflect.DeepEqual(before.ContactPhone, after.ContactPhone)
		case "notes":
			same = reflect.DeepEqual(before.Notes, after.Notes)
		}
		if !same {
			out = append(out, field)
		}
	}
	return out
}
