package share

import (
	"context"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/modules/geo"
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/modules/travel/trip"
)

// Record 是 trip_shares 的一行；Resource 由服务据此拼出 url 后返回给主人。
type Record struct {
	ID           uuid.UUID
	AccountID    uuid.UUID
	TripID       uuid.UUID
	Token        string
	CreatedAt    time.Time
	RotatedAt    *time.Time
	LastViewedAt *time.Time
	ViewCount    int64
}

// Resolved 是按令牌解析的结果：分享行加主人账号状态与旅行的回收站标记，访客侧一次查询完成全部前置判断。
type Resolved struct {
	Record
	OwnerStatus   string
	TripDeletedAt *time.Time
}

// Store 是分享记录的仓储。分享不进入同步写事务，直接以连接池执行。
type Store interface {
	GetByTrip(ctx context.Context, accountID, tripID uuid.UUID) (Record, bool, error)
	ResolveToken(ctx context.Context, token string) (Resolved, bool, error)
	// Insert 在 (account_id, trip_id) 已存在时不修改现有行并返回 inserted=false。
	Insert(ctx context.Context, r Record) (Record, bool, error)
	// Rotate 换令牌并写 rotated_at；分享不存在返回 found=false。
	Rotate(ctx context.Context, accountID, tripID uuid.UUID, token string, now time.Time) (Record, bool, error)
	// Delete 物理删行；不存在也成功。
	Delete(ctx context.Context, accountID, tripID uuid.UUID) error
	// RecordView 递增 view_count 并写 last_viewed_at。
	RecordView(ctx context.Context, id uuid.UUID, now time.Time) error
}

// TripSource 读取主人的旅行；trip.Reader 直接满足。
type TripSource interface {
	Get(ctx context.Context, accountID, id uuid.UUID) (trip.Resource, bool, error)
}

// ItinerarySource 读取主人的行程；itinerary.Reader 直接满足。
// 访客读取传入主人的 account_id：复用既有带账号过滤的查询，不新写任何不带账号过滤的 SQL。
type ItinerarySource interface {
	List(ctx context.Context, accountID, tripID uuid.UUID, q itinerary.ListQuery) ([]itinerary.Resource, error)
}

// RouteSource 以调用方指定的限流键算路；geo.Service 满足（S.7 新增 RouteAs）。
type RouteSource interface {
	RouteAs(ctx context.Context, limitKey string, origin, destination geo.Coordinate, mode geo.Mode) (geo.Route, error)
}

// Limiter 是固定窗口限流器；由 adapters/ratelimit 实现。
type Limiter interface {
	Allow(key string, limit int, window time.Duration, now time.Time) (bool, time.Duration)
}
