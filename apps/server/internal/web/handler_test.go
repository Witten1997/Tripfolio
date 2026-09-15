package web

import (
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testAssets() fs.FS {
	return fstest.MapFS{
		"index.html":             {Data: []byte("<div id=app></div>")},
		"assets/index-abc123.js": {Data: []byte("console.log(1)")},
		"favicon.ico":            {Data: []byte("icon")},
		// go:embed all:dist 会把 dist 下的点文件一起嵌进来，它们不该被 HTTP 暴露。
		".gitignore": {Data: []byte("构建期文件")},
	}
}

func newTestHandler(t *testing.T, o Options) http.Handler {
	t.Helper()
	if o.Assets == nil {
		o.Assets = testAssets()
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	h, err := NewHandler(o)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func get(t *testing.T, h http.Handler, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStaticAndFallback(t *testing.T) {
	h := newTestHandler(t, Options{})

	t.Run("入口页不缓存", func(t *testing.T) {
		rec := get(t, h, "/", nil)
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != noCache {
			t.Fatalf("状态 %d 缓存 %q", rec.Code, rec.Header().Get("Cache-Control"))
		}
	})

	t.Run("哈希资源长缓存", func(t *testing.T) {
		rec := get(t, h, "/assets/index-abc123.js", nil)
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != immutableCache {
			t.Fatalf("状态 %d 缓存 %q", rec.Code, rec.Header().Get("Cache-Control"))
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Fatalf("Content-Type = %q", ct)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("缺少 nosniff")
		}
	})

	t.Run("前端路由回落到入口页", func(t *testing.T) {
		rec := get(t, h, "/s/share-token", nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id=app`) {
			t.Fatalf("状态 %d 正文 %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("缺失的哈希资源返回 404 而不是 HTML", func(t *testing.T) {
		rec := get(t, h, "/assets/gone.js", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("状态 %d", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "id=app") {
			t.Fatal("不应回落到入口页")
		}
	})

	t.Run("其他静态文件直出", func(t *testing.T) {
		rec := get(t, h, "/favicon.ico", nil)
		if rec.Code != http.StatusOK || rec.Body.String() != "icon" {
			t.Fatalf("状态 %d 正文 %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("点文件一律 404，不回落到入口页", func(t *testing.T) {
		for _, path := range []string{"/.gitignore", "/.env", "/assets/.hidden.js"} {
			rec := get(t, h, path, nil)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s 状态 = %d，期望 404", path, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "构建期文件") {
				t.Errorf("%s 泄露了点文件内容", path)
			}
		}
	})
}

func TestMissingFrontend(t *testing.T) {
	h := newTestHandler(t, Options{Assets: fstest.MapFS{}})
	rec := get(t, h, "/", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "前端产物") {
		t.Fatalf("说明页正文 %q", rec.Body.String())
	}
	// 接口与探针不经过这里，缺前端时空资源请求仍是 404。
	if rec := get(t, h, "/assets/x.js", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("静态资源状态 %d", rec.Code)
	}
}

func TestAMapProxy(t *testing.T) {
	var got struct {
		path   string
		query  string
		host   string
		cookie string
		auth   string
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.query, got.host = r.URL.Path, r.URL.RawQuery, r.Host
		got.cookie, got.auth = r.Header.Get("Cookie"), r.Header.Get("Authorization")
		_, _ = w.Write([]byte("tile"))
	}))
	defer upstream.Close()

	h := newTestHandler(t, Options{
		JSCode: "server-secret",
		Upstreams: []Upstream{
			{Prefix: mountPrefix + "/v4/map/styles", Target: upstream.URL},
			{Prefix: mountPrefix + "/", Target: upstream.URL},
		},
	})
	rec := get(t, h, "/_AMapService/v4/map/styles?styleid=7&jscode=client-forged", map[string]string{
		"Cookie":        "session=1",
		"Authorization": "Bearer token",
	})
	if rec.Code != http.StatusOK || rec.Body.String() != "tile" {
		t.Fatalf("状态 %d 正文 %q", rec.Code, rec.Body.String())
	}
	if got.path != "/v4/map/styles" {
		t.Fatalf("上游路径 = %q", got.path)
	}
	if !strings.Contains(got.query, "styleid=7") {
		t.Fatalf("其余查询参数被丢弃：%q", got.query)
	}
	if !strings.Contains(got.query, "jscode=server-secret") || strings.Contains(got.query, "client-forged") {
		t.Fatalf("jscode 未被服务端覆盖：%q", got.query)
	}
	if got.cookie != "" || got.auth != "" {
		t.Fatalf("不应转发本站凭证：cookie=%q auth=%q", got.cookie, got.auth)
	}
	if want := strings.TrimPrefix(upstream.URL, "http://"); got.host != want {
		t.Fatalf("上游 Host = %q，期望 %q", got.host, want)
	}
}

func TestAMapProxyUnavailable(t *testing.T) {
	h := newTestHandler(t, Options{})
	rec := get(t, h, "/_AMapService/v3/vectormap?x=1", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("未配置密钥应返回 503，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "TRIPFOLIO_AMAP_JSCODE") {
		t.Fatalf("提示未包含变量名：%q", rec.Body.String())
	}
}

func TestAMapProxyRejectsUnknownPath(t *testing.T) {
	h := newTestHandler(t, Options{JSCode: "k", Upstreams: []Upstream{{Prefix: mountPrefix + "/v3/vectormap", Target: "https://example.invalid"}}})
	if rec := get(t, h, "/_AMapService/other", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("未命中规则应 404，实际 %d", rec.Code)
	}
}
