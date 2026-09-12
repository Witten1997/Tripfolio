package finance

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/write"
)

// CategoryRepo 是分类在写事务内的仓储；由 PostgreSQL 适配器绑定到当前事务。
type CategoryRepo interface {
	Get(ctx context.Context, accountID, id uuid.UUID) (CategoryResource, bool, error)
	GetForUpdate(ctx context.Context, accountID, id uuid.UUID) (CategoryResource, bool, error)
	Insert(ctx context.Context, accountID uuid.UUID, c CategoryResource) (CategoryResource, error)
	Update(ctx context.Context, accountID uuid.UUID, id uuid.UUID, name string, icon *string, sortOrder int32, now time.Time) (CategoryResource, error)
	SoftDelete(ctx context.Context, accountID, id uuid.UUID, now time.Time) (CategoryResource, error)
	MaxSortOrder(ctx context.Context, accountID uuid.UUID) (int32, error)
	CountActive(ctx context.Context, accountID uuid.UUID) (int64, error)
	LedgerEntriesUsing(ctx context.Context, accountID, id uuid.UUID) (int64, error)
	IDExists(ctx context.Context, id uuid.UUID) (bool, error)
	// MergeSource 提供字段级合并所需的变更历史。
	MergeSource() write.MergeSource
}

// CategoryReader 是事务外的只读仓储。
type CategoryReader interface {
	List(ctx context.Context, accountID uuid.UUID) ([]CategoryResource, error)
	Get(ctx context.Context, accountID, id uuid.UUID) (CategoryResource, bool, error)
}

// ErrDuplicateName 由仓储在名称唯一索引冲突时返回。
var ErrDuplicateName = errors.New("expense category name already exists")
