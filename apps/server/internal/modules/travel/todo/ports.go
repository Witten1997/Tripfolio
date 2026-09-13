package todo

import (
	"context"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
)

// TripInfo 是待办所属旅行的窄只读视图；Timezone 用于按当地今天判定逾期。
type TripInfo struct {
	Timezone  string
	DeletedAt *time.Time
}

// Values 是待办可编辑字段的集合；Update 整体写回，CompletedAt 由服务据 Completed 计算。
type Values struct {
	Title       string
	DueOn       *types.Date
	Notes       string
	Completed   bool
	CompletedAt *time.Time
}

// Values 取出资源的可编辑字段。
func (r Resource) Values() Values {
	return Values{Title: r.Title, DueOn: r.DueOn, Notes: r.Notes, Completed: r.Completed, CompletedAt: r.CompletedAt}
}

// Repo 是待办在写事务内的仓储；由 PostgreSQL 适配器绑定到当前事务。
type Repo interface {
	// Trip 读取所属旅行的窄视图；不存在或非本人返回 found=false。
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
	Get(ctx context.Context, accountID, tripID, id uuid.UUID) (Resource, bool, error)
	GetForUpdate(ctx context.Context, accountID, tripID, id uuid.UUID) (Resource, bool, error)
	// IDExists 检查 ID 是否已被现有行（含软删除）或墓碑占用。
	IDExists(ctx context.Context, id uuid.UUID) (bool, error)
	Insert(ctx context.Context, accountID uuid.UUID, r Resource) (Resource, error)
	Update(ctx context.Context, accountID, tripID, id uuid.UUID, v Values, now time.Time) (Resource, error)
	SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (Resource, error)
	// MergeSource 提供字段级合并所需的变更历史。
	MergeSource() write.MergeSource
}

// Position 是键集分页的位置：截止日期（空日期用哨兵）与 ID。
type Position struct {
	DueKey types.Date `json:"due_key"`
	ID     uuid.UUID  `json:"id"`
}

// ListQuery 是列表查询：筛选状态、判定逾期用的当地今天、读取条数与起始位置。
type ListQuery struct {
	State State
	Today types.Date
	Limit int
	After *Position
}

// Reader 是事务外的只读仓储。
type Reader interface {
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
	Get(ctx context.Context, accountID, tripID, id uuid.UUID) (Resource, bool, error)
	// List 按截止日期升序（空日期最后）、再按 id 升序返回最多 Limit 条有效待办。
	List(ctx context.Context, accountID, tripID uuid.UUID, q ListQuery) ([]Resource, error)
}
