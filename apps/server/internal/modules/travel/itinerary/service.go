package itinerary

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
)

// Service 实现每日行程用例（接口设计 2.3、3.3）。
type Service struct {
	uow     write.UnitOfWork[Repo]
	reader  Reader
	cursors paging.Codec
	clock   clock.Clock
}

// NewService 创建服务。
func NewService(uow write.UnitOfWork[Repo], reader Reader, cursors paging.Codec, clk clock.Clock) *Service {
	return &Service{uow: uow, reader: reader, cursors: cursors, clock: clk}
}

func ref(r Resource) write.EntityRef { return write.Ref(EntityType, r.ID, int64(r.Version)) }

func record(scope write.Scope, r Resource, kind write.ChangeKind, fields []string) {
	tripID := r.TripID
	change := write.Change{EntityType: EntityType, EntityID: r.ID, TripID: &tripID, Version: int64(r.Version), Kind: kind}
	if kind == write.ChangeUpsert {
		change.Snapshot = r
		change.ChangedFields = fields
	}
	scope.Record(change)
}

// Get 返回有效行程项目；不存在或非本人 404，已删除 410 RESOURCE_GONE。
func (s *Service) Get(ctx context.Context, a actor.Actor, tripID, id uuid.UUID) (Resource, error) {
	if _, err := loadTrip(ctx, s.reader, a.AccountID, tripID); err != nil {
		return Resource{}, err
	}
	r, found, err := s.reader.Get(ctx, a.AccountID, tripID, id)
	if err != nil {
		return Resource{}, apperr.Internal(err)
	}
	if !found {
		return Resource{}, apperr.NotFound()
	}
	if r.DeletedAt != nil {
		return Resource{}, resourceGone()
	}
	return r, nil
}

func listScope(tripID uuid.UUID, from, to types.Date, status Status) string {
	return fmt.Sprintf("itinerary|trip=%s|from=%s|to=%s|status=%s", tripID, from, to, status)
}

// List 返回一页有效行程项目（接口设计 3.9 ItineraryFilters）。
func (s *Service) List(ctx context.Context, a actor.Actor, tripID uuid.UUID, f Filters) (paging.Page[Resource], error) {
	if _, err := loadTrip(ctx, s.reader, a.AccountID, tripID); err != nil {
		return paging.Page[Resource]{}, err
	}
	var fields []apperr.FieldError
	var from, to types.Date
	if f.DateFrom != "" {
		d, ferr := parseDate("date_from", f.DateFrom)
		addField(&fields, ferr)
		from = d
	}
	if f.DateTo != "" {
		d, ferr := parseDate("date_to", f.DateTo)
		addField(&fields, ferr)
		to = d
	}
	if from != "" && to != "" && to.Before(from) {
		fields = append(fields, apperr.Field("date_to", "DATE_ORDER", "date_to 不能早于 date_from"))
	}
	var status Status
	if f.Status != "" {
		status = Status(f.Status)
		if !status.Valid() {
			fields = append(fields, apperr.Field("status", "INVALID", "状态须为 pending、completed 或 skipped"))
		}
	}
	limit, err := paging.Limit(f.Limit)
	if err != nil {
		e, ok := apperr.As(err)
		if !ok {
			return paging.Page[Resource]{}, err
		}
		fields = append(fields, e.Fields...)
	}
	if len(fields) > 0 {
		return paging.Page[Resource]{}, apperr.Validation(fields...)
	}
	scope := listScope(tripID, from, to, status)
	var after *Position
	if f.Cursor != "" {
		var p Position
		if err := s.cursors.Decode(a.AccountID, scope, f.Cursor, &p); err != nil {
			return paging.Page[Resource]{}, paging.InvalidCursor()
		}
		after = &p
	}
	items, err := s.reader.List(ctx, a.AccountID, tripID, ListQuery{DateFrom: from, DateTo: to, Status: status, Limit: limit + 1, After: after})
	if err != nil {
		return paging.Page[Resource]{}, apperr.Internal(err)
	}
	page := paging.Page[Resource]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := items[limit-1]
		token, err := s.cursors.Encode(a.AccountID, scope, Position{ScheduledOn: last.ScheduledOn, SortOrder: last.SortOrder, ID: last.ID})
		if err != nil {
			return paging.Page[Resource]{}, apperr.Internal(err)
		}
		page.NextCursor = &token
	}
	if page.Items == nil {
		page.Items = []Resource{}
	}
	return page, nil
}

