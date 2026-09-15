package geo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	geodata "tripfolio/server/internal/modules/geo"
)

var from = geodata.Coordinate{Latitude: 39.918058, Longitude: 116.397026}
var to = geodata.Coordinate{Latitude: 39.914628, Longitude: 116.404269}

func routeJSON(cost string) string {
	return `{"status":"1","route":{"paths":[{"distance":"1108",` + cost + `,"steps":[{"polyline":"116.397026,39.918058;116.4,39.916"},{"polyline":"116.4,39.916;116.404269,39.914628"}]}]}}`
}

func TestRouteModesAndRealDurationFormats(t *testing.T) {
	for _, tc := range []struct {
		mode           geodata.Mode
		endpoint, cost string
	}{
		{geodata.Driving, "driving", `"cost":{"duration":"886"}`},
		{geodata.Walking, "walking", `"cost":{"duration":"886"}`},
		{geodata.Cycling, "bicycling", `"duration":886`},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			stub := newStubAmap(t, func(path string, q url.Values) string {
				if path != "/v5/direction/"+tc.endpoint || q.Get("show_fields") != "cost,polyline" {
					t.Error("算路路径或返回字段不正确")
				}
				if q.Get("origin") != "116.397026,39.918058" || q.Get("destination") != "116.404269,39.914628" {
					t.Error("起终点顺序错误")
				}
				return routeJSON(tc.cost)
			})
			route, err := newTestClient(t, stub, nil).CalculateRoute(context.Background(), "a", from, to, tc.mode)
			if err != nil {
				t.Fatal(err)
			}
			if route.DistanceMeters != 1108 || route.DurationSeconds != 886 || len(route.Path) != 3 || route.Path[0] != from || route.Mode != tc.mode {
				t.Fatalf("路线解析错误：%+v", route)
			}
		})
	}
}

func TestRouteCacheKeysExpiryAndIsolation(t *testing.T) {
	stub := newStubAmap(t, func(string, url.Values) string { return routeJSON(`"cost":{"duration":"886"}`) })
	now := time.Now()
	client := newTestClient(t, stub, func(c *Config) { c.Now = func() time.Time { return now } })
	route, err := client.CalculateRoute(context.Background(), "a", from, to, geodata.Walking)
	if err != nil {
		t.Fatal(err)
	}
	route.Path[0].Latitude = 0
	cached, err := client.CalculateRoute(context.Background(), "b", from, to, geodata.Walking)
	if err != nil || stub.calls.Load() != 1 || cached.Path[0] != from {
		t.Fatal("缓存未命中或被调用方修改")
	}
	_, _ = client.CalculateRoute(context.Background(), "a", to, from, geodata.Walking)
	_, _ = client.CalculateRoute(context.Background(), "a", from, to, geodata.Driving)
	if stub.calls.Load() != 3 {
		t.Fatal("起终点方向和出行方式必须参与缓存键")
	}
	now = now.Add(routeCacheTTL)
	_, _ = client.CalculateRoute(context.Background(), "a", from, to, geodata.Walking)
	if stub.calls.Load() != 4 {
		t.Fatal("过期时刻必须失效")
	}
}

