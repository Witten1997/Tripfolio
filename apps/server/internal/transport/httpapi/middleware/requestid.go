// Package middleware 提供请求编号、CORS、恢复与日志等 HTTP 中间件。
// 认证与限流中间件在账号模块落地时补入。
package middleware

import (
	"context"
	"net/http"
	"regexp"

	"github.com/google/uuid"
)

// RequestIDHeader 是请求编号的头名。
const RequestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// validRequestID 限制客户端传入的编号字符集与长度，避免日志注入与超长头。
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// RequestID 读取或生成请求编号，写入响应头与上下文。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestIDFrom 返回上下文中的请求编号；没有时返回空串。
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func newRequestID() string {
	u, err := uuid.NewV7()
	if err != nil {
		u = uuid.New()
	}
	return "req_" + u.String()
}
