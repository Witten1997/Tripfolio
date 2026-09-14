// Package geo 代理高德 Web 服务 API：逆地理编码与地点搜索。
// 服务端持有 Web 服务 key，客户端不直连高德（技术选型第 12 节）。
//
// 三条保护措施都在这一层：按账号限流、逆地理编码按坐标缓存、全局日上限保护免费配额。
// 坐标一律是 GCJ-02，与业务存储一致；结果不落库，由客户端确认后写入业务记录。
package geo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNoResult 表示高德返回空结果（例如境外坐标）。
// 传输层映射为 404 RESOURCE_NOT_FOUND，客户端按「无结果」处理并允许手填。
var ErrNoResult = errors.New("地点服务无结果")

// ErrQuotaExceeded 表示触达服务端设置的全局日上限，用于保护高德免费配额。
// 传输层映射为 503 DEPENDENCY_UNAVAILABLE。
var ErrQuotaExceeded = errors.New("地点服务日配额已用尽")

// Place 是统一的地点模型，对应接口设计 3.10 的 GeoPlace。
type Place struct {
	Name      string
	Address   string
	Latitude  float64
	Longitude float64
	Adcode    string
	Provider  string
}

// Limiter 是按键固定窗口限流器，由 adapters/ratelimit 提供实现。
type Limiter interface {
	Allow(key string, limit int, window time.Duration, now time.Time) (bool, time.Duration)
}

// Config 是高德代理配置。
type Config struct {
	// Key 是高德 Web 服务 API key，只在服务端使用。
	Key string
	// BaseURL 默认 https://restapi.amap.com，测试可指向本地桩。
	BaseURL string
	// Timeout 是单次上游请求超时，超时返回依赖不可用。
	Timeout time.Duration
	// PerAccountPerMinute 是每账号每分钟上限，0 表示用默认值 60。
	PerAccountPerMinute int
	// GlobalDailyLimit 是全局日调用上限，0 表示不限制。
	GlobalDailyLimit int
	// CacheTTL 是逆地理编码缓存时长，0 表示用默认值 24 小时。
	CacheTTL time.Duration
	// HTTPClient 可注入自定义客户端；为 nil 时按 Timeout 构造。
	HTTPClient *http.Client
	// Now 可注入时钟，便于测试缓存过期与限流窗口。
	Now func() time.Time
}

// AmapClient 是高德 Web 服务 API 客户端。
type AmapClient struct {
	key        string
	baseURL    string
	httpClient *http.Client
	limiter    Limiter
	perAccount int
	now        func() time.Time

	cacheTTL time.Duration
	cacheMu  sync.Mutex
	cache    map[string]cacheEntry

	dailyLimit int
	dailyMu    sync.Mutex
	dailyDate  string
	dailyCount int
}

type cacheEntry struct {
	place     Place
	noResult  bool
	expiresAt time.Time
}

