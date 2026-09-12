package middleware

import (
	"context"
	"net/http"
	"strings"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/transport/httpapi/problem"
)

// Authenticator 把访问令牌换成 Actor；由账号模块的 SessionService 实现。
type Authenticator interface {
	Authenticate(ctx context.Context, accessToken string) (actor.Actor, error)
}

// AuthPolicy 决定哪些请求不要求身份、哪些在账号注销中仍可访问。
type AuthPolicy struct {
	// Public 返回 true 表示该请求不要求身份（有令牌时仍会解析并放入上下文）。
	Public func(r *http.Request) bool
	// AllowWhileDeleting 返回 true 表示账号处于 deleting 时仍放行。
	AllowWhileDeleting func(r *http.Request) bool
}

// Auth 对带 Bearer 的请求解析身份并放入上下文；对要求身份的请求缺少或无效令牌时返回 401。
func Auth(auth Authenticator, policy AuthPolicy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			required := policy.Public == nil || !policy.Public(r)
			token := bearerToken(r)
			if token == "" {
				if required {
					problem.Write(w, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录", RequestIDFrom(r.Context()))
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			a, err := auth.Authenticate(r.Context(), token)
			if err != nil {
				if !required {
					next.ServeHTTP(w, r)
					return
				}
				e, ok := apperr.As(err)
				if !ok {
					e = apperr.Internal(err)
				}
				problem.Write(w, e.Status, e.Code, e.Detail, RequestIDFrom(r.Context()))
				return
			}
			if a.AccountStatus != "active" && required && (policy.AllowWhileDeleting == nil || !policy.AllowWhileDeleting(r)) {
				problem.Write(w, http.StatusForbidden, "ACCOUNT_DELETING", "账号正在注销", RequestIDFrom(r.Context()))
				return
			}
			next.ServeHTTP(w, r.WithContext(actor.WithActor(r.Context(), a)))
		})
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// ClientIP 从代理头或远端地址取客户端 IP，用于限流键。
func ClientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return host
}
