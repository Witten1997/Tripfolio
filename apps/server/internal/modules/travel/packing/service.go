package packing

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/write"
)

// Service 实现行李清单用例（接口设计 2.4、3.4）。
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

const (
	maxNameChars  = 120
	maxNotesChars = 2000
	minQuantity   = 1
	maxQuantity   = 9999
	maxBatch      = 100
)

// 错误代码（接口设计 1.4）。
const (
	codeTripDeleted   = "TRIP_DELETED"
	codeResourceGone  = "RESOURCE_GONE"
	codeIDAlreadyUsed = "ID_ALREADY_USED"
)

func tripDeleted() *apperr.Error  { return apperr.Gone(codeTripDeleted, "旅行已在回收站中") }
func resourceGone() *apperr.Error { return apperr.Gone(codeResourceGone, "物品已删除") }

type tripReader interface {
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
}

func loadTrip(ctx context.Context, r tripReader, accountID, tripID uuid.UUID) error {
	info, found, err := r.Trip(ctx, accountID, tripID)
	if err != nil {
		return apperr.Internal(err)
	}
	if !found {
		return apperr.NotFound()
	}
	if info.DeletedAt != nil {
		return tripDeleted()
	}
	return nil
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

func addField(fields *[]apperr.FieldError, e *apperr.FieldError) {
	if e != nil {
		*fields = append(*fields, *e)
	}
}

func validateName(name string) (string, *apperr.FieldError) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameChars {
		e := apperr.Field("name", "INVALID", fmt.Sprintf("名称须为 1–%d 个字符", maxNameChars))
		return "", &e
	}
	return name, nil
}

func validateQuantity(field string, q int32) *apperr.FieldError {
	if q < minQuantity || q > maxQuantity {
		e := apperr.Field(field, "INVALID", fmt.Sprintf("数量须在 %d–%d 之间", minQuantity, maxQuantity))
		return &e
	}
	return nil
}

func validateNotes(field, value string) *apperr.FieldError {
	if utf8.RuneCountInString(value) > maxNotesChars {
		e := apperr.Field(field, "TOO_LONG", fmt.Sprintf("最多 %d 个字符", maxNotesChars))
		return &e
	}
	if !utf8.ValidString(value) {
		e := apperr.Field(field, "INVALID", "必须是有效的 UTF-8 文本")
		return &e
	}
	return nil
}

// Library 返回内置物品库。
func (s *Service) Library() Library { return BuiltinLibrary() }

