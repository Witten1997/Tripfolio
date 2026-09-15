// Package share 是旅行分享：主人为一趟旅行开启一条只读分享链接，访客凭分享令牌读取行程骨架与路线。
// 设计见 docs/superpowers/specs/2026-09-15-旅行分享-design.md。
// 分享不进入同步体系：不写变更日志、不递增旅行版本、不进入快照与导出；本包不依赖 HTTP、pgx 或 sqlc。
package share

import (
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/geo"
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/modules/travel/trip"
)

// TokenLength 是分享令牌字符数：16 字节随机数的 base64url 无填充编码。
const TokenLength = 22

// Resource 是主人可见的分享记录（接口设计 3.11 TripShare）。URL 由服务按站点根地址拼出；数据库行见 ports.go 的 Record。
type Resource struct {
	ID           uuid.UUID  `json:"id"`
	Token        string     `json:"token"`
	URL          string     `json:"url"`
	CreatedAt    time.Time  `json:"created_at"`
	RotatedAt    *time.Time `json:"rotated_at"`
	LastViewedAt *time.Time `json:"last_viewed_at"`
	ViewCount    int64      `json:"view_count"`
}

// Viewer 是持有效分享令牌的访客身份。它与 actor.Actor 是不同类型、放在不同的上下文键下，
// 分享令牌因此在类型层面无法触达任何账号接口（设计 5.2）。ClientIP 供访客限流使用。
type Viewer struct {
	ShareID   uuid.UUID
	AccountID uuid.UUID
	TripID    uuid.UUID
	ClientIP  string
}

// PublicTrip 是访客可见的旅行信息（接口设计 3.11）。字段固定，不复用 trip.Resource。
type PublicTrip struct {
	Name        string     `json:"name"`
	Destination string     `json:"destination"`
	StartDate   types.Date `json:"start_date"`
	EndDate     types.Date `json:"end_date"`
	Timezone    string     `json:"timezone"`
}

// PublicItineraryItem 是访客可见的行程骨架（接口设计 3.11）。刻意不含备注、实际情况、预计费用与状态；
// 给 itinerary_items 加字段不会自动流到这里，model_test 的白名单测试守住字段集合。
type PublicItineraryItem struct {
	ID                     uuid.UUID            `json:"id"`
	ScheduledOn            types.Date           `json:"scheduled_on"`
	SortOrder              int32                `json:"sort_order"`
	Title                  string               `json:"title"`
	Kind                   itinerary.Kind       `json:"kind"`
	PlaceName              string               `json:"place_name"`
	Address                string               `json:"address"`
	Latitude               *float64             `json:"latitude"`
	Longitude              *float64             `json:"longitude"`
	PlannedStartLocal      *types.LocalDateTime `json:"planned_start_local"`
	PlannedEndLocal        *types.LocalDateTime `json:"planned_end_local"`
	PlannedDurationMinutes *int32               `json:"planned_duration_minutes"`
}

// PublicLeg 是一段成功算出的相邻路段。
type PublicLeg struct {
	FromItemID      uuid.UUID        `json:"from_item_id"`
	ToItemID        uuid.UUID        `json:"to_item_id"`
	DistanceMeters  int64            `json:"distance_meters"`
	DurationSeconds int64            `json:"duration_seconds"`
	Path            []geo.Coordinate `json:"path"`
}

// PublicRoutes 是整趟旅行的路线结果：只含成功路段，总里程由客户端按成功路段汇总。
type PublicRoutes struct {
	Mode               geo.Mode    `json:"mode"`
	Legs               []PublicLeg `json:"legs"`
	FailedLegCount     int         `json:"failed_leg_count"`
	UnlocatedItemCount int         `json:"unlocated_item_count"`
}

// ToPublicTrip 从规范旅行资源投影公开字段。
func ToPublicTrip(t trip.Resource) PublicTrip {
	return PublicTrip{Name: t.Name, Destination: t.Destination, StartDate: t.StartDate, EndDate: t.EndDate, Timezone: t.Timezone}
}

// ToPublicItem 从规范行程资源投影公开字段。
func ToPublicItem(r itinerary.Resource) PublicItineraryItem {
	return PublicItineraryItem{
		ID: r.ID, ScheduledOn: r.ScheduledOn, SortOrder: r.SortOrder, Title: r.Title, Kind: r.Kind,
		PlaceName: r.PlaceName, Address: r.Address, Latitude: r.Latitude, Longitude: r.Longitude,
		PlannedStartLocal: r.PlannedStartLocal, PlannedEndLocal: r.PlannedEndLocal, PlannedDurationMinutes: r.PlannedDurationMinutes,
	}
}
