// Package trip 是旅行业务：创建、修改、归档、回收站与阶段计算（接口设计 2.2、3.2；数据库设计表 4）。
// 本包不依赖 HTTP、pgx 或 sqlc；数据访问通过 ports.go 的接口由 PostgreSQL 适配器实现。
package trip

import (
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
)

// EntityType 是旅行的同步实体类型。
const EntityType = "trip"

// EntityTypeDeletionJob 是永久清理任务在 WriteResult.affected 中的类型名；任务没有资源版本。
const EntityTypeDeletionJob = "deletion_job"

// RecycleBinRetention 是回收站保留期：进入回收站后 30 天内可恢复（总览 4.4）。
const RecycleBinRetention = 30 * 24 * time.Hour

// Resource 是旅行的规范资源，也是同步日志与快照中的表示（接口设计 3.2 Trip / TrashedTrip）。
// 阶段 phase 随时间变化，不在规范资源中，由 ListItem 另行携带。
type Resource struct {
	ID               uuid.UUID     `json:"id"`
	Name             string        `json:"name"`
	StartDate        types.Date    `json:"start_date"`
	EndDate          types.Date    `json:"end_date"`
	Destination      string        `json:"destination"`
	Notes            string        `json:"notes"`
	Timezone         string        `json:"timezone"`
	CurrencyCode     string        `json:"currency_code"`
	BudgetAmount     *string       `json:"budget_amount"`
	Version          types.Version `json:"version"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	ArchivedAt       *time.Time    `json:"archived_at"`
	CurrencyLockedAt *time.Time    `json:"currency_locked_at"`
	DeletedAt        *time.Time    `json:"deleted_at"`
	PurgeAfterAt     *time.Time    `json:"purge_after_at"`
	PurgeRequestedAt *time.Time    `json:"purge_requested_at"`
}

// Phase 是按旅行时区的今天与起止日期计算的阶段。
type Phase string

const (
	PhasePlanned Phase = "planned"
	PhaseOngoing Phase = "ongoing"
	PhaseEnded   Phase = "ended"
)

// Valid 判断是否为已知阶段。
func (p Phase) Valid() bool {
	return p == PhasePlanned || p == PhaseOngoing || p == PhaseEnded
}

// ListItem 是列表项：规范资源加阶段（接口设计 3.2 TripListItem）。
type ListItem struct {
	Resource
	Phase Phase `json:"phase"`
}

// PhaseOf 计算 r 在 now 时刻的阶段。旅行时区无法加载时按 UTC 计算，避免列表因历史数据出错而不可用。
func PhaseOf(r Resource, now time.Time) Phase {
	today, err := types.TodayIn(now, r.Timezone)
	if err != nil {
		today = types.DateOf(now.UTC())
	}
	switch {
	case today.Before(r.StartDate):
		return PhasePlanned
	case today.After(r.EndDate):
		return PhaseEnded
	default:
		return PhaseOngoing
	}
}

// Fields 是旅行可局部更新的业务字段名，用于 changed_fields 与字段级合并；创建时记录全部字段。
var Fields = []string{"name", "start_date", "end_date", "destination", "notes", "timezone", "currency_code", "budget_amount"}

// CreateFields 是创建变更记录的字段集合：全部业务字段加归档与币种锁状态。
var CreateFields = append(append([]string{}, Fields...), "archived_at", "currency_locked_at")

// Sort 是列表排序。
type Sort string

const (
	SortStartDateDesc Sort = "start_date_desc"
	SortUpdatedAtDesc Sort = "updated_at_desc"
)

// Valid 判断是否为已知排序。
func (s Sort) Valid() bool { return s == SortStartDateDesc || s == SortUpdatedAtDesc }

// ArchivedFilter 是归档筛选。
type ArchivedFilter string

const (
	ArchivedAll   ArchivedFilter = "all"
	ArchivedOnly  ArchivedFilter = "true"
	ArchivedNone  ArchivedFilter = "false"
	maxQueryChars                = 100
)

// Valid 判断是否为已知归档筛选。
func (f ArchivedFilter) Valid() bool {
	return f == ArchivedAll || f == ArchivedOnly || f == ArchivedNone
}

// Filters 是列表查询条件（接口设计 3.9 TripFilters）。零值表示默认：全部归档状态、按开始日期倒序、每页 50。
type Filters struct {
	Query    string
	Phase    *Phase
	Archived ArchivedFilter
	Sort     Sort
	Limit    int
	Cursor   string
}

// Page 是一页结果。
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