// NewAmapClient 创建高德客户端。limiter 为 nil 时不做按账号限流（仅测试场景）。
func NewAmapClient(cfg Config, limiter Limiter) (*AmapClient, error) {
	if strings.TrimSpace(cfg.Key) == "" {
		return nil, errors.New("geo: 高德 Web 服务 key 不能为空")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://restapi.amap.com"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	perAccount := cfg.PerAccountPerMinute
	if perAccount <= 0 {
		perAccount = 60
	}
	ttl := cfg.CacheTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &AmapClient{
		key:        cfg.Key,
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: client,
		limiter:    limiter,
		perAccount: perAccount,
		now:        now,
		cacheTTL:   ttl,
		cache:      map[string]cacheEntry{},
		dailyLimit: cfg.GlobalDailyLimit,
	}, nil
}

// ReverseGeocode 把 GCJ-02 坐标转为地点名与地址。
// 结果按坐标四舍五入到 3 位小数（约 100 米）缓存，命中缓存不计入限流与配额。
func (c *AmapClient) ReverseGeocode(ctx context.Context, accountKey string, lat, lng float64) (Place, error) {
	cacheKey := coordCacheKey(lat, lng)
	if entry, ok := c.lookupCache(cacheKey); ok {
		if entry.noResult {
			return Place{}, ErrNoResult
		}
		return entry.place, nil
	}

	if err := c.admit(accountKey); err != nil {
		return Place{}, err
	}

	q := url.Values{}
	q.Set("key", c.key)
	q.Set("location", formatCoord(lng)+","+formatCoord(lat))
	q.Set("extensions", "base")
	q.Set("output", "json")

	var body struct {
		Status   string `json:"status"`
		Info     string `json:"info"`
		InfoCode string `json:"infocode"`
		Regeo    struct {
			FormattedAddress flexString `json:"formatted_address"`
			AddressComponent struct {
				Province flexString `json:"province"`
				City     flexString `json:"city"`
				District flexString `json:"district"`
				Township flexString `json:"township"`
				Adcode   flexString `json:"adcode"`
				Building struct {
					Name flexString `json:"name"`
				} `json:"building"`
				Neighborhood struct {
					Name flexString `json:"name"`
				} `json:"neighborhood"`
			} `json:"addressComponent"`
		} `json:"regeocode"`
	}
	if err := c.get(ctx, "/v3/geocode/regeo", q, &body); err != nil {
		return Place{}, err
	}
	if body.Status != "1" {
		return Place{}, fmt.Errorf("高德逆地理编码失败：info=%s infocode=%s", body.Info, body.InfoCode)
	}

	comp := body.Regeo.AddressComponent
	address := body.Regeo.FormattedAddress.String()
	// 境外坐标与无覆盖区域返回空地址，按无结果处理。
	if address == "" {
		c.storeCache(cacheKey, cacheEntry{noResult: true, expiresAt: c.now().Add(c.cacheTTL)})
		return Place{}, ErrNoResult
	}

	// 地点名优先取最具体的一层：楼宇 → 社区 → 街道 → 区。
	name := firstNonEmpty(
		comp.Building.Name.String(),
		comp.Neighborhood.Name.String(),
		comp.Township.String(),
		comp.District.String(),
	)
	place := Place{
		Name:      name,
		Address:   address,
		Latitude:  lat,
		Longitude: lng,
		Adcode:    comp.Adcode.String(),
		Provider:  "amap",
	}
	c.storeCache(cacheKey, cacheEntry{place: place, expiresAt: c.now().Add(c.cacheTTL)})
	return place, nil
}

// SearchPlaces 按关键词搜索地点，最多返回 20 条。
// 搜索类调用的免费配额很小（5000 次/月），因此不缓存但同样受限流与日上限约束。
func (c *AmapClient) SearchPlaces(ctx context.Context, accountKey, keyword string, lat, lng *float64, city string) ([]Place, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, errors.New("geo: 搜索关键词不能为空")
	}
	if err := c.admit(accountKey); err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("key", c.key)
	q.Set("keywords", keyword)
	q.Set("datatype", "poi")
	q.Set("output", "json")
	if city != "" {
		q.Set("city", city)
	}
	// location 只在 city 不为空时生效（高德文档），因此两者都有才带上。
	if lat != nil && lng != nil && city != "" {
		q.Set("location", formatCoord(*lng)+","+formatCoord(*lat))
	}

	var body struct {
		Status   string `json:"status"`
		Info     string `json:"info"`
		InfoCode string `json:"infocode"`
		Tips     []struct {
			Name     flexString `json:"name"`
			District flexString `json:"district"`
			Address  flexString `json:"address"`
			Location flexString `json:"location"`
			Adcode   flexString `json:"adcode"`
		} `json:"tips"`
	}
	if err := c.get(ctx, "/v3/assistant/inputtips", q, &body); err != nil {
		return nil, err
	}
	if body.Status != "1" {
		return nil, fmt.Errorf("高德地点搜索失败：info=%s infocode=%s", body.Info, body.InfoCode)
	}

	places := make([]Place, 0, len(body.Tips))
	for _, tip := range body.Tips {
		// 公交线路等条目不返回坐标，选点用不上，直接丢弃。
		lng, lat, ok := parseCoord(tip.Location.String())
		if !ok {
			continue
		}
		name := tip.Name.String()
		if name == "" {
			continue
		}
		places = append(places, Place{
			Name:      name,
			Address:   firstNonEmpty(tip.Address.String(), tip.District.String()),
			Latitude:  lat,
			Longitude: lng,
			Adcode:    tip.Adcode.String(),
			Provider:  "amap",
		})
		if len(places) == 20 {
			break
		}
	}
	if len(places) == 0 {
		return nil, ErrNoResult
	}
	return places, nil
}

