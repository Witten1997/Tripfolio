package geo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	geodata "tripfolio/server/internal/modules/geo"
)

var _ geodata.Provider = (*AmapClient)(nil)

const routeCacheTTL = 5 * time.Minute
const maxRouteCacheEntries = 256

// drivingWaypointLimit 是高德路径规划 2.0 驾车接口的途经点上限，也就是一次请求能算的段数。
// 一片请求覆盖 drivingWaypointLimit 段，需要 drivingWaypointLimit+1 个坐标。
const drivingWaypointLimit = 16

// errNoStepDetail 表示上游没有返回可归属到各段的步骤级距离与时长；调用方应退回逐段算路，
// 宁可慢也不给分段数字做估算。
var errNoStepDetail = errors.New("geo: 高德未返回分段可用的步骤明细")

type routeCacheEntry struct {
	route     geodata.Route
	expiresAt time.Time
}

// routeRequest 是一次路径规划请求：单段算路没有途经点，批量算路按高德上限给出有序途经点。
type routeRequest struct {
	origin      string
	destination string
	waypoints   []string
}

// routeStep 是上游返回的一步；start 是这一步在合并路径中的起点下标。
type routeStep struct {
	start    int
	distance int64
	duration int64
}

// parsedRoute 是一次上游请求解析出的整条路线：总距离与总时长取自 paths[0]，
// steps 用于把整条路线按途经点切回各段。
type parsedRoute struct {
	distance int64
	duration int64
	path     []geodata.Coordinate
	steps    []routeStep
}

// CalculateRoute 使用高德路径规划 2.0。只选择供应商返回的首条方案，不更改行程顺序。
func (c *AmapClient) CalculateRoute(ctx context.Context, accountKey string, origin, destination geodata.Coordinate, mode geodata.Mode) (geodata.Route, error) {
	if err := ctx.Err(); err != nil {
		return geodata.Route{}, err
	}
	if !origin.Valid() || !destination.Valid() || !mode.Valid() {
		return geodata.Route{}, errors.New("geo: 起终点或出行方式无效")
	}
	from := coordString(origin)
	to := coordString(destination)
	key := routeKey(mode, from, to)
	if route, found := c.cachedRoute(key); found {
		return route, nil
	}
	if from == to {
		return geodata.Route{Mode: mode, Provider: "amap", Path: []geodata.Coordinate{origin, destination}}, nil
	}
	// 并发数不是 QPS：所有账号和模式共享该 Key 的门闩，实际请求平滑发起。
	// 不预留未来时隙，取消的排队者不会占用配额或把后续请求越推越远。
	release, err := c.acquireRouteGate(ctx)
	if err != nil {
		return geodata.Route{}, err
	}
	defer release()
	// 同路段在途调用完成后直接复用缓存，避免重复消耗上游配额。
	if route, found := c.cachedRoute(key); found {
		return route, nil
	}
	if err := c.admitRouteRequest(ctx, accountKey); err != nil {
		return geodata.Route{}, err
	}
	parsed, err := c.requestRoute(ctx, mode, routeRequest{origin: from, destination: to})
	if err != nil {
		return geodata.Route{}, err
	}
	route := parsed.route(mode)
	c.storeRoute(key, route)
	return route, nil
}

