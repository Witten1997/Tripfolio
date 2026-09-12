package middleware

import (
	"context"
	"net/http"
)

type requestKey struct{}

// WithRequest 把 *http.Request 放入上下文，供 strict 处理器读取 Cookie、Origin、客户端 IP 等请求信息。
func WithRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestKey{}, r)))
	})
}

// RequestFrom 取出原始请求；不存在时返回 nil。
func RequestFrom(ctx context.Context) *http.Request {
	r, _ := ctx.Value(requestKey{}).(*http.Request)
	return r
}