// CreateCommand 是创建命令（接口设计 3.3 ItineraryCreate）；指针字段区分缺省与显式值。
type CreateCommand struct {
	ID                     uuid.UUID `json:"id"`
	Title                  string    `json:"title"`
	Kind                   Kind      `json:"kind"`
	ScheduledOn            string    `json:"scheduled_on"`
	PlannedStartLocal      *string   `json:"planned_start_local"`
	PlannedEndLocal        *string   `json:"planned_end_local"`
	PlannedDurationMinutes *int32    `json:"planned_duration_minutes"`
	PlaceName              *string   `json:"place_name"`
	Address                *string   `json:"address"`
	Latitude               *float64  `json:"latitude"`
	Longitude              *float64  `json:"longitude"`
	EstimatedAmount        *string   `json:"estimated_amount"`
	CurrencyCode           *string   `json:"currency_code"`
	Notes                  *string   `json:"notes"`
	Status                 *string   `json:"status"`
	ActualStartLocal       *string   `json:"actual_start_local"`
	ActualEndLocal         *string   `json:"actual_end_local"`
	ActualNotes            *string   `json:"actual_notes"`
}

// Create 新建行程项目，默认追加到 scheduled_on 当天末尾。
func (s *Service) Create(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID, cmd CreateCommand) (write.Result, error) {
	var fields []apperr.FieldError
	if cmd.ID == uuid.Nil {
		fields = append(fields, apperr.Field("id", "INVALID", "id 必填"))
	}
	title, ferr := validateTitle(cmd.Title)
	addField(&fields, ferr)
	cmd.Title = title
	if !cmd.Kind.Valid() {
		fields = append(fields, apperr.Field("kind", "INVALID", "类型须为 attraction、transport、lodging、dining 或 other"))
	}
	scheduledOn, ferr := parseDate("scheduled_on", cmd.ScheduledOn)
	addField(&fields, ferr)

	plannedStart := parseLocal("planned_start_local", cmd.PlannedStartLocal, &fields)
	plannedEnd := parseLocal("planned_end_local", cmd.PlannedEndLocal, &fields)
	actualStart := parseLocal("actual_start_local", cmd.ActualStartLocal, &fields)
	actualEnd := parseLocal("actual_end_local", cmd.ActualEndLocal, &fields)
	duration := validateDuration(cmd.PlannedDurationMinutes, cmd.PlannedEndLocal != nil, &fields)
	validateTimeOrder("planned_end_local", plannedStart, plannedEnd, &fields)
	validateTimeOrder("actual_end_local", actualStart, actualEnd, &fields)
	cmd.Latitude, cmd.Longitude = validateCoordinates(cmd.Latitude, cmd.Longitude, &fields)
	placeName := optionalText("place_name", cmd.PlaceName, maxPlaceNameChars, &fields)
	address := optionalText("address", cmd.Address, maxAddressChars, &fields)
	notes := optionalText("notes", cmd.Notes, maxNotesChars, &fields)
	actualNotes := optionalText("actual_notes", cmd.ActualNotes, maxNotesChars, &fields)
	status := optionalStatus(cmd.Status, &fields)
	cmd.EstimatedAmount = compactAmount(cmd.EstimatedAmount, cmd.CurrencyCode, &fields)
	validateCurrencyCode(cmd.CurrencyCode, &fields)

	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}

	// 指纹用规范化后的命令：金额已按 money.Compact 化简，坐标已四舍五入到 6 位小数。
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "itinerary.create",
		Fingerprint: write.Fingerprint("itinerary.create", cmd.ID.String(), nil, cmd),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		info, err := loadTrip(ctx, repo, a.AccountID, tripID)
		if err != nil {
			return err
		}
		exists, err := repo.IDExists(ctx, cmd.ID)
		if err != nil {
			return err
		}
		if exists {
			return apperr.Conflicted(codeIDAlreadyUsed, "该 ID 已被使用")
		}
		amount, err := canonicalAmount(cmd.EstimatedAmount, cmd.CurrencyCode, info.CurrencyCode)
		if err != nil {
			return err
		}
		sortOrder := int32(0)
		if max, found, err := repo.MaxSortOrder(ctx, a.AccountID, tripID, scheduledOn); err != nil {
			return err
		} else if found {
			sortOrder = max + 1
		}
		now := s.clock.Now()
		created, err := repo.Insert(ctx, a.AccountID, Resource{
			ID: cmd.ID, TripID: tripID, Title: cmd.Title, Kind: cmd.Kind, ScheduledOn: scheduledOn, SortOrder: sortOrder,
			PlannedStartLocal: plannedStart, PlannedEndLocal: plannedEnd, PlannedDurationMinutes: duration,
			PlaceName: placeName, Address: address, Latitude: cmd.Latitude, Longitude: cmd.Longitude,
			EstimatedAmount: amount, Notes: notes, Status: status,
			ActualStartLocal: actualStart, ActualEndLocal: actualEnd, ActualNotes: actualNotes,
			Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if outsideTripDates(scheduledOn, info) {
			scope.Warn(write.WarnItineraryOutsideTripDates)
		}
		record(scope, created, write.ChangeUpsert, CreateFields)
		scope.SetPrimary(ref(created))
		return nil
	}, s.reload(a, tripID, cmd.ID))
}

