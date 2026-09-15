package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tripfolio/server/internal/modules/account"
	"tripfolio/server/internal/transport/httpapi"
)

func TestEmailChallengeWithMailDisabledReturns503(t *testing.T) {
	router := httpapi.NewRouter(httpapi.Deps{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Identity: account.NewIdentityService(account.IdentityDeps{}),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/email-challenges", strings.NewReader(`{"purpose":"register","email":"user@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/problem+json") {
		t.Fatalf("Content-Type = %q", got)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != "DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("邮件禁用应返回依赖不可用: %s, %v", rec.Body.String(), err)
	}
}