// CalculateTripRoutes 一次算出有序坐标的相邻路段（长度为坐标数减一）。
//
// 驾车使用高德路径规划 2.0 的途经点能力：每 16 段（17 个坐标）合并为一次上游请求，
// 单次返回的整条路线按步骤归属切回各段，因此等待时间与段数无关；步行与骑行逐段计算（上游不支持途经点）。
// 命中缓存的段不发起请求；某一片失败时只让那一片退回逐段算路，其余片不受影响。
func (c *AmapClient) CalculateTripRoutes(ctx context.Context, accountKey string, points []geodata.Coordinate, mode geodata.Mode) ([]geodata.Route, error) {
	if len(points) < 2 {
		return nil, errors.New("geo: 批量算路至少需要两个坐标")
	}
	if len(points) > geodata.MaxTripRoutePoints {
		return nil, errors.New("geo: 批量算路坐标过多")
	}
	for _, point := range points {
		if !point.Valid() {
			return nil, errors.New("geo: 坐标无效")
		}
	}
	if !mode.Valid() {
		return nil, errors.New("geo: 出行方式无效")
	}
	legCount := len(points) - 1
	routes := make([]geodata.Route, legCount)
	pending := make([]bool, legCount)
	for i := 0; i < legCount; i++ {
		from, to := coordString(points[i]), coordString(points[i+1])
		if route, found := c.cachedRoute(routeKey(mode, from, to)); found {
			routes[i] = route
			continue
		}
		// 同一坐标的相邻点不消耗上游配额，与 CalculateRoute 的短路保持一致。
		if from == to {
			routes[i] = geodata.Route{Mode: mode, Provider: "amap", Path: []geodata.Coordinate{points[i], points[i+1]}}
			continue
		}
		pending[i] = true
	}
	if mode != geodata.Driving {
		return c.fillLegs(ctx, accountKey, points, mode, routes, pending, 0, legCount)
	}
	for start := 0; start < legCount; {
		end := min(start+drivingWaypointLimit, legCount)
		if !pendingWithin(pending[start:end]) {
			start = end
			continue
		}
		chunk, err := c.drivingLegs(ctx, accountKey, points[start:end+1])
		if err != nil {
			// 一片失败不影响其余：这一片退回逐段算路，每段仍可单独降级。
			if _, err := c.fillLegs(ctx, accountKey, points, mode, routes, pending, start, end); err != nil {
				return nil, err
			}
			start = end
			continue
		}
		copy(routes[start:end], chunk)
		start = end
	}
	return routes, nil
}

// fillLegs 逐段算路，填充 [start, end) 内仍待请求的段。
func (c *AmapClient) fillLegs(ctx context.Context, accountKey string, points []geodata.Coordinate, mode geodata.Mode, routes []geodata.Route, pending []bool, start, end int) ([]geodata.Route, error) {
	for i := start; i < end; i++ {
		if !pending[i] {
			continue
		}
		route, err := c.CalculateRoute(ctx, accountKey, points[i], points[i+1], mode)
		if err != nil {
			return nil, err
		}
		routes[i] = route
	}
	return routes, nil
}