// Get 返回有效物品；不存在或非本人 404，已删除 410。
func (s *Service) Get(ctx context.Context, a actor.Actor, tripID, id uuid.UUID) (Resource, error) {
	if err := loadTrip(ctx, s.reader, a.AccountID, tripID); err != nil {
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

func listScope(tripID uuid.UUID, category Category, status Status) string {
	return fmt.Sprintf("packing|trip=%s|category=%s|status=%s", tripID, category, status)
}

// List 返回一页有效物品（接口设计 3.9 PackingFilters）。
func (s *Service) List(ctx context.Context, a actor.Actor, tripID uuid.UUID, f Filters) (paging.Page[Resource], error) {
	if err := loadTrip(ctx, s.reader, a.AccountID, tripID); err != nil {
		return paging.Page[Resource]{}, err
	}
	var fields []apperr.FieldError
	var category Category
	if f.Category != "" {
		category = Category(f.Category)
		if !category.Valid() {
			fields = append(fields, apperr.Field("category", "INVALID", "分类不合法"))
		}
	}
	var status Status
	if f.Status != "" {
		status = Status(f.Status)
		if !status.Valid() {
			fields = append(fields, apperr.Field("status", "INVALID", "状态须为 pending、ready 或 packed"))
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
	scope := listScope(tripID, category, status)
	var after *Position
	if f.Cursor != "" {
		var p Position
		if err := s.cursors.Decode(a.AccountID, scope, f.Cursor, &p); err != nil {
			return paging.Page[Resource]{}, paging.InvalidCursor()
		}
		after = &p
	}
	items, err := s.reader.List(ctx, a.AccountID, tripID, ListQuery{Category: category, Status: status, Limit: limit + 1, After: after})
	if err != nil {
		return paging.Page[Resource]{}, apperr.Internal(err)
	}
	page := paging.Page[Resource]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := items[limit-1]
		token, err := s.cursors.Encode(a.AccountID, scope, Position{Category: last.Category, CreatedAt: last.CreatedAt, ID: last.ID})
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

// CreateCommand 是创建命令（接口设计 3.4 PackingCreate）；Quantity、Status 为 nil 时用默认值。
type CreateCommand struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Category Category  `json:"category"`
	Quantity *int32    `json:"quantity"`
	Notes    *string   `json:"notes"`
	Status   *string   `json:"status"`
}

// Create 创建独立物品。
func (s *Service) Create(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID, cmd CreateCommand) (write.Result, error) {
	var fields []apperr.FieldError
	if cmd.ID == uuid.Nil {
		fields = append(fields, apperr.Field("id", "INVALID", "id 必填"))
	}
	name, ferr := validateName(cmd.Name)
	addField(&fields, ferr)
	cmd.Name = name
	if !cmd.Category.Valid() {
		fields = append(fields, apperr.Field("category", "INVALID", "分类不合法"))
	}
	quantity := int32(1)
	if cmd.Quantity != nil {
		quantity = *cmd.Quantity
		addField(&fields, validateQuantity("quantity", quantity))
	}
	notes := ""
	if cmd.Notes != nil {
		notes = *cmd.Notes
		addField(&fields, validateNotes("notes", notes))
	}
	status := StatusPending
	if cmd.Status != nil {
		status = Status(*cmd.Status)
		if !status.Valid() {
			fields = append(fields, apperr.Field("status", "INVALID", "状态须为 pending、ready 或 packed"))
		}
	}
	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}
	fp := struct {
		Name     string   `json:"name"`
		Category Category `json:"category"`
		Quantity int32    `json:"quantity"`
		Notes    string   `json:"notes"`
		Status   Status   `json:"status"`
	}{cmd.Name, cmd.Category, quantity, notes, status}
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "packing.create",
		Fingerprint: write.Fingerprint("packing.create", cmd.ID.String(), nil, fp),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if err := loadTrip(ctx, repo, a.AccountID, tripID); err != nil {
			return err
		}
		exists, err := repo.IDExists(ctx, cmd.ID)
		if err != nil {
			return err
		}
		if exists {
			return apperr.Conflicted(codeIDAlreadyUsed, "该 ID 已被使用")
		}
		taken, err := repo.NameTaken(ctx, a.AccountID, tripID, cmd.Category, cmd.Name, uuid.Nil)
		if err != nil {
			return err
		}
		if taken {
			return apperr.Validation(apperr.Field("name", "DUPLICATE", "该分类下已存在同名物品"))
		}
		now := s.clock.Now()
		created, err := repo.Insert(ctx, a.AccountID, Resource{
			ID: cmd.ID, TripID: tripID, Name: cmd.Name, Category: cmd.Category, Quantity: quantity,
			Notes: notes, Status: status, Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		record(scope, created, write.ChangeUpsert, Fields)
		scope.SetPrimary(ref(created))
		return nil
	}, s.reload(a, tripID, cmd.ID))
}

// Patch 是局部更新（接口设计 3.4 PackingPatch）；nil 表示缺省。
type Patch struct {
	Name     *string   `json:"name"`
	Category *Category `json:"category"`
	Quantity *int32    `json:"quantity"`
	Notes    *string   `json:"notes"`
	Status   *Status   `json:"status"`
}

func (p Patch) submittedFields() []string {
	var f []string
	if p.Name != nil {
		f = append(f, "name")
	}
	if p.Category != nil {
		f = append(f, "category")
	}
	if p.Quantity != nil {
		f = append(f, "quantity")
	}
	if p.Notes != nil {
		f = append(f, "notes")
	}
	if p.Status != nil {
		f = append(f, "status")
	}
	return f
}

// Update 局部更新物品，按字段级合并规则处理基线版本（接口设计 1.2、3.4）。
func (s *Service) Update(ctx context.Context, a actor.Actor, operationID, tripID, id uuid.UUID, baseVersion int64, patch Patch) (write.Result, error) {
	var fields []apperr.FieldError
	if patch.Name != nil {
		name, ferr := validateName(*patch.Name)
		addField(&fields, ferr)
		patch.Name = &name
	}
	if patch.Category != nil && !patch.Category.Valid() {
		fields = append(fields, apperr.Field("category", "INVALID", "分类不合法"))
	}
	if patch.Quantity != nil {
		addField(&fields, validateQuantity("quantity", *patch.Quantity))
	}
	if patch.Notes != nil {
		addField(&fields, validateNotes("notes", *patch.Notes))
	}
	if patch.Status != nil && !patch.Status.Valid() {
		fields = append(fields, apperr.Field("status", "INVALID", "状态须为 pending、ready 或 packed"))
	}
	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}
	submitted := patch.submittedFields()
	if len(submitted) == 0 {
		return write.Result{}, apperr.Validation(apperr.Field("", "EMPTY_PATCH", "没有可更新的字段"))
	}
	base := baseVersion
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "packing.update",
		Fingerprint: write.Fingerprint("packing.update", id.String(), &base, patch),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if err := loadTrip(ctx, repo, a.AccountID, tripID); err != nil {
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
		if patch.Name != nil {
			v.Name = *patch.Name
		}
		if patch.Category != nil {
			v.Category = *patch.Category
		}
		if patch.Quantity != nil {
			v.Quantity = *patch.Quantity
		}
		if patch.Notes != nil {
			v.Notes = *patch.Notes
		}
		if patch.Status != nil {
			v.Status = *patch.Status
		}
		if v.Name != current.Name || v.Category != current.Category {
			taken, err := repo.NameTaken(ctx, a.AccountID, tripID, v.Category, v.Name, id)
			if err != nil {
				return err
			}
			if taken {
				return apperr.Validation(apperr.Field("name", "DUPLICATE", "该分类下已存在同名物品"))
			}
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

// Delete 软删除物品；要求版本相等。
func (s *Service) Delete(ctx context.Context, a actor.Actor, operationID, tripID, id uuid.UUID, version int64) (write.Result, error) {
	base := version
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "packing.delete",
		Fingerprint: write.Fingerprint("packing.delete", id.String(), &base, struct{}{}),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if err := loadTrip(ctx, repo, a.AccountID, tripID); err != nil {
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
		if int64(current.Version) != version {
			return apperr.VersionConflict(&apperr.Conflict{
				EntityType: EntityType, EntityID: id, ExpectedVersion: version, CurrentVersion: int64(current.Version), Current: current,
			})
		}
		deleted, err := repo.SoftDelete(ctx, a.AccountID, tripID, id, s.clock.Now())
		if err != nil {
			return err
		}
		record(scope, deleted, write.ChangeDelete, nil)
		scope.SetPrimary(ref(deleted))
		return nil
	}, s.reload(a, tripID, id))
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
