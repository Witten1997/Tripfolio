package geo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubAmap 起一个假高德，记录调用次数与最后一次查询参数。
type stubAmap struct {
	server  *httptest.Server
	calls   atomic.Int32
	lastQry url.Values
}

func newStubAmap(t *testing.T, handler func(path string, q url.Values) string) *stubAmap {
	t.Helper()
	s := &stubAmap{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		s.lastQry = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(handler(r.URL.Path, r.URL.Query())))
	}))
	t.Cleanup(s.server.Close)
	return s
}

func newTestClient(t *testing.T, stub *stubAmap, mutate func(*Config)) *AmapClient {
	t.Helper()
	cfg := Config{Key: "test-key", BaseURL: stub.server.URL, Timeout: 2 * time.Second}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := NewAmapClient(cfg, nil)
	if err != nil {
		t.Fatalf("创建客户端：%v", err)
	}
	return c
}

const regeoOK = `{
  "status": "1", "info": "OK", "infocode": "10000",
  "regeocode": {
    "formatted_address": "北京市海淀区燕园街道北京大学",
    "addressComponent": {
      "province": "北京市", "city": [], "district": "海淀区",
      "township": "燕园街道", "adcode": "110108",
      "building": {"name": [], "type": []},
      "neighborhood": {"name": "北京大学", "type": "科教文化服务"}
    }
  }
}`

// 高德在字段为空时返回 []，直接用 string 解析会失败。
func TestReverseGeocodeHandlesAmapEmptyArrayFields(t *testing.T) {
	stub := newStubAmap(t, func(string, url.Values) string { return regeoOK })
	client := newTestClient(t, stub, nil)

	place, err := client.ReverseGeocode(context.Background(), "acct", 39.98871, 116.30576)
	if err != nil {
		t.Fatalf("逆地理编码：%v", err)
	}
	if place.Address != "北京市海淀区燕园街道北京大学" {
		t.Errorf("地址不符：%q", place.Address)
	}
	// city 为 []（直辖市），不能变成 "[]" 之类的字面量。
	if strings.Contains(place.Address, "[") || strings.Contains(place.Name, "[") {
		t.Errorf("空数组字段泄漏到结果：name=%q address=%q", place.Name, place.Address)
	}
	// building.name 为空时退到社区名。
	if place.Name != "北京大学" {
		t.Errorf("地点名应退到社区名，得到 %q", place.Name)
	}
	if place.Adcode != "110108" {
		t.Errorf("adcode 不符：%q", place.Adcode)
	}
	if place.Provider != "amap" {
		t.Errorf("provider 应为 amap，得到 %q", place.Provider)
	}
	// 坐标原样回传，不被上游改写。
	if place.Latitude != 39.98871 || place.Longitude != 116.30576 {
		t.Errorf("坐标被改写：%f,%f", place.Latitude, place.Longitude)
	}
}

// 逆地理编码按坐标缓存，约 100 米内的坐标共用一次上游调用。
func TestReverseGeocodeCachesByRoundedCoordinate(t *testing.T) {
	stub := newStubAmap(t, func(string, url.Values) string { return regeoOK })
	client := newTestClient(t, stub, nil)
	ctx := context.Background()

	if _, err := client.ReverseGeocode(ctx, "acct", 39.98871, 116.30576); err != nil {
		t.Fatal(err)
	}
	// 第四位小数的差异落在同一个缓存键里。
	if _, err := client.ReverseGeocode(ctx, "acct", 39.98874, 116.30573); err != nil {
		t.Fatal(err)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("邻近坐标应命中缓存，上游被调用 %d 次", got)
	}

	// 明显不同的坐标不应命中。
	if _, err := client.ReverseGeocode(ctx, "acct", 31.2304, 121.4737); err != nil {
		t.Fatal(err)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("不同坐标应重新请求，上游被调用 %d 次", got)
	}
}

func TestReverseGeocodeCacheExpires(t *testing.T) {
	stub := newStubAmap(t, func(string, url.Values) string { return regeoOK })
	now := time.Now()
	client := newTestClient(t, stub, func(c *Config) {
		c.CacheTTL = time.Hour
		c.Now = func() time.Time { return now }
	})
	ctx := context.Background()

	if _, err := client.ReverseGeocode(ctx, "acct", 39.9, 116.3); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := client.ReverseGeocode(ctx, "acct", 39.9, 116.3); err != nil {
		t.Fatal(err)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Errorf("缓存过期后应重新请求，上游被调用 %d 次", got)
	}
}

