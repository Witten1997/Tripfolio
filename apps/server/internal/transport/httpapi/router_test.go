package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/transport/httpapi"
)

type readinessFunc func(context.Context) error

func (f readinessFunc) Check(ctx context.Context) error { return f(ctx) }

func newTestRouter(t *testing.T, ready error) http.Handler {
	t.Helper()
	return httpapi.NewRouter(httpapi.Deps{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metadata:    metadata.Current(),
		Readiness:   readinessFunc(func(context.Context) error { return ready }),
		CORSOrigins: []string{"https://localhost"},
	})
}

// webStub 代替内嵌前端：单二进制部署下未匹配的路径都由它接管。
func webStub() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/_AMapService/") {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("proxied"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<div id=app></div>"))
	})
}

func TestWebHandlerServesFrontendAndKeepsAPIContract(t *testing.T) {
	router := httpapi.NewRouter(httpapi.Deps{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metadata:    metadata.Current(),
		Readiness:   readinessFunc(func(context.Context) error { return nil }),
		CORSOrigins: []string{"https://localhost"},
		Web:         webStub(),
	})

	cases := []struct {
		path        string
		wantStatus  int
		wantContent string
		wantType    string
	}{
		{"/", http.StatusOK, "id=app", "text/html"},
		{"/s/share-token", http.StatusOK, "id=app", "text/html"},
		{"/_AMapService/v3/vectormap", http.StatusOK, "proxied", "text/plain"},
		{"/api/v1/does-not-exist", http.StatusNotFound, "RESOURCE_NOT_FOUND", "application/problem+json"},
		{"/api/unknown", http.StatusNotFound, "RESOURCE_NOT_FOUND", "application/problem+json"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.wantStatus {
			t.Errorf("%s 状态 = %d，期望 %d", tc.path, rec.Code, tc.wantStatus)
			continue
		}
		if !strings.Contains(rec.Body.String(), tc.wantContent) {
			t.Errorf("%s 正文 = %q，未包含 %q", tc.path, rec.Body.String(), tc.wantContent)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, tc.wantType) {
			t.Errorf("%s Content-Type = %q，期望包含 %q", tc.path, ct, tc.wantType)
		}
	}
}

func TestWithoutWebHandlerUnknownPathIsProblemJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/somewhere", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("状态 = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestHealthLive(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHealthReadyReflectsDependency(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	newTestRouter(t, errors.New("db down")).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want 503", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("code = %v", body["code"])
	}
}

func TestGetMetadata(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(t, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/metadata", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing X-Request-ID header")
	}
	var body struct {
		Data struct {
			ProtocolVersion     int    `json:"protocol_version"`
			DefaultCurrencyCode string `json:"default_currency_code"`
			Currencies          []struct {
				Code       string `json:"code"`
				MinorUnits int    `json:"minor_units"`
			} `json:"currencies"`
			Map struct {
				Provider         string `json:"provider"`
				CoordinateSystem string `json:"coordinate_system"`
			} `json:"map"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.ProtocolVersion != 1 || body.Data.DefaultCurrencyCode != "CNY" || len(body.Data.Currencies) != 21 {
		t.Fatalf("unexpected metadata: %+v", body.Data)
	}
	if body.Data.Map.Provider != "amap" || body.Data.Map.CoordinateSystem != "GCJ-02" {
		t.Fatalf("unexpected map settings: %+v", body.Data.Map)
	}
}

func TestUnknownRouteAndMethodUseProblemJSON(t *testing.T) {
	cases := []struct {
		method, path string
		wantStatus   int
		wantCode     string
	}{
		{http.MethodGet, "/api/v1/nope", http.StatusNotFound, "RESOURCE_NOT_FOUND"},
		{http.MethodGet, "/nope", http.StatusNotFound, "RESOURCE_NOT_FOUND"},
		{http.MethodDelete, "/api/v1/metadata", http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		newTestRouter(t, nil).ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.wantStatus {
			t.Errorf("%s %s: status = %d, want %d", c.method, c.path, rec.Code, c.wantStatus)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json; charset=utf-8" {
			t.Errorf("%s %s: Content-Type = %q", c.method, c.path, ct)
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["code"] != c.wantCode {
			t.Errorf("%s %s: code = %v, want %s", c.method, c.path, body["code"], c.wantCode)
		}
		if body["request_id"] == "" || body["request_id"] == nil {
			t.Errorf("%s %s: request_id missing", c.method, c.path)
		}
	}
}

func TestSharePathsDoNotRequireBearerButRequireShareToken(t *testing.T) {
	r := newTestRouter(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/trip", nil)
	req.Header.Set("Authorization", "Share t000000000000000000001")
	r.ServeHTTP(rec, req)
	// 未装配 Sessions 与 Shares 的测试路由：不因缺 Bearer 返回 401 AUTH_REQUIRED，而是被分享中间件按未启用拒绝。
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Fatalf("X-Robots-Tag = %q", rec.Header().Get("X-Robots-Tag"))
	}
}

func TestCompressionAppliesToTextResponses(t *testing.T) {
	router := httpapi.NewRouter(httpapi.Deps{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metadata:    metadata.Current(),
		Readiness:   readinessFunc(func(context.Context) error { return nil }),
		CORSOrigins: []string{"https://localhost"},
		Web:         webStub(),
	})

	// 单二进制不再有 Caddy 做压缩：页面与 JSON 接口都要 gzip。
	for _, path := range []string{"/", "/health/live"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
			t.Errorf("%s 的 Content-Encoding = %q，期望 gzip", path, enc)
		}
	}

	// 客户端没有声明 gzip 时保持原文。
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("未声明 gzip 时不应压缩：Content-Encoding = %q", enc)
	}
}
