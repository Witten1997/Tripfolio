package album

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"math"
	"reflect"
	"strconv"
	"time"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/assets"
	"tripfolio/server/internal/modules/travel/content"
)

type Patch struct {
	AssetID         *uuid.UUID           `json:"asset_id"`
	TakenAtLocal    *types.LocalDateTime `json:"taken_at_local"`
	TakenAtLocalSet bool                 `json:"taken_at_local_set"`
	RecordedOn      *types.Date          `json:"recorded_on"`
	Caption         *string              `json:"caption"`
	SortOrder       *int32               `json:"sort_order"`
	PlaceName       *string              `json:"place_name"`
	Address         *string              `json:"address"`
	Latitude        *float64             `json:"latitude"`
	LatitudeSet     bool                 `json:"latitude_set"`
	Longitude       *float64             `json:"longitude"`
	LongitudeSet    bool                 `json:"longitude_set"`
}
type CreateCommand struct {
	ID uuid.UUID `json:"id"`
	Patch
	Asset *InlineAsset `json:"asset"`
}
type InlineAsset struct {
	OriginalName      string  `json:"original_name"`
	ExpectedSize      int64   `json:"expected_size"`
	DeclaredMediaType string  `json:"declared_media_type"`
	ClientSHA256      *string `json:"client_sha256"`
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
	Assets() assets.Repo
	ListForOrder(context.Context, uuid.UUID, uuid.UUID, types.Date) ([]Resource, error)
}
type Service struct {
	uow     write.UnitOfWork[Repo]
	reader  Reader
	cursors paging.Codec
	clock   clock.Clock
	assets  *assets.Service
}

func NewService(uow write.UnitOfWork[Repo], reader Reader, cursors paging.Codec, clk clock.Clock, assetSvc *assets.Service) *Service {
	return &Service{uow: uow, reader: reader, cursors: cursors, clock: clk, assets: assetSvc}
}
func (p Patch) fields() []string {
	f := []string{}
	if p.AssetID != nil {
		f = append(f, "asset_id")
	}
	if p.TakenAtLocalSet {
		f = append(f, "taken_at_local")
	}
	if p.RecordedOn != nil {
		f = append(f, "recorded_on")
	}
	if p.Caption != nil {
		f = append(f, "caption")
	}
	if p.SortOrder != nil {
		f = append(f, "sort_order")
	}
	if p.PlaceName != nil {
		f = append(f, "place_name")
	}
	if p.Address != nil {
		f = append(f, "address")
	}
	if p.LatitudeSet {
		f = append(f, "latitude")
	}
	if p.LongitudeSet {
		f = append(f, "longitude")
	}
	if p.TakenAtLocalSet && p.TakenAtLocal != nil && p.RecordedOn == nil {
		f = append(f, "recorded_on")
	}
	return f
}
func (p Patch) apply(v *Resource) {
	if p.AssetID != nil {
		v.AssetID = *p.AssetID
	}
	if p.TakenAtLocalSet {
		v.TakenAtLocal = p.TakenAtLocal
	}
	if p.RecordedOn != nil {
		v.RecordedOn = *p.RecordedOn
	}
	if p.Caption != nil {
		v.Caption = *p.Caption
	}
	if p.SortOrder != nil {
		v.SortOrder = *p.SortOrder
	}
	if p.PlaceName != nil {
		v.PlaceName = *p.PlaceName
	}
	if p.Address != nil {
		v.Address = *p.Address
	}
	if p.LatitudeSet {
		v.Latitude = photoCoordinate(p.Latitude)
	}
	if p.LongitudeSet {
		v.Longitude = photoCoordinate(p.Longitude)
	}
	if p.TakenAtLocalSet && p.TakenAtLocal != nil && p.RecordedOn == nil {
		v.RecordedOn = p.TakenAtLocal.Date()
	}
}
func photoCoordinate(value *float64) *float64 {
	if value == nil {
		return nil
	}
	// Match the existing PostgreSQL adapter's six-place decimal serialization.
	n, _ := strconv.ParseFloat(strconv.FormatFloat(*value, 'f', 6, 64), 64)
	if n == 0 {
		n = 0
	}
	return &n
}

