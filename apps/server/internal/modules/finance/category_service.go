package finance

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
)

// CategoryService 管理账号级账单分类（接口设计 2.5、3.5）。
type CategoryService struct {
	uow    write.UnitOfWork[CategoryRepo]
	reader CategoryReader
	clock  clock.Clock
}

// NewCategoryService 创建服务。
func NewCategoryService(uow write.UnitOfWork[CategoryRepo], reader CategoryReader, clk clock.Clock) *CategoryService {
	return &CategoryService{uow: uow, reader: reader, clock: clk}
}

// List 返回本账号全部有效分类，按 sort_order、id 排序。
func (s *CategoryService) List(ctx context.Context, a actor.Actor) ([]CategoryResource, error) {
	items, err := s.reader.List(ctx, a.AccountID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if items == nil {
		items = []CategoryResource{}
	}
	return items, nil
}

// Get 返回单个分类；不存在或非本人返回 404，已删除返回 410。
func (s *CategoryService) Get(ctx context.Context, a actor.Actor, id uuid.UUID) (CategoryResource, error) {
	c, found, err := s.reader.Get(ctx, a.AccountID, id)
	if err != nil {
		return CategoryResource{}, apperr.Internal(err)
	}
	if !found {
		return CategoryResource{}, apperr.NotFound()
	}
	if c.DeletedAt != nil {
		return CategoryResource{}, apperr.Gone("RESOURCE_GONE", "")
	}
	return c, nil
}

// CreateCategoryCommand 是创建命令；SortOrder 为 nil 表示追加到末尾。
type CreateCategoryCommand struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Icon      *string   `json:"icon"`
	SortOrder *int32    `json:"sort_order"`
}

// CategoryPatch 是局部更新；nil 表示缺省。IconSet 区分“清空图标”与缺省。
type CategoryPatch struct {
	Name      *string `json:"name"`
	IconSet   bool    `json:"icon_set"`
	Icon      *string `json:"icon"`
	SortOrder *int32  `json:"sort_order"`
}

func (p CategoryPatch) fields() []string {
	var f []string
	if p.Name != nil {
		f = append(f, "name")
	}
	if p.IconSet {
		f = append(f, "icon")
	}
	if p.SortOrder != nil {
		f = append(f, "sort_order")
	}
	return f
}

func validateCategoryName(name string) (string, *apperr.FieldError) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 40 {
		e := apperr.Field("name", "INVALID", "分类名称须为 1–40 个字符")
		return "", &e
	}
	return name, nil
}

func validateIcon(icon *string) *apperr.FieldError {
	if icon == nil {
		return nil
	}
	if _, ok := CategoryIcons[*icon]; !ok {
		e := apperr.Field("icon", "INVALID", "不是可用的图标键")
		return &e
	}
	return nil
}

// Create 新建分类。
func (s *CategoryService) Create(ctx context.Context, a actor.Actor, operationID uuid.UUID, cmd CreateCategoryCommand) (write.Result, error) {
	var fields []apperr.FieldError
	name, ferr := validateCategoryName(cmd.Name)
	if ferr != nil {
		fields = append(fields, *ferr)
	}
	if ferr := validateIcon(cmd.Icon); ferr != nil {
		fields = append(fields, *ferr)
	}
	if cmd.SortOrder != nil && *cmd.SortOrder < 0 {
		fields = append(fields, apperr.Field("sort_order", "INVALID", "排序值不能为负"))
	}
	if cmd.ID == uuid.Nil {
		fields = append(fields, apperr.Field("id", "INVALID", "id 必填"))
	}
	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}
	cmd.Name = name
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "expense_category.create",
		Fingerprint: write.Fingerprint("expense_category.create", cmd.ID.String(), nil, cmd),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo CategoryRepo) error {
		if exists, err := repo.IDExists(ctx, cmd.ID); err != nil {
			return err
		} else if exists {
			return apperr.Conflicted("ID_ALREADY_USED", "该 ID 已被使用")
		}
		sortOrder := int32(0)
		if cmd.SortOrder != nil {
			sortOrder = *cmd.SortOrder
		} else {
			max, err := repo.MaxSortOrder(ctx, a.AccountID)
			if err != nil {
				return err
			}
			sortOrder = max + 1
		}
		now := s.clock.Now()
		created, err := repo.Insert(ctx, a.AccountID, CategoryResource{
			ID: cmd.ID, Name: cmd.Name, Icon: cmd.Icon, SortOrder: sortOrder, IsPreset: false,
			Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		if errors.Is(err, ErrDuplicateName) {
			return apperr.Validation(apperr.Field("name", "DUPLICATE", "已存在同名分类"))
		}
		if err != nil {
			return err
		}
		recordCategory(scope, created, write.ChangeUpsert, CategoryFields)
		scope.SetPrimary(write.Ref(EntityTypeCategory, created.ID, int64(created.Version)))
		return nil
	}, s.reload(a, cmd.ID))
}

