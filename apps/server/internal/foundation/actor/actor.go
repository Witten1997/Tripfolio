// Package actor 表示已认证的调用者。传输层从令牌与会话得到 Actor 并放入上下文，业务服务只接收 Actor。
package actor

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ClientKind 是客户端类型。
type ClientKind string

const (
	ClientWeb     ClientKind = "web"
	ClientAndroid ClientKind = "android"
	ClientHarmony ClientKind = "harmony"
)

// Valid 判断是否为已知客户端类型。
func (k ClientKind) Valid() bool {
	return k == ClientWeb || k == ClientAndroid || k == ClientHarmony
}

// Actor 是已认证的账号与会话。
type Actor struct {
	AccountID         uuid.UUID
	SessionID         uuid.UUID
	ClientKind        ClientKind
	AccountStatus     string
	ReauthenticatedAt *time.Time
}

// RecentlyAuthenticated 判断本会话是否在 window 内验证过密码（接口设计 REAUTH_REQUIRED）。
func (a Actor) RecentlyAuthenticated(now time.Time, window time.Duration) bool {
	return a.ReauthenticatedAt != nil && now.Sub(*a.ReauthenticatedAt) <= window
}

type ctxKey struct{}

// WithActor 把 Actor 放入上下文。
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

// FromContext 取出 Actor；未认证时 ok 为 false。
func FromContext(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(ctxKey{}).(Actor)
	return a, ok
}

// Without 返回移除了 Actor 的上下文；分享访客路径用它保证公开处理器读不到任何账号身份。
func Without(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, nil)
}
