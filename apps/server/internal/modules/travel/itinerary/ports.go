package itinerary

import (
	"context"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
)

// TripInfo 是行程所属旅行的窄只读视图；由 PostgreSQL 适配器从 trips 表提供，本包不导入旅行模块。
type TripInfo struct {
	CurrencyCode string
	StartDate    types.Date
	EndDate      types.Date
	DeletedAt    *time.Time
}

// Values 是行程项目可编辑字段的集合；Update 整体写回，归属日期与顺序不在其中。
type Values struct {
	Title                  string
	Kind                   Kind
	PlannedStartLocal      *types.LocalDateTime
	PlannedEndLocal        *types.LocalDateTime
	PlannedDurationMinutes *int32
	PlaceName              string
	Address                string
	Latitude               *float64
	Longitude              *float64
	EstimatedAmount        *string
	Notes                  string
	Status                 Status
	ActualStartLocal       *types.LocalDateTime
	ActualEndLocal         *types.LocalDateTime
	ActualNotes            string
}

// Values 取出资源的可编辑字段。
func (r Resource) Values() Values {
	return Values{
		Title: r.Title, Kind: r.Kind,
		PlannedStartLocal: r.PlannedStartLocal, PlannedEndLocal: r.PlannedEndLocal, PlannedDurationMinutes: r.PlannedDurationMinutes,
		PlaceName: r.PlaceName, Address: r.Address, Latitude: r.Latitude, Longitude: r.Longitude,
		EstimatedAmount: r.EstimatedAmount, Notes: r.Notes, Status: r.Status,
		ActualStartLocal: r.ActualStartLocal, ActualEndLocal: r.ActualEndLocal, ActualNotes: r.ActualNotes,
	}
}

// Repo 是行程项目在写事务内的仓储；由 PostgreSQL 适配器绑定到当前事务。
type Repo interface {
	// Trip 读取所属旅行的窄视图；不存在或非本人返回 found=false。
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
	Get(ctx context.Context, accountID, tripID, id uuid.UUID) (Resource, bool, error)
	GetForUpdate(ctx context.Context, accountID, tripID, id uuid.UUID) (Resource, bool, error)
	// IDExists 检查 ID 是否已被现有行（含软删除）或墓碑占用。
	IDExists(ctx context.Context, id uuid.UUID) (bool, error)
	// MaxSortOrder 返回当天有效项目的最大顺序；没有项目时 found=false。
	MaxSortOrder(ctx context.Context, accountID, tripID uuid.UUID, on types.Date) (int32, bool, error)
	Insert(ctx context.Context, accountID uuid.UUID, r Resource) (Resource, error)
	Update(ctx context.Context, accountID, tripID, id uuid.UUID, v Values, now time.Time) (Resource, error)
	SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (Resource, error)
	// ListDaysForUpdate 锁定并返回给定日期下全部有效项目，按 scheduled_on、sort_order、id 升序。
	ListDaysForUpdate(ctx context.Context, accountID, tripID uuid.UUID, dates []types.Date) ([]Resource, error)
	// Reposition 只修改归属日期与顺序并递增版本。
	Reposition(ctx context.Context, accountID, tripID, id uuid.UUID, on types.Date, sortOrder int32, now time.Time) (Resource, error)
	// MergeSource 提供字段级合并所需的变更历史。
	MergeSource() write.MergeSource
}

// Position 是键集分页的位置：归属日期、同日顺序与 ID。
type Position struct {
	ScheduledOn types.Date `json:"scheduled_on"`
	SortOrder   int32      `json:"sort_order"`
	ID          uuid.UUID  `json:"id"`
}

// ListQuery 是列表查询：已校验的筛选、读取条数与起始位置。
type ListQuery struct {
	DateFrom types.Date
	DateTo   types.Date
	Status   Status
	Limit    int
	After    *Position
}

// Reader 是事务外的只读仓储。
type Reader interface {
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
	Get(ctx context.Context, accountID, tripID, id uuid.UUID) (Resource, bool, error)
	// List 按 scheduled_on、sort_order、id 升序返回最多 Limit 条有效项目。
	List(ctx context.Context, accountID, tripID uuid.UUID, q ListQuery) ([]Resource, error)
}
