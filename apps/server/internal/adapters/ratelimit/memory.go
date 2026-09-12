// Package ratelimit 提供单实例内存限流：固定窗口计数，按键（IP、邮箱等）限制次数。
// 多实例部署时按技术选型第 8 节改用 Redis。
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	windowStart time.Time
	count       int
}

// Limiter 是固定窗口限流器。
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
}

// New 创建限流器。
func New() *Limiter {
	return &Limiter{buckets: map[string]*bucket{}}
}

// Allow 判断 key 在 window 内是否还能执行一次（最多 limit 次）。不允许时返回需要等待的时长。
func (l *Limiter) Allow(key string, limit int, window time.Duration, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastGC) > time.Minute {
		for k, b := range l.buckets {
			if now.Sub(b.windowStart) > 24*time.Hour {
				delete(l.buckets, k)
			}
		}
		l.lastGC = now
	}
	b, ok := l.buckets[key]
	if !ok || now.Sub(b.windowStart) >= window {
		l.buckets[key] = &bucket{windowStart: now, count: 1}
		return true, 0
	}
	if b.count >= limit {
		return false, window - now.Sub(b.windowStart)
	}
	b.count++
	return true, 0
}
