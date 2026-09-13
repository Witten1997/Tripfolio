// Package packing 是行李清单业务：物品的创建、编辑、状态、软删除、批量创建与内置物品库
// （接口设计 2.4、3.4；数据库设计表 6）。本包不依赖 HTTP、pgx 或 sqlc。
package packing

import (
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
)

// EntityType 是物品的同步实体类型（接口设计 4.2）。
const EntityType = "packing_item"

// Category 是物资分类。
type Category string

const (
	CategoryDocuments   Category = "documents"
	CategoryElectronics Category = "electronics"
	CategoryClothing    Category = "clothing"
	CategoryDaily       Category = "daily"
	CategoryFood        Category = "food"
	CategoryMedicine    Category = "medicine"
	CategoryOther       Category = "other"
)

// Valid 判断是否为已知分类；清单与 metadata.PackingCategories 一致。
func (c Category) Valid() bool {
	switch c {
	case CategoryDocuments, CategoryElectronics, CategoryClothing, CategoryDaily, CategoryFood, CategoryMedicine, CategoryOther:
		return true
	}
	return false
}

// Status 是物品状态。
type Status string

const (
	StatusPending Status = "pending"
	StatusReady   Status = "ready"
	StatusPacked  Status = "packed"
)

// Valid 判断是否为已知状态；清单与 metadata.PackingStatuses 一致。
func (s Status) Valid() bool {
	return s == StatusPending || s == StatusReady || s == StatusPacked
}

// Resource 是物品的规范资源，也是同步日志与快照中的表示（接口设计 3.4 PackingItem）。
type Resource struct {
	ID        uuid.UUID     `json:"id"`
	TripID    uuid.UUID     `json:"trip_id"`
	Name      string        `json:"name"`
	Category  Category      `json:"category"`
	Quantity  int32         `json:"quantity"`
	Notes     string        `json:"notes"`
	Status    Status        `json:"status"`
	Version   types.Version `json:"version"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	DeletedAt *time.Time    `json:"deleted_at"`
}

// Fields 是可局部更新的业务字段名，用于 changed_fields 与字段级合并。
var Fields = []string{"name", "category", "quantity", "notes", "status"}

// Filters 是列表查询条件（接口设计 3.9 PackingFilters）；字符串由服务校验。
type Filters struct {
	Category string
	Status   string
	Limit    int
	Cursor   string
}

// BatchResult 是批量创建的返回（接口设计 3.4 PackingBatchResult）：内嵌 WriteResult，另附创建与跳过明细。
// primary 与 data 恒为 null，affected 为已创建物品。
type BatchResult struct {
	write.Result
	CreatedIDs []uuid.UUID   `json:"created_ids"`
	Skipped    []SkippedItem `json:"skipped"`
}

// SkippedItem 是被跳过的物品；reason 目前仅 duplicate。
type SkippedItem struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Category Category  `json:"category"`
	Reason   string    `json:"reason"`
}

// ReasonDuplicate 表示同分类同名已存在或请求内重复。
const ReasonDuplicate = "duplicate"
