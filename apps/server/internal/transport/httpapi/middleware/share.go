package middleware

import (
	"context"
	"net/http"
	"strings"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/share"
	"tripfolio/server/internal/transport/httpapi/problem"
)

// ShareResolver 把分享令牌换成访客身份；由 share.Service 实现。
type ShareResolver interface {
	Resolve(ctx context.Context, token, clientIP string) (share.Viewer, error)
}

// Share 对访客分享路径强制校验 Authorization: Share <token>，把 Viewer 放入独立上下文键，并剥离可能存在的账号身份。
// 非分享路径原样放行。这是分享功能的安全核心：Viewer 与 Actor 不同类型、不同键，分享令牌在类型层面触达不了账号接口。
func Share(resolver ShareResolver, isSharePath func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isSharePath(r) {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
			token := shareToken(r)
			if token == "" {
				problem.Write(w, http.StatusUnauthorized, "AUTH_REQUIRED", "缺少分享令牌", RequestIDFrom(r.Context()))
				return
			}
			v, err := resolver.Resolve(r.Context(), token, ClientIP(r))
			if err != nil {
				e, ok := apperr.As(err)
				if !ok {
					e = apperr.Internal(err)
				}
				for k, val := range e.Headers {
					w.Header().Set(k, val)
				}
				problem.Write(w, e.Status, e.Code, e.Detail, RequestIDFrom(r.Context()))
				return
			}
			ctx := share.WithViewer(actor.Without(r.Context()), v)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func shareToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 6 && strings.EqualFold(h[:6], "Share ") {
		return strings.TrimSpace(h[6:])
	}
	return ""
}