// 境外坐标返回空地址，按无结果处理，并且无结果同样缓存以免反复打上游。
func TestReverseGeocodeEmptyAddressIsNoResultAndCached(t *testing.T) {
	const empty = `{"status":"1","info":"OK","infocode":"10000",
		"regeocode":{"formatted_address":[],"addressComponent":{"province":[],"city":[],"district":[],"adcode":[]}}}`
	stub := newStubAmap(t, func(string, url.Values) string { return empty })
	client := newTestClient(t, stub, nil)
	ctx := context.Background()

	if _, err := client.ReverseGeocode(ctx, "acct", 48.8584, 2.2945); !errors.Is(err, ErrNoResult) {
		t.Errorf("空地址应返回 ErrNoResult，得到 %v", err)
	}
	if _, err := client.ReverseGeocode(ctx, "acct", 48.8584, 2.2945); !errors.Is(err, ErrNoResult) {
		t.Errorf("第二次也应是 ErrNoResult，得到 %v", err)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("无结果应被缓存，上游被调用 %d 次", got)
	}
}

func TestReverseGeocodeUpstreamFailureStatus(t *testing.T) {
	const failed = `{"status":"0","info":"INVALID_USER_KEY","infocode":"10001"}`
	stub := newStubAmap(t, func(string, url.Values) string { return failed })
	client := newTestClient(t, stub, nil)

	_, err := client.ReverseGeocode(context.Background(), "acct", 39.9, 116.3)
	if err == nil {
		t.Fatal("status=0 应返回错误")
	}
	if errors.Is(err, ErrNoResult) {
		t.Error("上游失败不应被当成无结果")
	}
	if !strings.Contains(err.Error(), "10001") {
		t.Errorf("错误应带上 infocode 便于排查：%v", err)
	}
}

// 坐标按经度在前、纬度在后传给高德，且最多 6 位小数。
func TestReverseGeocodeSendsLngLatOrderWithSixDecimals(t *testing.T) {
	stub := newStubAmap(t, func(string, url.Values) string { return regeoOK })
	client := newTestClient(t, stub, nil)

	if _, err := client.ReverseGeocode(context.Background(), "acct", 39.123456789, 116.987654321); err != nil {
		t.Fatal(err)
	}
	got := stub.lastQry.Get("location")
	if got != "116.987654,39.123457" {
		t.Errorf("location 应为经度在前且 6 位小数，得到 %q", got)
	}
}

const tipsOK = `{
  "status": "1", "info": "OK", "infocode": "10000",
  "tips": [
    {"id":"B1","name":"故宫博物院","district":"北京市东城区","address":"景山前街4号","location":"116.397026,39.918058","adcode":"110101"},
    {"id":"B2","name":"故宫角楼","district":"北京市东城区","address":[],"location":"116.401,39.921","adcode":"110101"},
    {"id":"L1","name":"1路公交","district":[],"address":[],"location":[],"adcode":[]}
  ]
}`

// 无坐标的条目（公交线路）对选点无用，必须被剔除。
func TestSearchPlacesDropsEntriesWithoutCoordinates(t *testing.T) {
	stub := newStubAmap(t, func(string, url.Values) string { return tipsOK })
	client := newTestClient(t, stub, nil)

	places, err := client.SearchPlaces(context.Background(), "acct", "故宫", nil, nil, "")
	if err != nil {
		t.Fatalf("地点搜索：%v", err)
	}
	if len(places) != 2 {
		t.Fatalf("应剔除无坐标条目，得到 %d 条：%+v", len(places), places)
	}
	if places[0].Name != "故宫博物院" || places[0].Address != "景山前街4号" {
		t.Errorf("第一条不符：%+v", places[0])
	}
	if places[0].Latitude != 39.918058 || places[0].Longitude != 116.397026 {
		t.Errorf("坐标解析错误（应经度在前）：%+v", places[0])
	}
	// address 为空时退到 district。
	if places[1].Address != "北京市东城区" {
		t.Errorf("空 address 应退到 district，得到 %q", places[1].Address)
	}
}

func TestSearchPlacesNoResult(t *testing.T) {
	const emptyTips = `{"status":"1","info":"OK","infocode":"10000","tips":[]}`
	stub := newStubAmap(t, func(string, url.Values) string { return emptyTips })
	client := newTestClient(t, stub, nil)

	if _, err := client.SearchPlaces(context.Background(), "acct", "不存在的地方", nil, nil, ""); !errors.Is(err, ErrNoResult) {
		t.Errorf("空结果应返回 ErrNoResult，得到 %v", err)
	}
}