// drivingLegs 用一次途经点请求算出这一片的所有路段，并写入各段缓存。
func (c *AmapClient) drivingLegs(ctx context.Context, accountKey string, points []geodata.Coordinate) ([]geodata.Route, error) {
	release, err := c.acquireRouteGate(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	// 同片在途调用完成后整片命中缓存，不再重复请求。
	if legs, ok := c.cachedLegs(points, geodata.Driving); ok {
		return legs, nil
	}
	if err := c.admitRouteRequest(ctx, accountKey); err != nil {
		return nil, err
	}
	request := routeRequest{origin: coordString(points[0]), destination: coordString(points[len(points)-1])}
	for _, point := range points[1 : len(points)-1] {
		request.waypoints = append(request.waypoints, coordString(point))
	}
	parsed, err := c.requestRoute(ctx, geodata.Driving, request)
	if err != nil {
		return nil, err
	}
	legs, err := attributeLegs(parsed, points)
	if err != nil {
		return nil, err
	}
	for i := range legs {
		c.storeRoute(routeKey(geodata.Driving, coordString(points[i]), coordString(points[i+1])), legs[i])
	}
	return legs, nil
}

// cachedLegs 在整片都命中缓存时返回各段，否则返回 false。
func (c *AmapClient) cachedLegs(points []geodata.Coordinate, mode geodata.Mode) ([]geodata.Route, bool) {
	legs := make([]geodata.Route, len(points)-1)
	for i := range legs {
		route, found := c.cachedRoute(routeKey(mode, coordString(points[i]), coordString(points[i+1])))
		if !found {
			return nil, false
		}
		legs[i] = route
	}
	return legs, true
}

// attributeLegs 把一次途经点请求返回的整条路线切回各段。
//
// 分段点取每个途经点在合并路径上的最近顶点（路线本身经过这些点），再把每一步按起点下标归入所在段：
// 各段的距离与时长是各步之和，仍是高德口径；几何取合并路径在分段点之间的切片，段与段首尾相接。
// 上游没有返回步骤级距离与时长、或某一段切分不出有效内容时返回 errNoStepDetail，由调用方退回逐段算路。
func attributeLegs(parsed parsedRoute, points []geodata.Coordinate) ([]geodata.Route, error) {
	legCount := len(points) - 1
	if legCount < 1 || len(parsed.path) < 2 || len(parsed.steps) == 0 {
		return nil, errNoStepDetail
	}
	bounds := make([]int, legCount+1)
	bounds[0] = 0
	bounds[legCount] = len(parsed.path) - 1
	for i := 1; i < legCount; i++ {
		index, ok := nearestVertex(parsed.path, points[i], bounds[i-1])
		if !ok {
			return nil, errNoStepDetail
		}
		bounds[i] = index
	}
	distance := make([]int64, legCount)
	duration := make([]int64, legCount)
	for _, step := range parsed.steps {
		index := legAt(bounds, step.start)
		distance[index] += step.distance
		duration[index] += step.duration
	}
	out := make([]geodata.Route, legCount)
	for i := range out {
		if distance[i] <= 0 || duration[i] <= 0 {
			return nil, errNoStepDetail
		}
		path := append([]geodata.Coordinate(nil), parsed.path[bounds[i]:bounds[i+1]+1]...)
		if len(path) < 2 {
			return nil, errNoStepDetail
		}
		out[i] = geodata.Route{
			Mode:            geodata.Driving,
			DistanceMeters:  distance[i],
			DurationSeconds: duration[i],
			Path:            path,
			Provider:        "amap",
		}
	}
	return out, nil
}

// nearestVertex 从 from 起找路径上离目标最近的顶点；下标单调不减，保证分段点顺序与途经点一致。
func nearestVertex(path []geodata.Coordinate, target geodata.Coordinate, from int) (int, bool) {
	best, bestScore := -1, math.Inf(1)
	for i := max(from, 0); i < len(path); i++ {
		score := coordinateDistance(target, path[i])
		if score < bestScore {
			best, bestScore = i, score
		}
	}
	return best, best >= 0
}

// legAt 返回下标落在哪一段：bounds[i] <= index < bounds[i+1]。
func legAt(bounds []int, index int) int {
	leg := 0
	for i := 1; i < len(bounds)-1; i++ {
		if index >= bounds[i] {
			leg = i
		}
	}
	return leg
}

// acquireRouteGate 串行化算路请求。调用方持有门闩后先复查缓存，
// 只有确实要请求上游时才等待发起时隙并消耗配额。
func (c *AmapClient) acquireRouteGate(ctx context.Context) (func(), error) {
	select {
	case c.routeGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-c.routeGate }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// admitRouteRequest 在持有 routeGate 时调用。不预留未来时隙，取消的等待者
// 不占用配额，也不把后续请求越推越远。
func (c *AmapClient) admitRouteRequest(ctx context.Context, accountKey string) error {
	if delay := time.Until(c.routeNext); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.admit(accountKey); err != nil {
		return err
	}
	c.routeNext = time.Now().Add(c.routeInterval)
	return nil
}

// requestRoute 发起一次路径规划请求并解析出整条路线。
func (c *AmapClient) requestRoute(ctx context.Context, mode geodata.Mode, request routeRequest) (parsedRoute, error) {
	endpoint := string(mode)
	if mode == geodata.Cycling {
		endpoint = "bicycling"
	}
	q := url.Values{"key": {c.key}, "origin": {request.origin}, "destination": {request.destination}, "show_fields": {"cost,polyline"}, "output": {"json"}}
	if len(request.waypoints) > 0 {
		q.Set("waypoints", strings.Join(request.waypoints, ";"))
	}
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
					// 2.0 的步骤距离／时长在不同版本文档里出现过两种字段名，都接受。
					Distance     flexString `json:"distance"`
					StepDistance flexString `json:"step_distance"`
					Duration     flexString `json:"duration"`
					Cost         struct {
						Duration flexString `json:"duration"`
					} `json:"cost"`
				} `json:"steps"`
			} `json:"paths"`
		} `json:"route"`
	}
	if err := c.get(ctx, "/v5/direction/"+endpoint, q, &body); err != nil {
		return parsedRoute{}, err
	}
	if body.Status != "1" {
		return parsedRoute{}, providerError(body.InfoCode)
	}
	if len(body.Route.Paths) == 0 {
		return parsedRoute{}, ErrNoResult
	}
	p := body.Route.Paths[0]
	distance, err := nonnegativeInteger(p.Distance.String())
	if err != nil {
		return parsedRoute{}, fmt.Errorf("高德路线距离: %w", err)
	}
	// 实际骑行响应在 paths[].duration，驾车和步行在 paths[].cost.duration。
	duration, err := nonnegativeInteger(firstNonEmpty(p.Cost.Duration.String(), p.Duration.String()))
	if err != nil {
		return parsedRoute{}, fmt.Errorf("高德路线时长: %w", err)
	}
	parsed := parsedRoute{distance: distance, duration: duration}
	for _, step := range p.Steps {
		start := len(parsed.path)
		firstPoint := true
		added := 0
		for _, raw := range strings.Split(step.Polyline.String(), ";") {
			if raw == "" {
				continue
			}
			lng, lat, ok := parseCoord(raw)
			if !ok {
				return parsedRoute{}, errors.New("高德路线含无效坐标")
			}
			point := geodata.Coordinate{Latitude: lat, Longitude: lng}
			if firstPoint {
				if len(parsed.path) > 0 && parsed.path[len(parsed.path)-1] == point {
					start = len(parsed.path) - 1
				}
				firstPoint = false
			}
			if len(parsed.path) == 0 || parsed.path[len(parsed.path)-1] != point {
				parsed.path = append(parsed.path, point)
				added++
			}
		}
		if added == 0 {
			continue
		}
		stepDistance, err := optionalInteger(firstNonEmpty(step.StepDistance.String(), step.Distance.String()))
		if err != nil {
			return parsedRoute{}, fmt.Errorf("高德步骤距离: %w", err)
		}
		stepDuration, err := optionalInteger(firstNonEmpty(step.Cost.Duration.String(), step.Duration.String()))
		if err != nil {
			return parsedRoute{}, fmt.Errorf("高德步骤时长: %w", err)
		}
		parsed.steps = append(parsed.steps, routeStep{start: start, distance: stepDistance, duration: stepDuration})
	}
	if len(parsed.path) < 2 {
		return parsedRoute{}, errors.New("高德未返回完整路线点")
	}
	return parsed, nil
}

