package geo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	geodata "tripfolio/server/internal/modules/geo"
)

var _ geodata.Provider = (*AmapClient)(nil)

const routeCacheTTL = 5 * time.Minute
const maxRouteCacheEntries = 256

type routeCacheEntry struct {
	route     geodata.Route
	expiresAt time.Time
}

// CalculateRoute 使用高德路径规划 2.0。只选择供应商返回的首条方案，不更改行程顺序。
func (c *AmapClient) CalculateRoute(ctx context.Context, accountKey string, origin, destination geodata.Coordinate, mode geodata.Mode) (geodata.Route, error) {
	if err := ctx.Err(); err != nil {
		return geodata.Route{}, err
	}
	if !origin.Valid() || !destination.Valid() || !mode.Valid() {
		return geodata.Route{}, errors.New("geo: 起终点或出行方式无效")
	}
	from := formatCoord(origin.Longitude) + "," + formatCoord(origin.Latitude)
	to := formatCoord(destination.Longitude) + "," + formatCoord(destination.Latitude)
	key := string(mode) + ":" + from + ">" + to
	if route, found := c.cachedRoute(key); found {
		return route, nil
	}
	if from == to {
		return geodata.Route{Mode: mode, Provider: "amap", Path: []geodata.Coordinate{origin, destination}}, nil
	}
	// 并发数不是 QPS：所有账号和模式共享该 Key 的门闩，实际请求平滑发起。
	// 不预留未来时隙，取消的排队者不会占用配额或把后续请求越推越远。
	select {
	case c.routeGate <- struct{}{}:
		defer func() { <-c.routeGate }()
	case <-ctx.Done():
		return geodata.Route{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return geodata.Route{}, err
	}
	// 同路段在途调用完成后直接复用缓存，避免重复消耗上游配额。
	if route, found := c.cachedRoute(key); found {
		return route, nil
	}
	if delay := time.Until(c.routeNext); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return geodata.Route{}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return geodata.Route{}, err
	}
	if err := c.admit(accountKey); err != nil {
		return geodata.Route{}, err
	}
	c.routeNext = time.Now().Add(c.routeInterval)
	endpoint := string(mode)
	if mode == geodata.Cycling {
		endpoint = "bicycling"
	}
	q := url.Values{"key": {c.key}, "origin": {from}, "destination": {to}, "show_fields": {"cost,polyline"}, "output": {"json"}}
	var body struct {
		Status   string `json:"status"`
		InfoCode string `json:"infocode"`
		Route    struct {
			Paths []struct {
				Distance flexString `json:"distance"`
				Duration flexString `json:"duration"`
				Cost     struct {
					Duration flexString `json:"duration"`
				} `json:"cost"`
				Steps []struct {
					Polyline flexString `json:"polyline"`
				} `json:"steps"`
			} `json:"paths"`
		} `json:"route"`
	}
	if err := c.get(ctx, "/v5/direction/"+endpoint, q, &body); err != nil {
		return geodata.Route{}, err
	}
	if body.Status != "1" {
		return geodata.Route{}, providerError(body.InfoCode)
	}
	if len(body.Route.Paths) == 0 {
		return geodata.Route{}, ErrNoResult
	}
	p := body.Route.Paths[0]
	distance, err := nonnegativeInteger(p.Distance.String())
	if err != nil {
		return geodata.Route{}, fmt.Errorf("高德路线距离: %w", err)
	}
	// 实际骑行响应在 paths[].duration，驾车和步行在 paths[].cost.duration。
	duration, err := nonnegativeInteger(firstNonEmpty(p.Cost.Duration.String(), p.Duration.String()))
	if err != nil {
		return geodata.Route{}, fmt.Errorf("高德路线时长: %w", err)
	}
	path := make([]geodata.Coordinate, 0)
	for _, step := range p.Steps {
		for _, raw := range strings.Split(step.Polyline.String(), ";") {
			if raw == "" {
				continue
			}
			lng, lat, ok := parseCoord(raw)
			if !ok {
				return geodata.Route{}, errors.New("高德路线含无效坐标")
			}
			point := geodata.Coordinate{Latitude: lat, Longitude: lng}
			if len(path) == 0 || path[len(path)-1] != point {
				path = append(path, point)
			}
		}
	}
	if len(path) < 2 {
		return geodata.Route{}, errors.New("高德未返回完整路线点")
	}
	route := geodata.Route{Mode: mode, DistanceMeters: distance, DurationSeconds: duration, Path: path, Provider: "amap"}
	c.cacheMu.Lock()
	if len(c.routes) >= maxRouteCacheEntries {
		var oldestKey string
		var oldest time.Time
		for k, e := range c.routes {
			if oldest.IsZero() || e.expiresAt.Before(oldest) {
				oldestKey, oldest = k, e.expiresAt
			}
		}
		delete(c.routes, oldestKey)
	}
	c.routes[key] = routeCacheEntry{route: cloneRoute(route), expiresAt: c.now().Add(routeCacheTTL)}
	c.cacheMu.Unlock()
	return route, nil
}

func (c *AmapClient) cachedRoute(key string) (geodata.Route, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	entry, found := c.routes[key]
	if found && !c.now().Before(entry.expiresAt) {
		delete(c.routes, key)
		found = false
	}
	if !found {
		return geodata.Route{}, false
	}
	return cloneRoute(entry.route), true
}

func nonnegativeInteger(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("缺少有效非负整数")
	}
	return n, nil
}

func cloneRoute(r geodata.Route) geodata.Route {
	r.Path = append([]geodata.Coordinate(nil), r.Path...)
	return r
}
