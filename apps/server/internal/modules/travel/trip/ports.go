package trip

import (
	"context"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/member"
)

// Values 是旅行可编辑字段的集合；Update 整体写回。
type Values struct {
	Name                     string
	StartDate                types.Date
	EndDate                  types.Date
	Destination              string
	Notes                    string
	Timezone                 string
	CurrencyCode             string
	BudgetAmount             *string
	RouteShortMode           string
	RouteShortDistanceMeters int32
}

// Values 取出资源的可编辑字段。
func (r Resource) Values() Values {
	return Values{
		Name: r.Name, StartDate: r.StartDate, EndDate: r.EndDate, Destination: r.Destination, Notes: r.Notes,
		Timezone: r.Timezone, CurrencyCode: r.CurrencyCode, BudgetAmount: r.BudgetAmount,
		RouteShortMode: r.RouteShortMode, RouteShortDistanceMeters: r.RouteShortDistanceMeters,
	}
}

// DeletionJob 是永久清理任务的最小表示（数据库设计表 23）；进度查询接口在数据管理切片提供。
type DeletionJob struct {
	ID             uuid.UUID
	OwnerAccountID uuid.UUID
	TargetTripID   uuid.UUID
	Status         string
	Stage          string
	CreatedAt      time.Time
}

// PurgeJobArgs 是永久清理的后台任务参数；Kind 即 River 任务类型，worker 在 transport/river 注册。
type PurgeJobArgs struct {
	JobID     uuid.UUID `json:"job_id"`
	AccountID uuid.UUID `json:"account_id"`
	TripID    uuid.UUID `json:"trip_id"`
}

// Kind 实现 write.JobArgs 与 river.JobArgs。
func (PurgeJobArgs) Kind() string { return "trip_purge" }

// ContentProbe 是旅行内容的只读探针，供修改日期、时区与币种时做跨模块检查；由适配器实现。
type ContentProbe interface {
	// HasEstimatedAmounts 判断是否存在未删除且预计费用非空的行程项目。
	HasEstimatedAmounts(ctx context.Context, accountID, tripID uuid.UUID) (bool, error)
	// HasLocalTimes 判断是否存在未删除的当地时间记录（行程计划/实际时间、预订起止、照片拍摄时刻）。
	HasLocalTimes(ctx context.Context, accountID, tripID uuid.UUID) (bool, error)
	// HasItineraryOutside 判断是否存在未删除且归属日期在 [start, end] 之外的行程项目。
	HasItineraryOutside(ctx context.Context, accountID, tripID uuid.UUID, start, end types.Date) (bool, error)
}

// Repo 是旅行在写事务内的仓储；由 PostgreSQL 适配器绑定到当前事务。
type Repo interface {
	ContentProbe
	Get(ctx context.Context, accountID, id uuid.UUID) (Resource, bool, error)
	GetForUpdate(ctx context.Context, accountID, id uuid.UUID) (Resource, bool, error)
	// IDExists 检查 ID 是否已被现有行（含回收站）或墓碑占用。
	IDExists(ctx context.Context, id uuid.UUID) (bool, error)
	AccountDefaultTimezone(ctx context.Context, accountID uuid.UUID) (string, error)
	Insert(ctx context.Context, accountID uuid.UUID, r Resource) (Resource, error)
	// InsertMember 在创建旅行的同一事务里插入成员「我」（数据库设计表 25）。
	InsertMember(ctx context.Context, accountID uuid.UUID, m member.Resource) (member.Resource, error)
	Update(ctx context.Context, accountID, id uuid.UUID, v Values, now time.Time) (Resource, error)
	InvalidateRouteSummary(ctx context.Context, accountID, tripID uuid.UUID, now time.Time) (int64, error)
	SetArchived(ctx context.Context, accountID, id uuid.UUID, archivedAt *time.Time, now time.Time) (Resource, error)
	Trash(ctx context.Context, accountID, id uuid.UUID, now, purgeAfter time.Time) (Resource, error)
	Restore(ctx context.Context, accountID, id uuid.UUID, now time.Time) (Resource, error)
	RequestPurge(ctx context.Context, accountID, id uuid.UUID, now time.Time) (Resource, error)
	ActiveDeletionJob(ctx context.Context, accountID, tripID uuid.UUID) (DeletionJob, bool, error)
	InsertDeletionJob(ctx context.Context, job DeletionJob) (DeletionJob, error)
	// MergeSource 提供字段级合并所需的变更历史。
	MergeSource() write.MergeSource
}

// Position 是键集分页的位置；随排序只使用其中一个时间键，ID 作次序。
type Position struct {
	StartDate types.Date `json:"start_date,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	ID        uuid.UUID  `json:"id"`
}

// ListQuery 是有效旅行列表的查询：已规范化的筛选、计算阶段用的当前时刻、读取条数与起始位置。
type ListQuery struct {
	Filters Filters
	Now     time.Time
	Limit   int
	After   *Position
}

// TrashedQuery 是回收站列表的查询。
type TrashedQuery struct {
	Limit int
	After *Position
}

// Reader 是事务外的只读仓储。
type Reader interface {
	Get(ctx context.Context, accountID, id uuid.UUID) (Resource, bool, error)
	// List 按查询返回最多 Limit 条有效旅行及其阶段，顺序与接口设计 3.9 一致。
	List(ctx context.Context, accountID uuid.UUID, q ListQuery) ([]ListItem, error)
	// ListTrashed 按删除时间倒序返回回收站旅行。
	ListTrashed(ctx context.Context, accountID uuid.UUID, q TrashedQuery) ([]Resource, error)
}