// admit 依次检查按账号限流与全局日上限。
func (c *AmapClient) admit(accountKey string) error {
	if c.limiter != nil && accountKey != "" {
		if ok, _ := c.limiter.Allow("geo:"+accountKey, c.perAccount, time.Minute, c.now()); !ok {
			// 限流的重试时长由传输层换算为 Retry-After。
			return errRateLimited{retryAfter: time.Minute}
		}
	}
	if c.dailyLimit > 0 {
		c.dailyMu.Lock()
		defer c.dailyMu.Unlock()
		today := c.now().UTC().Format(time.DateOnly)
		if c.dailyDate != today {
			c.dailyDate = today
			c.dailyCount = 0
		}
		if c.dailyCount >= c.dailyLimit {
			return ErrQuotaExceeded
		}
		c.dailyCount++
	}
	return nil
}

// errRateLimited 让传输层拿到重试时长而不必导入 apperr。
type errRateLimited struct{ retryAfter time.Duration }

func (e errRateLimited) Error() string { return "地点服务请求过于频繁" }

// RetryAfter 供传输层构造 429 响应。
func (e errRateLimited) RetryAfter() time.Duration { return e.retryAfter }

// RateLimitedError 判断错误是否为限流，并返回建议的重试时长。
func RateLimitedError(err error) (time.Duration, bool) {
	var e errRateLimited
	if errors.As(err, &e) {
		return e.retryAfter, true
	}
	return 0, false
}

func (c *AmapClient) lookupCache(key string) (cacheEntry, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	entry, ok := c.cache[key]
	if !ok || c.now().After(entry.expiresAt) {
		return cacheEntry{}, false
	}
	return entry, true
}

func (c *AmapClient) storeCache(key string, entry cacheEntry) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	// 顺带清理过期项，避免长期运行后无界增长。
	if len(c.cache) > 4096 {
		now := c.now()
		for k, v := range c.cache {
			if now.After(v.expiresAt) {
				delete(c.cache, k)
			}
		}
	}
	c.cache[key] = entry
}

// get 发起上游请求。网络错误、超时与非 2xx 都返回错误，由传输层映射为 503。
func (c *AmapClient) get(ctx context.Context, path string, q url.Values, out any) error {
	endpoint := c.baseURL + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("构造高德请求: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求高德: %w", err)
	}
	defer resp.Body.Close()

	// 上游响应体限长，避免异常响应占用内存。
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("读取高德响应: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("高德返回状态 %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("解析高德响应: %w", err)
	}
	return nil
}

// flexString 兼容高德的空值表示：字符串字段为空时返回 []，不是 ""。
type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	switch {
	case len(data) == 0, bytes.Equal(data, []byte("null")):
		*s = ""
		return nil
	case data[0] == '[':
		// 空数组代表空值；非空数组取第一个字符串元素。
		var items []any
		if err := json.Unmarshal(data, &items); err != nil {
			return err
		}
		if len(items) == 0 {
			*s = ""
			return nil
		}
		if str, ok := items[0].(string); ok {
			*s = flexString(str)
			return nil
		}
		*s = ""
		return nil
	case data[0] == '"':
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		*s = flexString(str)
		return nil
	default:
		// 数字等其他标量按原样保留，例如 adcode 偶尔是数字。
		*s = flexString(data)
		return nil
	}
}

// String 返回去空白后的值。
func (s flexString) String() string { return strings.TrimSpace(string(s)) }

// coordCacheKey 把坐标四舍五入到 3 位小数（约 100 米）作为缓存键。
func coordCacheKey(lat, lng float64) string {
	return fmt.Sprintf("%.3f,%.3f", lat, lng)
}

// formatCoord 按高德要求输出最多 6 位小数。
func formatCoord(v float64) string {
	return strconv.FormatFloat(round6(v), 'f', -1, 64)
}

func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// parseCoord 解析高德的 "经度,纬度" 字符串。
func parseCoord(s string) (lng, lat float64, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false
	}
	lngStr, latStr, found := strings.Cut(s, ",")
	if !found {
		return 0, 0, false
	}
	lng, err := strconv.ParseFloat(strings.TrimSpace(lngStr), 64)
	if err != nil {
		return 0, 0, false
	}
	lat, err = strconv.ParseFloat(strings.TrimSpace(latStr), 64)
	if err != nil {
		return 0, 0, false
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, false
	}
	return lng, lat, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
