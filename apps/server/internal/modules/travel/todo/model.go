// Package todo 是待办业务：创建、编辑、完成状态、截止与逾期规则（接口设计 2.4、3.4；数据库设计表 7）。
// 本包不依赖 HTTP、pgx 或 sqlc；数据访问通过 ports.go 的接口由 PostgreSQL 适配器实现。
package todo

import (
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
)

// EntityType 是待办的同步实体类型（接口设计 4.2）。
const EntityType = "todo"

// Resource 是待办的规范资源，也是同步日志与快照中的表示（接口设计 3.4 Todo）。
// Completed 与 CompletedAt 一致：客户端只写 Completed，服务端维护 CompletedAt。
type Resource struct {
	ID          uuid.UUID     `json:"id"`
	TripID      uuid.UUID     `json:"trip_id"`
	Title       string        `json:"title"`
	DueOn       *types.Date   `json:"due_on"`
	Notes       string        `json:"notes"`
	Completed   bool          `json:"completed"`
	CompletedAt *time.Time    `json:"completed_at"`
	Version     types.Version `json:"version"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	DeletedAt   *time.Time    `json:"deleted_at"`
}

// ListItem 是列表项：规范资源加按旅行时区当天计算的逾期标识（接口设计 3.4 TodoListItem）。
type ListItem struct {
	Resource
	IsOverdue bool `json:"is_overdue"`
}

// Overdue 判断待办在旅行时区的 today 是否逾期：未完成且截止日期早于今天。
func Overdue(r Resource, today types.Date) bool {
	return !r.Completed && r.DueOn != nil && r.DueOn.Before(today)
}

// State 是列表筛选状态。
type State string

const (
	StateAll       State = "all"
	StatePending   State = "pending"
	StateCompleted State = "completed"
	StateOverdue   State = "overdue"
)

// Valid 判断是否为已知筛选状态。
func (s State) Valid() bool {
	return s == StateAll || s == StatePending || s == StateCompleted || s == StateOverdue
}

// DueSentinel 是空截止日期的排序哨兵，与数据库索引表达式一致：空日期排在最后。
const DueSentinel = types.Date("9999-12-31")

// DueKey 返回排序与游标使用的截止日期键。
func DueKey(r Resource) types.Date {
	if r.DueOn == nil {
		return DueSentinel
	}
	return *r.DueOn
}

// Fields 是可局部更新的业务字段名，用于 changed_fields 与字段级合并；
// completed_at 随 completed 一起变化，不单独登记。
var Fields = []string{"title", "due_on", "notes", "completed"}

// Filters 是列表查询条件（接口设计 3.9 TodoFilters）；状态为原始字符串，由服务校验。
type Filters struct {
	State  string
	Limit  int
	Cursor string
}
