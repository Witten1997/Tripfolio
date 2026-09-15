package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	geoadapter "tripfolio/server/internal/adapters/geo"
	"tripfolio/server/internal/adapters/ratelimit"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/modules/geo"
	"tripfolio/server/internal/transport/httpapi"
)

func TestGeoHTTPContractAndErrors(t *testing.T) {
	response := `{"status":"1","pois":[{"name":"故宫","location":"116.397026,39.918058","address":[]}],"regeocode":{"formatted_address":"北京市东城区","addressComponent":{"district":"东城区"}},"route":{"paths":[{"distance":"1108","cost":{"duration":"886"},"steps":[{"polyline":"116.397026,39.918058;116.404269,39.914628"}]}]}}`
	var calls atomic.Int32
	var reply atomic.Value
	reply.Store(response)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, reply.Load().(string))
	}))
	defer upstream.Close()
	provider, _ := geoadapter.NewAmapClient(geoadapter.Config{Key: "secret", BaseURL: upstream.URL, PerAccountPerMinute: 2}, ratelimit.New())
	router := httpapi.NewRouter(httpapi.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Geo: geo.NewService(provider)})
	a := actor.Actor{AccountID: uuid.New(), AccountStatus: "active"}
	routeQuery := "/api/v1/geo/routes?origin_latitude=39.918058&origin_longitude=116.397026&destination_latitude=39.914628&destination_longitude=116.404269&mode=walking"
	request := func(path string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if authenticated {
			r = r.WithContext(actor.WithActor(r.Context(), a))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{routeQuery, "/api/v1/geo/places?q=park", "/api/v1/geo/reverse-geocode?latitude=39.9&longitude=116.4"} {
		if w := request(path, false); w.Code != 401 {
			t.Fatalf("地点接口须认证：%d", w.Code)
		}
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/api/v1/geo/places?q=", 422},
		{"/api/v1/geo/places?q=" + strings.Repeat("a", 51), 422},
		{"/api/v1/geo/places?q=park&latitude=39", 422},
		{"/api/v1/geo/reverse-geocode?latitude=NaN&longitude=116", 422},
		{"/api/v1/geo/reverse-geocode?latitude=91&longitude=116", 422},
		{"/api/v1/geo/reverse-geocode?latitude=39", 400},
		{strings.Replace(routeQuery, "walking", "flying", 1), 422},
	} {
		if w := request(tc.path, true); w.Code != tc.status {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatal("未认证和无效参数不得消耗高德配额")
	}
	if w := request(routeQuery, true); w.Code != 200 || !strings.Contains(w.Body.String(), `"distance_meters":1108`) {
		t.Fatalf("路线响应：%d %s", w.Code, w.Body.String())
	}
	if w := request("/api/v1/geo/places?q=park", true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w := request("/api/v1/geo/places?q=park", true)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("限流：%d %s", w.Code, w.Body.String())
	}

	a.AccountID = uuid.New()
	reply.Store(`{"status":"1","pois":[]}`)
	if w := request("/api/v1/geo/places?q=none", true); w.Code != 404 {
		t.Fatal(w.Body.String())
	}
	reply.Store(`{"status":"0","infocode":"10001","info":"INVALID_USER_KEY"}`)
	if w := request("/api/v1/geo/places?q=bad", true); w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Body.String())
	}
	router = httpapi.NewRouter(httpapi.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Geo: geo.NewService(nil)})
	w = request(routeQuery, true)
	if w.Code != 503 {
		t.Fatal("未配置应返回 503")
	}
	var problem map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil || problem["code"] != "DEPENDENCY_UNAVAILABLE" {
		t.Fatal(w.Body.String())
	}
}

func TestGeoCoordinateValidation(t *testing.T) {
	svc := geo.NewService(nil)
	a := actor.Actor{AccountID: uuid.New()}
	for _, point := range []geo.Coordinate{{Latitude: math.NaN()}, {Longitude: math.Inf(1)}, {Longitude: 181}, {Latitude: -91}} {
		if _, err := svc.Reverse(context.Background(), a, point); err == nil {
			t.Fatal("非法坐标通过校验")
		}
	}
}

func TestRouteHTTPAdvertisesOnlyRecoverableFailures(t *testing.T) {
	for _, tc := range []struct {
		infocode string
		status   int
		retry    string
	}{
		{"10020", 429, "1"}, {"10016", 503, "1"},
		{"10003", 503, ""}, {"10001", 503, ""}, {"20803", 404, ""},
	} {
		t.Run(tc.infocode, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"status":"0","infocode":"`+tc.infocode+`","info":"private-secret-key"}`)
			}))
			defer upstream.Close()
			provider, _ := geoadapter.NewAmapClient(geoadapter.Config{Key: "private-secret-key", BaseURL: upstream.URL, RouteInterval: time.Nanosecond}, nil)
			router := httpapi.NewRouter(httpapi.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Geo: geo.NewService(provider)})
			r := httptest.NewRequest(http.MethodGet, "/api/v1/geo/routes?origin_latitude=39.918058&origin_longitude=116.397026&destination_latitude=39.914628&destination_longitude=116.404269&mode=walking", nil)
			r = r.WithContext(actor.WithActor(r.Context(), actor.Actor{AccountID: uuid.New(), AccountStatus: "active"}))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Retry-After") != tc.retry {
				t.Fatalf("status=%d retry=%q body=%s", w.Code, w.Header().Get("Retry-After"), w.Body.String())
			}
			if strings.Contains(w.Body.String(), "private-secret-key") || strings.Contains(w.Body.String(), "infocode") {
				t.Fatal("provider internals leaked into HTTP response")
			}
		})
	}
}