// reload 在写事务提交前重读主资源，作为 WriteResult.data（接口设计 1.2：删除也返回删除后的资源）。
func (s *CategoryService) reload(a actor.Actor, id uuid.UUID) func(ctx context.Context, repo CategoryRepo) (any, error) {
	return func(ctx context.Context, repo CategoryRepo) (any, error) {
		c, found, err := repo.Get(ctx, a.AccountID, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, apperr.NotFound()
		}
		return c, nil
	}
}

// Update 局部更新分类，按字段级合并规则处理基线版本。
func (s *CategoryService) Update(ctx context.Context, a actor.Actor, operationID uuid.UUID, id uuid.UUID, baseVersion int64, patch CategoryPatch) (write.Result, error) {
	var fields []apperr.FieldError
	if patch.Name != nil {
		name, ferr := validateCategoryName(*patch.Name)
		if ferr != nil {
			fields = append(fields, *ferr)
		} else {
			patch.Name = &name
		}
	}
	if patch.IconSet {
		if ferr := validateIcon(patch.Icon); ferr != nil {
			fields = append(fields, *ferr)
		}
	}
	if patch.SortOrder != nil && *patch.SortOrder < 0 {
		fields = append(fields, apperr.Field("sort_order", "INVALID", "排序值不能为负"))
	}
	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}
	submitted := patch.fields()
	if len(submitted) == 0 {
		return write.Result{}, apperr.Validation(apperr.Field("", "EMPTY_PATCH", "没有可更新的字段"))
	}
	base := baseVersion
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "expense_category.update",
		Fingerprint: write.Fingerprint("expense_category.update", id.String(), &base, patch),
	}
	reload := s.reload(a, id)
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo CategoryRepo) error {
		current, found, err := repo.GetForUpdate(ctx, a.AccountID, id)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFound()
		}
		if current.DeletedAt != nil {
			return apperr.Gone("RESOURCE_GONE", "")
		}
		decision, err := write.ResolvePatch(ctx, repo.MergeSource(), a.AccountID, EntityTypeCategory, id, baseVersion, int64(current.Version), submitted)
		if err != nil {
			return err
		}
		if !decision.Merge {
			return apperr.VersionConflict(&apperr.Conflict{
				EntityType: EntityTypeCategory, EntityID: id, ExpectedVersion: baseVersion,
				CurrentVersion: int64(current.Version), ConflictingFields: decision.Conflicting, Current: current,
			})
		}
		if decision.Merged {
			scope.Warn(write.WarnMergedWithNewerVersion)
		}
		name, icon, sortOrder := current.Name, current.Icon, current.SortOrder
		if patch.Name != nil {
			name = *patch.Name
		}
		if patch.IconSet {
			icon = patch.Icon
		}
		if patch.SortOrder != nil {
			sortOrder = *patch.SortOrder
		}
		updated, err := repo.Update(ctx, a.AccountID, id, name, icon, sortOrder, s.clock.Now())
		if errors.Is(err, ErrDuplicateName) {
			return apperr.Validation(apperr.Field("name", "DUPLICATE", "已存在同名分类"))
		}
		if err != nil {
			return err
		}
		recordCategory(scope, updated, write.ChangeUpsert, submitted)
		scope.SetPrimary(write.Ref(EntityTypeCategory, updated.ID, int64(updated.Version)))
		return nil
	}, reload)
}

// Delete 软删除分类；仍被未删除账目引用时返回 409 CATEGORY_IN_USE；要求版本相等。
func (s *CategoryService) Delete(ctx context.Context, a actor.Actor, operationID uuid.UUID, id uuid.UUID, version int64) (write.Result, error) {
	base := version
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "expense_category.delete",
		Fingerprint: write.Fingerprint("expense_category.delete", id.String(), &base, struct{}{}),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo CategoryRepo) error {
		current, found, err := repo.GetForUpdate(ctx, a.AccountID, id)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFound()
		}
		if current.DeletedAt != nil {
			return apperr.Gone("RESOURCE_GONE", "")
		}
		if int64(current.Version) != version {
			return apperr.VersionConflict(&apperr.Conflict{
				EntityType: EntityTypeCategory, EntityID: id, ExpectedVersion: version, CurrentVersion: int64(current.Version), Current: current,
			})
		}
		using, err := repo.LedgerEntriesUsing(ctx, a.AccountID, id)
		if err != nil {
			return err
		}
		if using > 0 {
			return apperr.Conflicted("CATEGORY_IN_USE", "分类仍被账目使用，请先修改这些账目的分类")
		}
		deleted, err := repo.SoftDelete(ctx, a.AccountID, id, s.clock.Now())
		if err != nil {
			return err
		}
		recordCategory(scope, deleted, write.ChangeDelete, nil)
		scope.SetPrimary(write.Ref(EntityTypeCategory, deleted.ID, int64(deleted.Version)))
		return nil
	}, s.reload(a, id))
}

func recordCategory(scope write.Scope, c CategoryResource, kind write.ChangeKind, fields []string) {
	change := write.Change{EntityType: EntityTypeCategory, EntityID: c.ID, TripID: nil, Version: int64(c.Version), Kind: kind}
	if kind == write.ChangeUpsert {
		change.Snapshot = c
		change.ChangedFields = fields
	}
	scope.Record(change)
}