// route 把整条路线当作一段返回（单段算路用）。
func (p parsedRoute) route(mode geodata.Mode) geodata.Route {
	return geodata.Route{
		Mode:            mode,
		DistanceMeters:  p.distance,
		DurationSeconds: p.duration,
		Path:            p.path,
		Provider:        "amap",
	}
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

func (c *AmapClient) storeRoute(key string, route geodata.Route) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
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
}

// routeKey 是路段缓存键：模式 + 有序坐标，与上游请求参数一一对应。
func routeKey(mode geodata.Mode, from, to string) string {
	return string(mode) + ":" + from + ">" + to
}

// coordString 按高德要求把坐标写成 "经度,纬度"，最多 6 位小数。
func coordString(point geodata.Coordinate) string {
	return formatCoord(point.Longitude) + "," + formatCoord(point.Latitude)
}

func pendingWithin(pending []bool) bool {
	for _, need := range pending {
		if need {
			return true
		}
	}
	return false
}

// coordinateDistance 返回两个坐标之间的角距离平方（只用于比较远近，不需要开方）。
func coordinateDistance(a, b geodata.Coordinate) float64 {
	const rad = math.Pi / 180
	dLat := (b.Latitude - a.Latitude) * rad / 2
	dLng := (b.Longitude - a.Longitude) * rad / 2
	return dLat*dLat + math.Cos(a.Latitude*rad)*math.Cos(b.Latitude*rad)*dLng*dLng
}

func nonnegativeInteger(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("缺少有效非负整数")
	}
	return n, nil
}

// optionalInteger 允许字段缺失（按 0 处理），用于步骤级明细。
func optionalInteger(s string) (int64, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	return nonnegativeInteger(strings.TrimSpace(s))
}

func cloneRoute(r geodata.Route) geodata.Route {
	r.Path = append([]geodata.Coordinate(nil), r.Path...)
	return r
}