// Patch 是局部更新（接口设计 3.3 ItineraryPatch）；*Set 字段区分“显式清空”与缺省。
type Patch struct {
	Title                  *string  `json:"title"`
	Kind                   *Kind    `json:"kind"`
	PlannedStartSet        bool     `json:"planned_start_set"`
	PlannedStartLocal      *string  `json:"planned_start_local"`
	PlannedEndSet          bool     `json:"planned_end_set"`
	PlannedEndLocal        *string  `json:"planned_end_local"`
	PlannedDurationSet     bool     `json:"planned_duration_set"`
	PlannedDurationMinutes *int32   `json:"planned_duration_minutes"`
	PlaceName              *string  `json:"place_name"`
	Address                *string  `json:"address"`
	LatitudeSet            bool     `json:"latitude_set"`
	Latitude               *float64 `json:"latitude"`
	LongitudeSet           bool     `json:"longitude_set"`
	Longitude              *float64 `json:"longitude"`
	EstimatedAmountSet     bool     `json:"estimated_amount_set"`
	EstimatedAmount        *string  `json:"estimated_amount"`
	CurrencyCode           *string  `json:"currency_code"`
	Notes                  *string  `json:"notes"`
	Status                 *Status  `json:"status"`
	ActualStartSet         bool     `json:"actual_start_set"`
	ActualStartLocal       *string  `json:"actual_start_local"`
	ActualEndSet           bool     `json:"actual_end_set"`
	ActualEndLocal         *string  `json:"actual_end_local"`
	ActualNotes            *string  `json:"actual_notes"`
}

