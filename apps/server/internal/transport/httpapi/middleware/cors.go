package middleware

import (
	"net/http"
	"strings"
)

// 允许的请求方法与请求头。请求头覆盖幂等键、版本、CSRF 与请求编号（接口设计 1.2、3.1）。
const (
	corsAllowMethods  = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	corsAllowHeaders  = "Authorization, Content-Type, Accept, Idempotency-Key, If-Match, If-None-Match, X-Request-ID, X-CSRF-Token"
	corsExposeHeaders = "ETag, X-Request-ID, Location, Retry-After, Content-Disposition"
	corsMaxAge        = "600"
)

// CORS 按允许来源列表设置跨源响应头。允许列表来自配置：网页域与 Capacitor 来源（技术选型 4.3、§8）。
// 非允许来源的请求不带任何 CORS 头，由浏览器拒绝；预检请求返回 204。
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[strings.TrimRight(strings.ToLower(o), "/")] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Add("Vary", "Origin")
			if _, ok := allowed[strings.ToLower(origin)]; !ok {
				if isPreflight(r) {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
			if isPreflight(r) {
				h.Set("Access-Control-Allow-Methods", corsAllowMethods)
				h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
				h.Set("Access-Control-Max-Age", corsMaxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}