func TestRouteRejectsInvalidAndDoesNotInventDistance(t *testing.T) {
	for _, body := range []string{
		`{"status":"1","route":{"paths":[]}}`,
		`{"status":"0","infocode":"20801"}`,
		routeJSON(`"cost":{"duration":"-1"}`),
		strings.Replace(routeJSON(`"cost":{"duration":"886"}`), `"1108"`, `"unknown"`, 1),
		strings.ReplaceAll(routeJSON(`"cost":{"duration":"886"}`), "116.4,39.916", "NaN,39.916"),
		`{"status":"1","route":{"paths":[{"distance":"1108","cost":{"duration":"886"},"steps":[]}]}}`,
	} {
		t.Run(body[:20], func(t *testing.T) {
			stub := newStubAmap(t, func(string, url.Values) string { return body })
			if _, err := newTestClient(t, stub, nil).CalculateRoute(context.Background(), "a", from, to, geodata.Walking); err == nil {
				t.Fatal("无结果或畸形响应不能伪造成功")
			}
		})
	}
	stub := newStubAmap(t, func(string, url.Values) string { return "" })
	client := newTestClient(t, stub, nil)
	for _, point := range []geodata.Coordinate{{Latitude: math.NaN()}, {Longitude: math.Inf(1)}, {Latitude: 91}} {
		if _, err := client.CalculateRoute(context.Background(), "a", point, to, geodata.Walking); err == nil {
			t.Fatal("非法坐标应被拒绝")
		}
	}
	route, err := client.CalculateRoute(context.Background(), "a", from, from, geodata.Walking)
	if err != nil || route.DistanceMeters != 0 || route.DurationSeconds != 0 || len(route.Path) != 2 {
		t.Fatal("相同坐标应为零路程")
	}
	if stub.calls.Load() != 0 {
		t.Fatal("非法坐标和相同坐标无需上游调用")
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed")
}

func TestUpstreamNetworkErrorDoesNotLeakKey(t *testing.T) {
	client, _ := NewAmapClient(Config{Key: "private-secret-key", HTTPClient: &http.Client{Transport: failingTransport{}}}, nil)
	_, err := client.CalculateRoute(context.Background(), "a", from, to, geodata.Walking)
	if err == nil || strings.Contains(err.Error(), "private-secret-key") || strings.Contains(err.Error(), "key=") {
		t.Fatalf("错误应隐藏凭证：%v", err)
	}
}

// 默认跳过，仅显式提供测试 Key 时调用真实高德，每次共 5 次请求。
func TestLiveAmap(t *testing.T) {
	key := os.Getenv("TRIPFOLIO_TEST_AMAP_KEY")
	if key == "" {
		t.Skip("未提供真实高德测试 Key")
	}
	client, err := NewAmapClient(Config{Key: key, Timeout: 12 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	places, err := client.SearchPlaces(ctx, "live", "故宫", nil, nil, "北京")
	if err != nil || len(places) == 0 {
		t.Fatalf("真实 POI 搜索失败：%v", err)
	}
	place, err := client.ReverseGeocode(ctx, "live", from.Latitude, from.Longitude)
	if err != nil || place.Address == "" {
		t.Fatalf("真实逆地理编码失败：%v", err)
	}
	for _, mode := range []geodata.Mode{geodata.Driving, geodata.Walking, geodata.Cycling} {
		route, err := client.CalculateRoute(ctx, "live", from, to, mode)
		if err != nil {
			t.Fatal(err)
		}
		if route.DistanceMeters <= 0 || route.DurationSeconds <= 0 || len(route.Path) < 2 {
			t.Fatal("真实路线不完整")
		}
		t.Log(fmt.Sprintf("%s: %d 米 / %d 秒 / %d 个路线点", mode, route.DistanceMeters, route.DurationSeconds, len(route.Path)))
	}
}

// 六个近邻坐标、五段路线的有界只读联调，不读取或修改用户行程。
func TestLiveAmapSixPointRoute(t *testing.T) {
	key := os.Getenv("TRIPFOLIO_TEST_AMAP_KEY")
	if key == "" {
		t.Skip("未提供真实高德测试 Key")
	}
	client, err := NewAmapClient(Config{Key: key, Timeout: 12 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	points := []geodata.Coordinate{from, to,
		{Latitude: 39.925, Longitude: 116.404},
		{Latitude: 39.933, Longitude: 116.397},
		{Latitude: 39.934, Longitude: 116.387},
		{Latitude: 39.94, Longitude: 116.387},
	}
	for i := 1; i < len(points); i++ {
		route, err := client.CalculateRoute(context.Background(), "live-six", points[i-1], points[i], geodata.Walking)
		if err != nil {
			t.Fatalf("第 %d 段: %v", i, err)
		}
		if len(route.Path) < 2 || route.DistanceMeters <= 0 {
			t.Fatalf("第 %d 段没有道路结果", i)
		}
		t.Logf("第 %d 段: %d 米 / %d 秒", i, route.DistanceMeters, route.DurationSeconds)
	}
}
