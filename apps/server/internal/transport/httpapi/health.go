package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Readiness 是就绪探针的依赖：数据库可达且模式版本满足当前程序。
type Readiness interface {
	Check(ctx context.Context) error
}

// live 表示进程可响应；不做任何外部探测。
func live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ready 在 2 秒内完成依赖检查；失败返回 503 DEPENDENCY_UNAVAILABLE，不泄露连接串或内部版本。
func ready(check Readiness) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := check.Check(ctx); err != nil {
			WriteProblem(w, r, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
