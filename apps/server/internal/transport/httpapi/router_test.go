package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
