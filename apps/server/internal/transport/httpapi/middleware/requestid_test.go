package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tripfolio/server/internal/transport/httpapi/middleware"
)

func TestRequestIDEchoesValidClientValue(t *testing.T) {
	var seen string
	h := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = middleware.RequestIDFrom(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "client.trace_42-a")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if seen != "client.trace_42-a" {
		t.Fatalf("context id = %q", seen)
	}
	if got := rec.Header().Get("X-Request-ID"); got != "client.trace_42-a" {
		t.Fatalf("response header = %q", got)
	}
}

func TestRequestIDReplacesInvalidClientValue(t *testing.T) {
	for _, bad := range []string{"", "has space", strings.Repeat("a", 129), "中文", "a\nb"} {
		h := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if bad != "" {
			req.Header.Set("X-Request-ID", bad)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		got := rec.Header().Get("X-Request-ID")
		if !strings.HasPrefix(got, "req_") || len(got) != len("req_")+36 {
			t.Errorf("for input %q got generated id %q", bad, got)
		}
	}
}