// location 只在 city 非空时对高德生效，因此只有两者都给出才发送。
func TestSearchPlacesSendsLocationOnlyWithCity(t *testing.T) {
	stub := newStubAmap(t, func(string, url.Values) string { return tipsOK })
	client := newTestClient(t, stub, nil)
	lat, lng := 39.9, 116.4
	ctx := context.Background()

	if _, err := client.SearchPlaces(ctx, "acct", "故宫", &lat, &lng, ""); err != nil {
		t.Fatal(err)
	}
	if stub.lastQry.Get("location") != "" {
		t.Errorf("无 city 时不应发送 location，得到 %q", stub.lastQry.Get("location"))
	}

	if _, err := client.SearchPlaces(ctx, "acct", "故宫", &lat, &lng, "110000"); err != nil {
		t.Fatal(err)
	}
	if stub.lastQry.Get("location") != "116.4,39.9" {
		t.Errorf("有 city 时应发送 location，得到 %q", stub.lastQry.Get("location"))
	}
	if stub.lastQry.Get("city") != "110000" {
		t.Errorf("city 未透传：%q", stub.lastQry.Get("city"))
	}
}

func TestSearchPlacesRejectsEmptyKeyword(t *testing.T) {
	stub := newStubAmap(t, func(string, url.Values) string { return tipsOK })
	client := newTestClient(t, stub, nil)

	if _, err := client.SearchPlaces(context.Background(), "acct", "   ", nil, nil, ""); err == nil {
		t.Error("空关键词应被拒绝")
	}
	if stub.calls.Load() != 0 {
		t.Error("空关键词不应打到上游")
	}
}

// 限流器拒绝时返回可换算 Retry-After 的错误，且不打上游。
type denyLimiter struct{}

func (denyLimiter) Allow(string, int, time.Duration, time.Time) (bool, time.Duration) {
	return false, 30 * time.Second
}

func TestPerAccountRateLimitBlocksUpstream(t *testing.T) {
	stub := newStubAmap(t, func(string, url.Values) string { return regeoOK })
	client, err := NewAmapClient(Config{Key: "k", BaseURL: stub.server.URL}, denyLimiter{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.ReverseGeocode(context.Background(), "acct", 39.9, 116.3)
	if err == nil {
		t.Fatal("限流时应返回错误")
	}
	retryAfter, ok := RateLimitedError(err)
	if !ok {
		t.Fatalf("应可识别为限流错误：%v", err)
	}
	if retryAfter <= 0 {
		t.Errorf("重试时长应为正：%v", retryAfter)
	}
	if stub.calls.Load() != 0 {
		t.Error("限流后不应打到上游")
	}
}

// 全局日上限保护免费配额，跨天后重置。
func TestGlobalDailyLimitResetsNextDay(t *testing.T) {
	stub := newStubAmap(t, func(string, url.Values) string { return tipsOK })
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	client := newTestClient(t, stub, func(c *Config) {
		c.GlobalDailyLimit = 2
		c.Now = func() time.Time { return now }
	})
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := client.SearchPlaces(ctx, "acct", "故宫", nil, nil, ""); err != nil {
			t.Fatalf("第 %d 次应成功：%v", i+1, err)
		}
	}
	if _, err := client.SearchPlaces(ctx, "acct", "故宫", nil, nil, ""); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("超出日上限应返回 ErrQuotaExceeded，得到 %v", err)
	}
	if stub.calls.Load() != 2 {
		t.Errorf("超限后不应打到上游，上游被调用 %d 次", stub.calls.Load())
	}

	now = now.Add(24 * time.Hour)
	if _, err := client.SearchPlaces(ctx, "acct", "故宫", nil, nil, ""); err != nil {
		t.Errorf("跨天后应重置配额：%v", err)
	}
}

func TestNewAmapClientRequiresKey(t *testing.T) {
	if _, err := NewAmapClient(Config{}, nil); err == nil {
		t.Error("缺少 key 应返回错误")
	}
}

func TestUpstreamErrorStatusIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	client, err := NewAmapClient(Config{Key: "k", BaseURL: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.ReverseGeocode(context.Background(), "acct", 39.9, 116.3); err == nil {
		t.Error("上游 502 应返回错误")
	}
}
