// Package itinerary 是每日行程业务：项目的创建、编辑、状态、软删除与按天重排（接口设计 2.3、3.3；数据库设计表 5）。
// 本包不依赖 HTTP、pgx 或 sqlc；数据访问通过 ports.go 的接口由 PostgreSQL 适配器实现。
package itinerary

import (
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
)

// EntityType 是行程项目的同步实体类型（接口设计 4.2）。
const EntityType = "itinerary_item"

// Kind 是项目类型。
type Kind string

const (
	KindAttraction Kind = "attraction"
	KindTransport  Kind = "transport"
	KindLodging    Kind = "lodging"
	KindDining     Kind = "dining"
	KindOther      Kind = "other"
)

// Valid 判断是否为已知类型；清单与 metadata.ItineraryKinds 一致。
func (k Kind) Valid() bool {
	switch k {
	case KindAttraction, KindTransport, KindLodging, KindDining, KindOther:
		return true
	}
	return false
}

// Status 是项目状态。
type Status string

const (
	StatusPending   Status = "pending"
	StatusCompleted Status = "completed"
	StatusSkipped   Status = "skipped"
)

// Valid 判断是否为已知状态；清单与 metadata.ItineraryStatuses 一致。
func (s Status) Valid() bool {
	return s == StatusPending || s == StatusCompleted || s == StatusSkipped
}

// Resource 是行程项目的规范资源，也是同步日志与快照中的表示（接口设计 3.3 ItineraryItem）。
// CurrencyCode 由旅行派生、只读，与 EstimatedAmount 成对出现。
type Resource struct {
	ID                     uuid.UUID            `json:"id"`
	TripID                 uuid.UUID            `json:"trip_id"`
	Title                  string               `json:"title"`
	Kind                   Kind                 `json:"kind"`
	ScheduledOn            types.Date           `json:"scheduled_on"`
	SortOrder              int32                `json:"sort_order"`
	PlannedStartLocal      *types.LocalDateTime `json:"planned_start_local"`
	PlannedEndLocal        *types.LocalDateTime `json:"planned_end_local"`
	PlannedDurationMinutes *int32               `json:"planned_duration_minutes"`
	PlaceName              string               `json:"place_name"`
	Address                string               `json:"address"`
	Latitude               *float64             `json:"latitude"`
	Longitude              *float64             `json:"longitude"`
	EstimatedAmount        *string              `json:"estimated_amount"`
	CurrencyCode           *string              `json:"currency_code"`
	Notes                  string               `json:"notes"`
	Status                 Status               `json:"status"`
	ActualStartLocal       *types.LocalDateTime `json:"actual_start_local"`
	ActualEndLocal         *types.LocalDateTime `json:"actual_end_local"`
	ActualNotes            string               `json:"actual_notes"`
	Version                types.Version        `json:"version"`
	CreatedAt              time.Time            `json:"created_at"`
	UpdatedAt              time.Time            `json:"updated_at"`
	DeletedAt              *time.Time           `json:"deleted_at"`
}

// Fields 是可通过局部更新修改的业务字段名，用于 changed_fields 与字段级合并。
// scheduled_on 与 sort_order 只能由重排接口修改，因此不在此列：编辑与重排并发时不相互冲突。
var Fields = []string{
	"title", "kind", "planned_start_local", "planned_end_local", "planned_duration_minutes",
	"place_name", "address", "latitude", "longitude", "estimated_amount",
	"notes", "status", "actual_start_local", "actual_end_local", "actual_notes",
}

// PositionFields 是重排修改的字段名。
var PositionFields = []string{"scheduled_on", "sort_order"}

// CreateFields 是创建变更记录的字段集合：全部业务字段加归属日期与顺序。
var CreateFields = append(append([]string{}, Fields...), PositionFields...)

// Filters 是列表查询条件（接口设计 3.9 ItineraryFilters）；日期为原始字符串，由服务校验。
type Filters struct {
	DateFrom string
	DateTo   string
	Status   string
	Limit    int
	Cursor   string
}
