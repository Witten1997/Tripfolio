// Package finance 是账单业务：账号级账单分类、账目与退款、开支统计。
// 本包不依赖 HTTP、pgx 或 sqlc；数据访问通过 ports.go 的接口由 PostgreSQL 适配器实现。
package finance

import (
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
)

// EntityTypeCategory 是账单分类的同步实体类型。
const EntityTypeCategory = "expense_category"

// CategoryResource 是账单分类的对外资源，也是同步日志与快照中的表示（接口设计 3.5）。
type CategoryResource struct {
	ID        uuid.UUID     `json:"id"`
	Name      string        `json:"name"`
	Icon      *string       `json:"icon"`
	SortOrder int32         `json:"sort_order"`
	IsPreset  bool          `json:"is_preset"`
	Version   types.Version `json:"version"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	DeletedAt *time.Time    `json:"deleted_at"`
}

// Preset 是注册时种入的预设分类。
type Preset struct {
	Name string
	Icon string
}

// PresetCategories 返回接口设计 3.5 规定的六个预设，顺序即 sort_order。
func PresetCategories() []Preset {
	return []Preset{
		{"交通", "transport"}, {"住宿", "lodging"}, {"美食", "food"},
		{"景点", "attraction"}, {"购物", "shopping"}, {"其他", "other"},
	}
}

// CategoryIcons 是可用的图标键集合，与 metadata 一致。
var CategoryIcons = map[string]struct{}{
	"transport": {}, "lodging": {}, "food": {}, "attraction": {}, "shopping": {},
	"entertainment": {}, "ticket": {}, "gift": {}, "medical": {}, "other": {},
}

// CategoryFields 是分类可局部更新的字段名，用于 changed_fields 与字段级合并。
var CategoryFields = []string{"name", "icon", "sort_order"}
