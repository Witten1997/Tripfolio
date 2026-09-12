package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tripfolio/server/internal/transport/httpapi/middleware"
)

func newCORSHandler(t *testing.T) http.Handler {
	t.Helper()
	called := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return middleware.CORS([]string{"http://localhost:5173", "https://localhost"})(called)
}

func TestCORSAllowsCapacitorOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metadata", nil)
	req.Header.Set("Origin", "https://localhost")
	rec := httptest.NewRecorder()
	newCORSHandler(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://localhost" {
		t.Fatalf("Allow-Origin = %q", got)
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Fatalf("Vary = %q", rec.Header().Get("Vary"))
	}
}

func TestCORSPreflightForAllowedOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/trips", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	rec := httptest.NewRecorder()
	newCORSHandler(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	for _, h := range []string{"Access-Control-Allow-Methods", "Access-Control-Allow-Headers", "Access-Control-Max-Age"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
}

func TestCORSIgnoresUnknownOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metadata", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	newCORSHandler(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unknown origin must not receive Allow-Origin")
	}
}