func (p Patch) validate() error {
	if p.AssetID != nil && *p.AssetID == uuid.Nil {
		return apperr.Validation(apperr.Field("asset_id", "INVALID", "UUID 无效"))
	}
	if err := content.Local("taken_at_local", p.TakenAtLocal); err != nil {
		return err
	}
	if p.RecordedOn != nil {
		if err := content.Date("recorded_on", *p.RecordedOn); err != nil {
			return err
		}
	}
	if err := content.Text("caption", p.Caption, 0, 2000); err != nil {
		return err
	}
	if err := content.Text("place_name", p.PlaceName, 0, 200); err != nil {
		return err
	}
	if err := content.Text("address", p.Address, 0, 500); err != nil {
		return err
	}
	if p.SortOrder != nil && *p.SortOrder < 0 {
		return apperr.Validation(apperr.Field("sort_order", "INVALID", "顺序不能为负数"))
	}
	if p.Latitude != nil && (math.IsNaN(*p.Latitude) || math.IsInf(*p.Latitude, 0) || math.Abs(*p.Latitude) > 90) {
		return apperr.Validation(apperr.Field("latitude", "INVALID", "坐标超出范围"))
	}
	if p.Longitude != nil && (math.IsNaN(*p.Longitude) || math.IsInf(*p.Longitude, 0) || math.Abs(*p.Longitude) > 180) {
		return apperr.Validation(apperr.Field("longitude", "INVALID", "坐标超出范围"))
	}
	return nil
}
func validateResource(v Resource) error {
	if (v.Latitude == nil) != (v.Longitude == nil) {
		return apperr.Validation(apperr.Field("latitude", "INVALID", "经纬度必须成对"))
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
	for _, v := range []string{f.DateFrom, f.DateTo} {
		if v != "" {
			if err := content.Date("date", types.Date(v)); err != nil {
				return empty, err
			}
		}
	}
	if f.DateFrom != "" && f.DateTo != "" && f.DateFrom > f.DateTo {
		return empty, apperr.Validation(apperr.Field("date_to", "INVALID", "日期区间无效"))
	}
	scope := fmt.Sprintf("photos|trip=%s|kind=%s|reservation=%v|from=%s|to=%s", tripID, f.Kind, f.ReservationID, f.DateFrom, f.DateTo)
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
		p.Day = last.RecordedOn
		p.Sort = last.SortOrder
		p.Time = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		if last.TakenAtLocal != nil {
			p.Time = last.TakenAtLocal.Time()
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
func (s *Service) Create(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID, cmd CreateCommand) (WriteResult, error) {
	empty := WriteResult{}
	if cmd.ID == uuid.Nil {
		return empty, apperr.Validation(apperr.Field("id", "REQUIRED", "ID 必填"))
	}
	if err := cmd.Patch.validate(); err != nil {
		return empty, err
	}
	if (cmd.AssetID == nil) == (cmd.Asset == nil) {
		return empty, apperr.Validation(apperr.Field("asset_id", "INVALID", "asset_id 和 asset 必须二选一"))
	}
	var assetInput assets.CreateInput
	if cmd.Asset != nil {
		assetInput = assets.CreateInput{ID: uuid.NewSHA1(cmd.ID, []byte("tripfolio.photo.asset")), Scope: assets.ScopeTrip, TripID: &tripID, OriginalName: cmd.Asset.OriginalName, ExpectedSize: cmd.Asset.ExpectedSize, DeclaredMediaType: cmd.Asset.DeclaredMediaType, ClientSHA256: cmd.Asset.ClientSHA256}
		if err := s.assets.ValidatePhotoCreate(assetInput); err != nil {
			return empty, err
		}
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
		v.RecordedOn, err = types.TodayIn(now, info.Timezone)
		if err != nil {
			return err
		}
		cmd.Patch.apply(&v)
		if err := validateResource(v); err != nil {
			return err
		}
		if cmd.Asset != nil {
			asset, err := s.assets.CreateInTransaction(ctx, scope, repo.Assets(), a.AccountID, assetInput)
			if err != nil {
				return err
			}
			v.AssetID = asset.ID
		}
		if err := content.CheckAsset(ctx, repo, a.AccountID, tripID, v.AssetID, true); err != nil {
			return err
		}
		created, err := repo.Insert(ctx, a.AccountID, v)
		if err != nil {
			return err
		}
		record(scope, created, write.ChangeUpsert, Fields)
		return nil
	}, s.reload(a, tripID, cmd.ID))
	if err != nil {
		return empty, err
	}
	out := WriteResult{Result: result}
	if cmd.Asset != nil {
		out.UploadAuthorization, err = s.assets.CurrentUploadAuthorization(ctx, a, assetInput.ID)
	}
	return out, err
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
		if err := content.CheckAsset(ctx, repo, a.AccountID, tripID, v.AssetID, true); err != nil {
			return err
		}
		if reflect.DeepEqual(before, v) {
			scope.SetPrimary(write.Ref(EntityType, v.ID, int64(v.Version)))
			return nil
		}
		fields = actualChangedFields(before, v)
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

func actualChangedFields(before, after Resource) []string {
	out := []string{}
	values := []struct {
		name          string
		before, after any
	}{
		{"asset_id", before.AssetID, after.AssetID},
		{"taken_at_local", before.TakenAtLocal, after.TakenAtLocal},
		{"recorded_on", before.RecordedOn, after.RecordedOn},
		{"caption", before.Caption, after.Caption},
		{"sort_order", before.SortOrder, after.SortOrder},
		{"place_name", before.PlaceName, after.PlaceName},
		{"address", before.Address, after.Address},
		{"latitude", before.Latitude, after.Latitude},
		{"longitude", before.Longitude, after.Longitude},
	}
	for _, field := range values {
		if !reflect.DeepEqual(field.before, field.after) {
			out = append(out, field.name)
		}
	}
	return out
}
