package packing

import (
	"context"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/write"
)

// TripInfo 是物品所属旅行的窄只读视图；由 PostgreSQL 适配器从 trips 表提供，本包不导入旅行模块。
type TripInfo struct {
	DeletedAt *time.Time
}

// Values 是物品可编辑字段的集合；Update 整体写回。
type Values struct {
	Name     string
	Category Category
	Quantity int32
	Notes    string
	Status   Status
}

// Values 取出资源的可编辑字段。
func (r Resource) Values() Values {
	return Values{Name: r.Name, Category: r.Category, Quantity: r.Quantity, Notes: r.Notes, Status: r.Status}
}

// Repo 是物品在写事务内的仓储；由 PostgreSQL 适配器绑定到当前事务。
type Repo interface {
	// Trip 读取所属旅行的窄视图；不存在或非本人返回 found=false。
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
	Get(ctx context.Context, accountID, tripID, id uuid.UUID) (Resource, bool, error)
	GetForUpdate(ctx context.Context, accountID, tripID, id uuid.UUID) (Resource, bool, error)
	// IDExists 检查 ID 是否已被现有行（含软删除）或墓碑占用。
	IDExists(ctx context.Context, id uuid.UUID) (bool, error)
	// NameTaken 判断本旅行同分类下是否已有同名（去首尾空格、大小写不敏感）有效物品；exclude 为要排除的自身 ID。
	NameTaken(ctx context.Context, accountID, tripID uuid.UUID, category Category, name string, exclude uuid.UUID) (bool, error)
	ProbeBatch(ctx context.Context, accountID, tripID uuid.UUID, items []BatchItem) (map[uuid.UUID]BatchProbe, error)
	Insert(ctx context.Context, accountID uuid.UUID, r Resource) (Resource, error)
	InsertBatch(ctx context.Context, accountID uuid.UUID, items []Resource) ([]Resource, error)
	Update(ctx context.Context, accountID, tripID, id uuid.UUID, v Values, now time.Time) (Resource, error)
	UpdateStatusIfVersion(ctx context.Context, accountID, tripID, id uuid.UUID, version int64, status Status, now time.Time) (Resource, bool, error)
	SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (Resource, error)
	// MergeSource 提供字段级合并所需的变更历史。
	MergeSource() write.MergeSource
}

type BatchProbe struct {
	NameTaken bool
	IDUsed    bool
}

// Position 是键集分页的位置：分类、创建时间与 ID。
type Position struct {
	Category  Category  `json:"category"`
	CreatedAt time.Time `json:"created_at"`
	ID        uuid.UUID `json:"id"`
}

// ListQuery 是列表查询：已校验的筛选、读取条数与起始位置。
type ListQuery struct {
	Category Category
	Status   Status
	Limit    int
	After    *Position
}

// Reader 是事务外的只读仓储。
type Reader interface {
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
	Get(ctx context.Context, accountID, tripID, id uuid.UUID) (Resource, bool, error)
	// List 按 category、created_at、id 升序返回最多 Limit 条有效物品。
	List(ctx context.Context, accountID, tripID uuid.UUID, q ListQuery) ([]Resource, error)
}