func (p Patch) submittedFields() []string {
	var f []string
	add := func(cond bool, name string) {
		if cond {
			f = append(f, name)
		}
	}
	add(p.Title != nil, "title")
	add(p.Kind != nil, "kind")
	add(p.PlannedStartSet, "planned_start_local")
	add(p.PlannedEndSet, "planned_end_local")
	add(p.PlannedDurationSet, "planned_duration_minutes")
	add(p.PlaceName != nil, "place_name")
	add(p.Address != nil, "address")
	add(p.LatitudeSet, "latitude")
	add(p.LongitudeSet, "longitude")
	add(p.EstimatedAmountSet, "estimated_amount")
	add(p.Notes != nil, "notes")
	add(p.Status != nil, "status")
	add(p.ActualStartSet, "actual_start_local")
	add(p.ActualEndSet, "actual_end_local")
	add(p.ActualNotes != nil, "actual_notes")
	return f
}

// Update 局部更新行程项目，按字段级合并规则处理基线版本（接口设计 1.2、3.3）。
func (s *Service) Update(ctx context.Context, a actor.Actor, operationID, tripID, id uuid.UUID, baseVersion int64, patch Patch) (write.Result, error) {
	var fields []apperr.FieldError
	if patch.Title != nil {
		title, ferr := validateTitle(*patch.Title)
		addField(&fields, ferr)
		patch.Title = &title
	}
	if patch.Kind != nil && !patch.Kind.Valid() {
		fields = append(fields, apperr.Field("kind", "INVALID", "类型须为 attraction、transport、lodging、dining 或 other"))
	}
	plannedStart := parseLocal("planned_start_local", patch.PlannedStartLocal, &fields)
	plannedEnd := parseLocal("planned_end_local", patch.PlannedEndLocal, &fields)
	actualStart := parseLocal("actual_start_local", patch.ActualStartLocal, &fields)
	actualEnd := parseLocal("actual_end_local", patch.ActualEndLocal, &fields)
	var duration *int32
	if patch.PlannedDurationSet {
		duration = validateDuration(patch.PlannedDurationMinutes, false, &fields)
	}
	if patch.PlaceName != nil {
		addField(&fields, validateText("place_name", *patch.PlaceName, maxPlaceNameChars))
	}
	if patch.Address != nil {
		addField(&fields, validateText("address", *patch.Address, maxAddressChars))
	}
	if patch.Notes != nil {
		addField(&fields, validateText("notes", *patch.Notes, maxNotesChars))
	}
	if patch.ActualNotes != nil {
		addField(&fields, validateText("actual_notes", *patch.ActualNotes, maxNotesChars))
	}
	if patch.Status != nil && !patch.Status.Valid() {
		fields = append(fields, apperr.Field("status", "INVALID", "状态须为 pending、completed 或 skipped"))
	}
	patch.Latitude, patch.Longitude = validatePatchCoordinates(patch, &fields)
	if patch.EstimatedAmountSet {
		patch.EstimatedAmount = compactAmount(patch.EstimatedAmount, patch.CurrencyCode, &fields)
	}
	validateCurrencyCode(patch.CurrencyCode, &fields)
	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}
	submitted := patch.submittedFields()
	if len(submitted) == 0 {
		return write.Result{}, apperr.Validation(apperr.Field("", "EMPTY_PATCH", "没有可更新的字段"))
	}
	base := baseVersion
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "itinerary.update",
		Fingerprint: write.Fingerprint("itinerary.update", id.String(), &base, patch),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		info, err := loadTrip(ctx, repo, a.AccountID, tripID)
		if err != nil {
			return err
		}
		current, found, err := repo.GetForUpdate(ctx, a.AccountID, tripID, id)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFound()
		}
		if current.DeletedAt != nil {
			return resourceGone()
		}
		decision, err := write.ResolvePatch(ctx, repo.MergeSource(), a.AccountID, EntityType, id, baseVersion, int64(current.Version), submitted)
		if err != nil {
			return err
		}
		if !decision.Merge {
			return apperr.VersionConflict(&apperr.Conflict{
				EntityType: EntityType, EntityID: id, ExpectedVersion: baseVersion,
				CurrentVersion: int64(current.Version), ConflictingFields: decision.Conflicting, Current: current,
			})
		}
		if decision.Merged {
			scope.Warn(write.WarnMergedWithNewerVersion)
		}
		v := current.Values()
		if patch.Title != nil {
			v.Title = *patch.Title
		}
		if patch.Kind != nil {
			v.Kind = *patch.Kind
		}
		if patch.PlannedStartSet {
			v.PlannedStartLocal = plannedStart
		}
		if patch.PlannedEndSet {
			v.PlannedEndLocal = plannedEnd
		}
		if patch.PlannedDurationSet {
			v.PlannedDurationMinutes = duration
		}
		if patch.PlaceName != nil {
			v.PlaceName = *patch.PlaceName
		}
		if patch.Address != nil {
			v.Address = *patch.Address
		}
		if patch.LatitudeSet {
			v.Latitude = patch.Latitude
		}
		if patch.LongitudeSet {
			v.Longitude = patch.Longitude
		}
		if patch.Notes != nil {
			v.Notes = *patch.Notes
		}
		if patch.Status != nil {
			v.Status = *patch.Status
		}
		if patch.ActualStartSet {
			v.ActualStartLocal = actualStart
		}
		if patch.ActualEndSet {
			v.ActualEndLocal = actualEnd
		}
		if patch.ActualNotes != nil {
			v.ActualNotes = *patch.ActualNotes
		}
		if patch.EstimatedAmountSet {
			amount, err := canonicalAmount(patch.EstimatedAmount, patch.CurrencyCode, info.CurrencyCode)
			if err != nil {
				return err
			}
			v.EstimatedAmount = amount
		}
		if err := validateMergedValues(v); err != nil {
			return err
		}
		updated, err := repo.Update(ctx, a.AccountID, tripID, id, v, s.clock.Now())
		if err != nil {
			return err
		}
		record(scope, updated, write.ChangeUpsert, submitted)
		scope.SetPrimary(ref(updated))
		return nil
	}, s.reload(a, tripID, id))
}

