package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"tripfolio/server/internal/transport/httpapi/problem"
)

// Recoverer 捕获处理器 panic，记录堆栈并返回 500 INTERNAL_ERROR；不向客户端泄露堆栈。
// http.ErrAbortHandler 按 net/http 约定原样重新抛出。
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				logger.ErrorContext(r.Context(), "处理请求时 panic",
					"panic", rec,
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", RequestIDFrom(r.Context()),
					"stack", string(debug.Stack()),
				)
				problem.Write(w, http.StatusInternalServerError, "INTERNAL_ERROR", "", RequestIDFrom(r.Context()))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
