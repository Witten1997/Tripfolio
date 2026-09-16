package geo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
)

type Service struct{ provider Provider }

func NewService(provider Provider) *Service { return &Service{provider: provider} }

func (s *Service) Reverse(ctx context.Context, a actor.Actor, point Coordinate) (Place, error) {
	if !point.Valid() {
		return Place{}, invalid("latitude", "请提供有效的经纬度")
	}
	if s.provider == nil {
		return Place{}, unavailable()
	}
	place, err := s.provider.ReverseGeocode(ctx, a.AccountID.String(), point.Latitude, point.Longitude)
	return place, mapError(err)
}

func (s *Service) Search(ctx context.Context, a actor.Actor, q, city string, lat, lng *float64) ([]Place, error) {
	q, city = strings.TrimSpace(q), strings.TrimSpace(city)
	if n := utf8.RuneCountInString(q); n < 1 || n > 50 {
		return nil, invalid("q", "搜索关键词须为 1–50 个字符")
	}
	if utf8.RuneCountInString(city) > 40 {
		return nil, invalid("city", "城市名最多 40 个字符")
	}
	if (lat == nil) != (lng == nil) {
		return nil, invalid("latitude", "经纬度须同时提供")
	}
	if lat != nil && !(Coordinate{Latitude: *lat, Longitude: *lng}).Valid() {
		return nil, invalid("latitude", "请提供有效的经纬度")
	}
	if s.provider == nil {
		return nil, unavailable()
	}
	places, err := s.provider.SearchPlaces(ctx, a.AccountID.String(), q, lat, lng, city)
	return places, mapError(err)
}

// Route 以账号 ID 作限流键算路。
func (s *Service) Route(ctx context.Context, a actor.Actor, origin, destination Coordinate, mode Mode) (Route, error) {
	return s.RouteAs(ctx, a.AccountID.String(), origin, destination, mode)
}

// RouteAs 以调用方指定的限流键算路，供分享访客等非账号身份使用；校验与错误映射与 Route 相同。
func (s *Service) RouteAs(ctx context.Context, limitKey string, origin, destination Coordinate, mode Mode) (Route, error) {
	if !origin.Valid() {
		return Route{}, invalid("origin_latitude", "起点经纬度无效")
	}
	if !destination.Valid() {
		return Route{}, invalid("destination_latitude", "终点经纬度无效")
	}
	if !mode.Valid() {
		return Route{}, invalid("mode", "请选择驾车、步行或骑行")
	}
	if s.provider == nil {
		return Route{}, unavailable()
	}
	route, err := s.provider.CalculateRoute(ctx, limitKey, origin, destination, mode)
	return route, mapError(err)
}

// Routes 一次算出整趟行程的相邻路段，以账号 ID 作限流键。
func (s *Service) Routes(ctx context.Context, a actor.Actor, points []Coordinate, mode Mode) ([]Route, error) {
	return s.RoutesAs(ctx, a.AccountID.String(), points, mode)
}

// RoutesAs 以调用方指定的限流键批量算路，供分享访客等非账号身份使用；校验与错误映射与 Route 相同。
// 返回的路段与 points 的相邻关系一一对应（长度为 len(points)-1）。
func (s *Service) RoutesAs(ctx context.Context, limitKey string, points []Coordinate, mode Mode) ([]Route, error) {
	if len(points) < 2 || len(points) > MaxTripRoutePoints {
		return nil, invalid("points", fmt.Sprintf("请提供 2–%d 个坐标", MaxTripRoutePoints))
	}
	for _, point := range points {
		if !point.Valid() {
			return nil, invalid("points", "坐标无效")
		}
	}
	if !mode.Valid() {
		return nil, invalid("mode", "请选择驾车、步行或骑行")
	}
	if s.provider == nil {
		return nil, unavailable()
	}
	routes, err := s.provider.CalculateTripRoutes(ctx, limitKey, points, mode)
	return routes, mapError(err)
}

func invalid(field, message string) error {
	return apperr.Validation(apperr.Field(field, "INVALID", message))
}

func unavailable() error {
	return apperr.New(503, "DEPENDENCY_UNAVAILABLE", "地图服务尚未配置，请联系管理员配置高德凭证").WithCause(ErrConfiguration)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNoResult) {
		return apperr.NotFound()
	}
	if errors.Is(err, ErrQuotaExceeded) {
		return apperr.New(503, "DEPENDENCY_UNAVAILABLE", "地图服务配额已用尽，请检查配额或稍后再试").WithCause(err)
	}
	if errors.Is(err, ErrConfiguration) {
		return apperr.New(503, "DEPENDENCY_UNAVAILABLE", "地图服务凭证或权限不可用，请检查高德服务配置").WithCause(err)
	}
	var limited interface{ RetryAfter() time.Duration }
	if errors.As(err, &limited) {
		return apperr.RateLimited(limited.RetryAfter()).WithCause(err)
	}
	problem := apperr.Dependency(err)
	problem.Detail = "地图服务暂时繁忙，稍后可重试"
	problem.Headers = map[string]string{"Retry-After": "1"}
	var temporary interface{ TemporaryRetryAfter() time.Duration }
	if errors.As(err, &temporary) {
		seconds := max(1, int(math.Ceil(temporary.TemporaryRetryAfter().Seconds())))
		problem.Headers["Retry-After"] = strconv.Itoa(seconds)
	}
	return problem
}
