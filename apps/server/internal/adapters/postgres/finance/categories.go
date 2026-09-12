// Package financepg 是 finance 模块的 PostgreSQL 适配：事务内仓储、只读仓储与注册时的预设分类种入。
package financepg

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/finance"
)

const nameUniqueIndex = "expense_categories_account_name_unique"

func toResource(row dbgen.ExpenseCategory) finance.CategoryResource {
	return finance.CategoryResource{
		ID: row.ID, Name: row.Name, Icon: row.Icon, SortOrder: row.SortOrder, IsPreset: row.IsPreset,
		Version: types.Version(row.Version), CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt),
		DeletedAt: pgcore.UTCPtr(row.DeletedAt),
	}
}

// categoryRepo 绑定到一次写事务。
type categoryRepo struct {
	scope *pgcore.TxScope
}

var _ finance.CategoryRepo = (*categoryRepo)(nil)

// NewCategoryUnitOfWork 创建分类写事务入口。
func NewCategoryUnitOfWork(writer *pgcore.Writer) write.UnitOfWork[finance.CategoryRepo] {
	return pgcore.NewUnitOfWork(writer, func(scope *pgcore.TxScope) finance.CategoryRepo {
		return &categoryRepo{scope: scope}
	})
}

func (r *categoryRepo) MergeSource() write.MergeSource { return r.scope }

func (r *categoryRepo) Get(ctx context.Context, accountID, id uuid.UUID) (finance.CategoryResource, bool, error) {
	row, err := r.scope.Queries.GetExpenseCategory(ctx, dbgen.GetExpenseCategoryParams{AccountID: accountID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return finance.CategoryResource{}, false, nil
	}
	if err != nil {
		return finance.CategoryResource{}, false, err
	}
	return toResource(row), true, nil
}

func (r *categoryRepo) GetForUpdate(ctx context.Context, accountID, id uuid.UUID) (finance.CategoryResource, bool, error) {
	row, err := r.scope.Queries.GetExpenseCategoryForUpdate(ctx, dbgen.GetExpenseCategoryForUpdateParams{AccountID: accountID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return finance.CategoryResource{}, false, nil
	}
	if err != nil {
		return finance.CategoryResource{}, false, err
	}
	return toResource(row), true, nil
}

func (r *categoryRepo) Insert(ctx context.Context, accountID uuid.UUID, c finance.CategoryResource) (finance.CategoryResource, error) {
	row, err := r.scope.Queries.InsertExpenseCategory(ctx, dbgen.InsertExpenseCategoryParams{
		ID: c.ID, AccountID: accountID, Name: c.Name, Icon: c.Icon, SortOrder: c.SortOrder, IsPreset: c.IsPreset, CreatedAt: c.CreatedAt,
	})
	if pgcore.ConstraintViolation(err) == nameUniqueIndex {
		return finance.CategoryResource{}, finance.ErrDuplicateName
	}
	if err != nil {
		return finance.CategoryResource{}, err
	}
	return toResource(row), nil
}

func (r *categoryRepo) Update(ctx context.Context, accountID, id uuid.UUID, name string, icon *string, sortOrder int32, now time.Time) (finance.CategoryResource, error) {
	row, err := r.scope.Queries.UpdateExpenseCategory(ctx, dbgen.UpdateExpenseCategoryParams{
		AccountID: accountID, ID: id, Name: name, Icon: icon, SortOrder: sortOrder, UpdatedAt: now,
	})
	if pgcore.ConstraintViolation(err) == nameUniqueIndex {
		return finance.CategoryResource{}, finance.ErrDuplicateName
	}
	if err != nil {
		return finance.CategoryResource{}, err
	}
	return toResource(row), nil
}

func (r *categoryRepo) SoftDelete(ctx context.Context, accountID, id uuid.UUID, now time.Time) (finance.CategoryResource, error) {
	row, err := r.scope.Queries.SoftDeleteExpenseCategory(ctx, dbgen.SoftDeleteExpenseCategoryParams{AccountID: accountID, ID: id, DeletedAt: &now})
	if err != nil {
		return finance.CategoryResource{}, err
	}
	return toResource(row), nil
}

func (r *categoryRepo) MaxSortOrder(ctx context.Context, accountID uuid.UUID) (int32, error) {
	return r.scope.Queries.MaxExpenseCategorySortOrder(ctx, accountID)
}

func (r *categoryRepo) CountActive(ctx context.Context, accountID uuid.UUID) (int64, error) {
	return r.scope.Queries.CountExpenseCategories(ctx, accountID)
}

func (r *categoryRepo) LedgerEntriesUsing(ctx context.Context, accountID, id uuid.UUID) (int64, error) {
	return r.scope.Queries.CountLedgerEntriesUsingCategory(ctx, dbgen.CountLedgerEntriesUsingCategoryParams{AccountID: accountID, CategoryID: id})
}

func (r *categoryRepo) IDExists(ctx context.Context, id uuid.UUID) (bool, error) {
	return r.scope.Queries.ExpenseCategoryIDExists(ctx, id)
}

// CategoryReader 是事务外只读仓储。
type CategoryReader struct {
	q *dbgen.Queries
}

// NewCategoryReader 创建只读仓储。
func NewCategoryReader(pool *pgxpool.Pool) *CategoryReader {
	return &CategoryReader{q: dbgen.New(pool)}
}

var _ finance.CategoryReader = (*CategoryReader)(nil)

// List 返回有效分类。
func (r *CategoryReader) List(ctx context.Context, accountID uuid.UUID) ([]finance.CategoryResource, error) {
	rows, err := r.q.ListExpenseCategories(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]finance.CategoryResource, 0, len(rows))
	for _, row := range rows {
		out = append(out, toResource(row))
	}
	return out, nil
}

// Get 返回分类（含已删除）。
func (r *CategoryReader) Get(ctx context.Context, accountID, id uuid.UUID) (finance.CategoryResource, bool, error) {
	row, err := r.q.GetExpenseCategory(ctx, dbgen.GetExpenseCategoryParams{AccountID: accountID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return finance.CategoryResource{}, false, nil
	}
	if err != nil {
		return finance.CategoryResource{}, false, err
	}
	return toResource(row), true, nil
}

// SeedPresets 在注册事务内种入六个预设分类并写入账号级变更日志；返回新的同步序列。
func SeedPresets(ctx context.Context, q *dbgen.Queries, accountID, batchID uuid.UUID, lastSeq int64, now time.Time) (int64, error) {
	changes := make([]write.Change, 0, 6)
	for i, p := range finance.PresetCategories() {
		icon := p.Icon
		row, err := q.InsertExpenseCategory(ctx, dbgen.InsertExpenseCategoryParams{
			ID: uuid.New(), AccountID: accountID, Name: p.Name, Icon: &icon, SortOrder: int32(i), IsPreset: true, CreatedAt: now,
		})
		if err != nil {
			return lastSeq, err
		}
		res := toResource(row)
		changes = append(changes, write.Change{
			EntityType: finance.EntityTypeCategory, EntityID: res.ID, Version: int64(res.Version), Kind: write.ChangeUpsert,
			Snapshot: res, ChangedFields: append([]string{"is_preset"}, finance.CategoryFields...),
		})
	}
	return pgcore.RecordChanges(ctx, q, accountID, batchID, lastSeq, changes, now)
}
