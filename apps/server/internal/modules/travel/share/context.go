package share

import "context"

type viewerKey struct{}

// WithViewer 把访客身份放入上下文。键类型与 actor 不同，账号处理器的 actor.FromContext 永远读不到它（设计 5.2）。
func WithViewer(ctx context.Context, v Viewer) context.Context {
	return context.WithValue(ctx, viewerKey{}, v)
}

// ViewerFromContext 取出访客身份；不是访客请求时 ok 为 false。
func ViewerFromContext(ctx context.Context) (Viewer, bool) {
	v, ok := ctx.Value(viewerKey{}).(Viewer)
	return v, ok
}
