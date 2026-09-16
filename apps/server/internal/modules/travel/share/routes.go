package share

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/geo"
	"tripfolio/server/internal/modules/travel/itinerary"
)

// legPlan 是一段待算路的相邻路段：按行程顺序两两连接有坐标的项目，无坐标项目不打断相邻关系。
type legPlan struct {
	FromID, ToID uuid.UUID
	From, To     geo.Coordinate
}

// planLegs 从有序行程推导路段与无坐标项目数。
func planLegs(items []itinerary.Resource) ([]legPlan, int) {
	var legs []legPlan
	var prev *itinerary.Resource
	unlocated := 0
	for i := range items {
		it := &items[i]
		if it.Latitude == nil || it.Longitude == nil {
			unlocated++
			continue
		}
		if prev != nil {
			legs = append(legs, legPlan{
				FromID: prev.ID, ToID: it.ID,
				From: geo.Coordinate{Latitude: *prev.Latitude, Longitude: *prev.Longitude},
				To:   geo.Coordinate{Latitude: *it.Latitude, Longitude: *it.Longitude},
			})
		}
		prev = it
	}
	return legs, unlocated
}

// fingerprint 是有序路段（两端 ID 与 6 位小数坐标）的 SHA-256；行程一改指纹即变，整趟缓存自然失效。
func fingerprint(legs []legPlan) string {
	h := sha256.New()
	for _, l := range legs {
		fmt.Fprintf(h, "%s|%.6f|%.6f|%s|%.6f|%.6f\n", l.FromID, l.From.Latitude, l.From.Longitude, l.ToID, l.To.Latitude, l.To.Longitude)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// routeCache 是整趟路线结果的进程内缓存；键为 分享|模式|无坐标数|指纹，容量有上限。
type routeCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	cap     int
	entries map[string]routeEntry
}

type routeEntry struct {
	value   PublicRoutes
	expires time.Time
}

func newRouteCache(ttl time.Duration, capacity int) *routeCache {
	return &routeCache{ttl: ttl, cap: capacity, entries: map[string]routeEntry{}}
}

func (c *routeCache) get(key string, now time.Time) (PublicRoutes, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return PublicRoutes{}, false
	}
	if !now.Before(e.expires) {
		delete(c.entries, key)
		return PublicRoutes{}, false
	}
	return e.value, true
}

func (c *routeCache) put(key string, v PublicRoutes, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.cap {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		// 仍然满：整体丢弃，宁可多算几次也不让缓存无限增长。
		if len(c.entries) >= c.cap {
			c.entries = map[string]routeEntry{}
		}
	}
	c.entries[key] = routeEntry{value: v, expires: now.Add(c.ttl)}
}

// allItems 分页读全行程；访客页需要整趟数据。
func (s *Service) allItems(ctx context.Context, v Viewer) ([]itinerary.Resource, error) {
	const pageSize = 100
	var all []itinerary.Resource
	var after *itinerary.Position
	for {
		batch, err := s.d.Itinerary.List(ctx, v.AccountID, v.TripID, itinerary.ListQuery{Limit: pageSize, After: after})
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < pageSize {
			return all, nil
		}
		last := batch[len(batch)-1]
		after = &itinerary.Position{ScheduledOn: last.ScheduledOn, SortOrder: last.SortOrder, ID: last.ID}
	}
}

