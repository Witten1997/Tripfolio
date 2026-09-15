package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/share"
	"tripfolio/server/internal/transport/httpapi/middleware"
)

type resolverFunc func(ctx context.Context, token, ip string) (share.Viewer, error)

func (f resolverFunc) Resolve(ctx context.Context, token, ip string) (share.Viewer, error) {
	return f(ctx, token, ip)
}

func isPublic(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/public/") }

func TestShareMiddlewareInjectsViewerAndStripsActor(t *testing.T) {
	want := share.Viewer{ShareID: uuid.New(), AccountID: uuid.New(), TripID: uuid.New(), ClientIP: "203.0.113.9"}
	resolver := resolverFunc(func(_ context.Context, token, ip string) (share.Viewer, error) {
		if token != "t000000000000000000001" || ip != "203.0.113.9" {
			return share.Viewer{}, errors.New("unexpected " + token + " " + ip)
		}
		return want, nil
	})
	var gotViewer share.Viewer
	var hadActor bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotViewer, _ = share.ViewerFromContext(r.Context())
		_, hadActor = actor.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	h := middleware.Share(resolver, isPublic)(next)

	req := httptest.NewRequest(http.MethodGet, "/public/trip", nil)
	req.Header.Set("Authorization", "Share t000000000000000000001")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	// 登录用户打开分享链接时上下文里可能已有 Actor，公开处理器绝不能读到它。
	req = req.WithContext(actor.WithActor(req.Context(), actor.Actor{AccountID: uuid.New(), AccountStatus: "active"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || gotViewer != want || hadActor {
		t.Fatalf("status=%d viewer=%+v hadActor=%v", rec.Code, gotViewer, hadActor)
	}
	if rec.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Fatalf("X-Robots-Tag = %q", rec.Header().Get("X-Robots-Tag"))
	}
}

func TestShareMiddlewareRejectsMissingOrInvalidToken(t *testing.T) {
	resolver := resolverFunc(func(context.Context, string, string) (share.Viewer, error) {
		return share.Viewer{}, apperr.New(404, "SHARE_NOT_FOUND", "分享链接不存在或已失效")
	})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("不应到达处理器") })
	h := middleware.Share(resolver, isPublic)(next)

	for _, header := range []string{"", "Bearer abc", "Share"} {
		req := httptest.NewRequest(http.MethodGet, "/public/trip", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "AUTH_REQUIRED") {
			t.Fatalf("header %q: status=%d body=%s", header, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/public/trip", nil)
	req.Header.Set("Authorization", "Share t000000000000000000009")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "SHARE_NOT_FOUND") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestShareMiddlewareIgnoresNonPublicPaths(t *testing.T) {
	resolver := resolverFunc(func(context.Context, string, string) (share.Viewer, error) {
		t.Fatal("非公开路径不应解析")
		return share.Viewer{}, nil
	})
	var hadViewer bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadViewer = share.ViewerFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/trips", nil)
	req.Header.Set("Authorization", "Share t000000000000000000001")
	rec := httptest.NewRecorder()
	middleware.Share(resolver, isPublic)(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || hadViewer {
		t.Fatalf("status=%d hadViewer=%v", rec.Code, hadViewer)
	}
}