// Delete 软删除行程项目，不影响其他记录；要求版本相等。
func (s *Service) Delete(ctx context.Context, a actor.Actor, operationID, tripID, id uuid.UUID, version int64) (write.Result, error) {
	base := version
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "itinerary.delete",
		Fingerprint: write.Fingerprint("itinerary.delete", id.String(), &base, struct{}{}),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if _, err := loadTrip(ctx, repo, a.AccountID, tripID); err != nil {
			return err
		}
		current, err := lockActive(ctx, repo, a.AccountID, tripID, id, version)
		if err != nil {
			return err
		}
		deleted, err := repo.SoftDelete(ctx, a.AccountID, tripID, current.ID, s.clock.Now())
		if err != nil {
			return err
		}
		record(scope, deleted, write.ChangeDelete, nil)
		scope.SetPrimary(ref(deleted))
		return nil
	}, s.reload(a, tripID, id))
}

// lockActive 读取并锁定有效项目，统一处理 404、410 与版本相等校验。
func lockActive(ctx context.Context, repo Repo, accountID, tripID, id uuid.UUID, version int64) (Resource, error) {
	current, found, err := repo.GetForUpdate(ctx, accountID, tripID, id)
	if err != nil {
		return Resource{}, err
	}
	if !found {
		return Resource{}, apperr.NotFound()
	}
	if current.DeletedAt != nil {
		return Resource{}, resourceGone()
	}
	if int64(current.Version) != version {
		return Resource{}, apperr.VersionConflict(&apperr.Conflict{
			EntityType: EntityType, EntityID: id, ExpectedVersion: version, CurrentVersion: int64(current.Version), Current: current,
		})
	}
	return current, nil
}

func (s *Service) reload(a actor.Actor, tripID, id uuid.UUID) func(ctx context.Context, repo Repo) (any, error) {
	return func(ctx context.Context, repo Repo) (any, error) {
		r, found, err := repo.Get(ctx, a.AccountID, tripID, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, apperr.NotFound()
		}
		return r, nil
	}
}