// Routes 计算整趟行程的相邻路段（设计 5.4）：访客没有坐标注入点；未配置、凭证错误或配额耗尽整体 503；
// 单段无路线或暂时性失败计入 failed_leg_count 不中断；全部成功才缓存，便于失败后重试恢复。
func (s *Service) Routes(ctx context.Context, v Viewer, mode geo.Mode) (PublicRoutes, error) {
	if !mode.Valid() {
		return PublicRoutes{}, apperr.Validation(apperr.Field("mode", "INVALID", "请选择驾车、步行或骑行"))
	}
	if s.d.Routes == nil {
		return PublicRoutes{}, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "地图服务尚未配置，请联系管理员配置高德凭证")
	}
	now := s.d.Clock.Now()
	if ok, wait := s.d.Limiter.Allow("share-routes|"+v.ShareID.String()+"|"+v.ClientIP, routesPerMinute, time.Minute, now); !ok {
		return PublicRoutes{}, apperr.RateLimited(wait)
	}
	items, err := s.allItems(ctx, v)
	if err != nil {
		return PublicRoutes{}, apperr.Internal(err)
	}
	legs, unlocated := planLegs(items)
	key := fmt.Sprintf("%s|%s|%d|%s", v.ShareID, mode, unlocated, fingerprint(legs))
	if cached, ok := s.routes.get(key, now); ok {
		return cached, nil
	}
	out := PublicRoutes{Mode: mode, Legs: []PublicLeg{}, UnlocatedItemCount: unlocated}
	limitKey := "share|" + v.ShareID.String()
	// 先试批量算路：驾车时整趟合成一次上游请求（geo.CalculateTripRoutes 按途经点分片），
	// 访客首次打开不必等 (段数-1) × 1.1 秒。不可批量或批量失败时整批放弃，
	// 退回下面的逐段循环，保持「单段失败只计 failed_leg_count」的既有语义。
	if batched, err := s.batchRoutes(ctx, limitKey, legs, mode); err == nil {
		out.Legs = make([]PublicLeg, 0, len(legs))
		for i, leg := range legs {
			out.Legs = append(out.Legs, PublicLeg{
				FromItemID:      leg.FromID,
				ToItemID:        leg.ToID,
				DistanceMeters:  batched[i].DistanceMeters,
				DurationSeconds: batched[i].DurationSeconds,
				Path:            batched[i].Path,
			})
		}
		s.routes.put(key, out, now)
		return out, nil
	} else if hardUnavailable(err) {
		return PublicRoutes{}, err
	}
	for _, leg := range legs {
		route, err := s.d.Routes.RouteAs(ctx, limitKey, leg.From, leg.To, mode)
		if err != nil {
			if _, ok := apperr.As(err); !ok {
				return PublicRoutes{}, apperr.Internal(err)
			}
			// 未配置、凭证错误或配额耗尽：503 且不带 Retry-After，继续请求只会重复失败，整体不可用，前端降级为示意连线。
			if hardUnavailable(err) {
				return PublicRoutes{}, err
			}
			out.FailedLegCount++
			continue
		}
		out.Legs = append(out.Legs, PublicLeg{FromItemID: leg.FromID, ToItemID: leg.ToID, DistanceMeters: route.DistanceMeters, DurationSeconds: route.DurationSeconds, Path: route.Path})
	}
	if out.FailedLegCount == 0 {
		s.routes.put(key, out, now)
	}
	return out, nil
}

// errBatchUnavailable 表示这一趟不适合批量算路（段数或坐标数超出批量接口的范围）。
var errBatchUnavailable = errors.New("share: 不具备批量算路条件")

// batchRoutes 把相邻路段合成一条有序坐标序列，用一次批量请求算完。
func (s *Service) batchRoutes(ctx context.Context, limitKey string, legs []legPlan, mode geo.Mode) ([]geo.Route, error) {
	if len(legs) == 0 || len(legs)+1 > geo.MaxTripRoutePoints {
		return nil, errBatchUnavailable
	}
	points := make([]geo.Coordinate, 0, len(legs)+1)
	points = append(points, legs[0].From)
	for _, leg := range legs {
		points = append(points, leg.To)
	}
	return s.d.Routes.RoutesAs(ctx, limitKey, points, mode)
}

// hardUnavailable 判断错误是否属于「继续请求也只会重复失败」的整体不可用：
// 未配置凭证、凭证或权限错误、配额耗尽（503 且不带 Retry-After）。
func hardUnavailable(err error) bool {
	e, ok := apperr.As(err)
	return ok && e.Status == 503 && e.Headers["Retry-After"] == ""
}
