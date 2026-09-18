package finance

import (
	"context"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/trip"
)

// LedgerTripInfo 是账目所属旅行的窄只读视图：币种用于规范化金额与派生 currency_code，
// 时区用于默认实际日期，CurrencyLockedAt 用于判断是否需要锁定币种。
type LedgerTripInfo struct {
	Timezone         string
	CurrencyCode     string
	BudgetAmount     *string
	CurrencyLockedAt *time.Time
	DeletedAt        *time.Time
}

// LedgerValues 是账目可编辑字段的集合；Update 整体写回，kind 与币种不在其中。
type LedgerValues struct {
	Amount             string
	SplitCount         int32
	PersonalAmount     string
	CategoryID         uuid.UUID
	OccurredOn         types.Date
	Notes              string
	RefundedEntryID    *uuid.UUID
	AttachmentAssetIDs []uuid.UUID
}

// Values 取出资源的可编辑字段。
func (r LedgerResource) Values() LedgerValues {
	return LedgerValues{
		Amount: r.Amount, SplitCount: r.SplitCount, PersonalAmount: r.PersonalAmount,
		CategoryID: r.CategoryID, OccurredOn: r.OccurredOn, Notes: r.Notes,
		RefundedEntryID: r.RefundedEntryID, AttachmentAssetIDs: append([]uuid.UUID(nil), r.AttachmentAssetIDs...),
	}
}

// LedgerRepo 是账目在写事务内的仓储；由 PostgreSQL 适配器绑定到当前事务。
type LedgerRepo interface {
	// Trip 读取所属旅行的窄视图；不存在或非本人返回 found=false。
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (LedgerTripInfo, bool, error)
	// SetTripCurrencyLock 写入或清空旅行的 currency_locked_at，递增旅行版本并返回其规范资源作为同步快照。
	SetTripCurrencyLock(ctx context.Context, accountID, tripID uuid.UUID, lockedAt *time.Time, now time.Time) (trip.Resource, error)
	// CategoryActive 判断分类是否为本账号未删除的分类。
	CategoryActive(ctx context.Context, accountID, categoryID uuid.UUID) (bool, error)
	// MissingAssets 返回 ids 中不是本账号同旅行、未删除的图片资产的 ID；顺序与输入一致。
	MissingAssets(ctx context.Context, accountID, tripID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error)
	Get(ctx context.Context, accountID, tripID, id uuid.UUID) (LedgerResource, bool, error)
	GetForUpdate(ctx context.Context, accountID, tripID, id uuid.UUID) (LedgerResource, bool, error)
	// IDExists 检查 ID 是否已被现有行（含软删除）或墓碑占用。
	IDExists(ctx context.Context, id uuid.UUID) (bool, error)
	Insert(ctx context.Context, accountID uuid.UUID, r LedgerResource) (LedgerResource, error)
	Update(ctx context.Context, accountID, tripID, id uuid.UUID, v LedgerValues, now time.Time) (LedgerResource, error)
	SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (LedgerResource, error)
	// LinkedRefundsForUpdate 返回关联到 expenseID 的全部有效退款并锁定，按 created_at、id 升序。
	LinkedRefundsForUpdate(ctx context.Context, accountID, tripID, expenseID uuid.UUID) ([]LedgerResource, error)
	// CountActive 返回旅行内有效账目数，用于币种锁定与解锁。
	CountActive(ctx context.Context, accountID, tripID uuid.UUID) (int64, error)
	// MergeSource 提供字段级合并所需的变更历史。
	MergeSource() write.MergeSource
}

// LedgerPosition 是键集分页的位置：实际日期与 ID，均降序。
type LedgerPosition struct {
	OccurredOn types.Date `json:"occurred_on"`
	ID         uuid.UUID  `json:"id"`
}

// LedgerListQuery 是已校验的列表查询。
type LedgerListQuery struct {
	DateFrom        *types.Date
	DateTo          *types.Date
	CategoryID      *uuid.UUID
	Kind            *LedgerKind
	RefundedEntryID *uuid.UUID
	Limit           int
	After           *LedgerPosition
}

// StatisticsQuery 是已校验的统计查询；DailyAfter 是每日明细的游标位置（日期降序，取更早的日期）。
type StatisticsQuery struct {
	DateFrom   *types.Date
	DateTo     *types.Date
	CategoryID *uuid.UUID
	DailyLimit int
	DailyAfter *types.Date
}

// CategoryAggregate 是单个分类的原始聚合：筛选范围内与整趟旅行的支出、退款（NUMERIC 文本）。
type CategoryAggregate struct {
	CategoryID      uuid.UUID
	Name            string
	Icon            *string
	SortOrder       int32
	FilteredExpense string
	FilteredRefund  string
	TripExpense     string
	TripRefund      string
}

// DailyAggregate 是某一天的原始聚合。
type DailyAggregate struct {
	Date    types.Date
	Expense string
	Refund  string
}

// StatisticsData 是统计所需的全部原始聚合，由适配器在同一个只读一致性事务中读取（数据库设计 §5）。
// 金额为 NUMERIC 文本（可含尾随零），换算与占比由服务完成。
type StatisticsData struct {
	Trip LedgerTripInfo
	// FilteredExpense、FilteredRefund、FilteredCount 是日期与分类筛选范围内的合计。
	FilteredExpense string
	FilteredRefund  string
	FilteredCount   int64
	// TripExpense、TripRefund 是整趟旅行不受筛选影响的合计。
	TripExpense string
	TripRefund  string
	// Categories 包含本账号全部有效分类以及仍被本旅行有效账目引用的已删除分类，按 sort_order、id 升序。
	Categories []CategoryAggregate
	// Daily 是筛选范围内有账目的日期，按日期降序，最多 DailyLimit 行（服务多取一行判断下一页）。
	Daily []DailyAggregate
}

// LedgerReader 是事务外的只读仓储。
type LedgerReader interface {
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (LedgerTripInfo, bool, error)
	Get(ctx context.Context, accountID, tripID, id uuid.UUID) (LedgerResource, bool, error)
	// List 按 occurred_on、id 均降序返回最多 Limit 条有效账目。
	List(ctx context.Context, accountID, tripID uuid.UUID, q LedgerListQuery) ([]LedgerResource, error)
	// Statistics 在一个只读一致性事务中读取统计聚合；旅行不存在或非本人返回 found=false。
	Statistics(ctx context.Context, accountID, tripID uuid.UUID, q StatisticsQuery) (StatisticsData, bool, error)
}
