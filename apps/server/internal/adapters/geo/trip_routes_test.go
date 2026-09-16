package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	geodata "tripfolio/server/internal/modules/geo"
)

// tripStub 按 origin/waypoints/destination 生成一跳一步的驾车响应；detail 为 false 时不返回
// 步骤级距离与时长（模拟上游只给整条路线的总量）。
func tripStub(t *testing.T, calls *int, detail bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*calls)++
		query := r.URL.Query()
		hops := []string{query.Get("origin")}
		if waypoints := query.Get("waypoints"); waypoints != "" {
			hops = append(hops, strings.Split(waypoints, ";")...)
		}
		hops = append(hops, query.Get("destination"))
		steps := make([]map[string]any, 0, len(hops)-1)
		for i := 0; i+1 < len(hops); i++ {
			step := map[string]any{"polyline": hops[i] + ";" + hops[i+1]}
			if detail {
				step["step_distance"] = "1000"
				step["cost"] = map[string]any{"duration": "200"}
			}
			steps = append(steps, step)
		}
		body := map[string]any{
			"status": "1",
			"route": map[string]any{"paths": []any{map[string]any{
				"distance": fmt.Sprintf("%d", (len(hops)-1)*1000),
				"duration": fmt.Sprintf("%d", (len(hops)-1)*200),
				"cost":     map[string]any{"duration": fmt.Sprintf("%d", (len(hops)-1)*200)},
				"steps":    steps,
			}}},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("写入响应: %v", err)
		}
	}))
}

func tripTestClient(t *testing.T, server *httptest.Server) *AmapClient {
	t.Helper()
	client, err := NewAmapClient(Config{Key: "test", BaseURL: server.URL, RouteInterval: time.Nanosecond, HTTPClient: server.Client()}, nil)
	if err != nil {
		t.Fatalf("创建客户端: %v", err)
	}
	return client
}

func stripedPoints(count int) []geodata.Coordinate {
	points := make([]geodata.Coordinate, 0, count)
	for i := 0; i < count; i++ {
		points = append(points, geodata.Coordinate{Latitude: 39, Longitude: 116 + float64(i)*0.1})
	}
	return points
}

// 驾车批量算路：整趟一次途经点请求，返回的整条路线按步骤切回各段，各段之和等于总量。
func TestCalculateTripRoutesBatchesDrivingAndSplitsSteps(t *testing.T) {
	calls := 0
	server := tripStub(t, &calls, true)
	defer server.Close()
	routes, err := tripTestClient(t, server).CalculateTripRoutes(context.Background(), "account", stripedPoints(4), geodata.Driving)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("4 个坐标应只请求一次上游，实际 %d 次", calls)
	}
	if len(routes) != 3 {
		t.Fatalf("应为 3 段: %+v", routes)
	}
	var total int64
	for i, route := range routes {
		if route.DistanceMeters != 1000 || route.DurationSeconds != 200 || len(route.Path) != 2 {
			t.Fatalf("第 %d 段切分不符: %+v", i, route)
		}
		total += route.DistanceMeters
	}
	if total != 3000 {
		t.Fatalf("各段距离之和应等于整条路线: %d", total)
	}
}

// 超过途经点上限（16 段）时按片请求：17 段需要两次上游请求，每段仍是高德步骤之和。
func TestCalculateTripRoutesChunksBeyondWaypointLimit(t *testing.T) {
	calls := 0
	server := tripStub(t, &calls, true)
	defer server.Close()
	routes, err := tripTestClient(t, server).CalculateTripRoutes(context.Background(), "account", stripedPoints(18), geodata.Driving)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("17 段应按 16+1 分两片，实际 %d 次", calls)
	}
	if len(routes) != 17 {
		t.Fatalf("应为 17 段: %d", len(routes))
	}
	for i, route := range routes {
		if route.DistanceMeters != 1000 || route.DurationSeconds != 200 {
			t.Fatalf("第 %d 段不符: %+v", i, route)
		}
	}
}

// 上游没有步骤级明细时整批放弃、退回逐段算路，数字仍取整条路线的口径，不做估算。
func TestCalculateTripRoutesFallsBackWhenStepsHaveNoDetail(t *testing.T) {
	calls := 0
	server := tripStub(t, &calls, false)
	defer server.Close()
	routes, err := tripTestClient(t, server).CalculateTripRoutes(context.Background(), "account", stripedPoints(4), geodata.Driving)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("批量 1 次失败后应逐段 3 次，实际 %d 次", calls)
	}
	for i, route := range routes {
		if route.DistanceMeters != 1000 || route.DurationSeconds != 200 {
			t.Fatalf("第 %d 段应取逐段结果: %+v", i, route)
		}
	}
}
