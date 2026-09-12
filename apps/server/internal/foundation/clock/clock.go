// Package clock 提供可替换的时间源，业务服务通过它取“现在”，测试用 Fake 控制时间。
package clock

import (
	"sync"
	"time"
)

// Clock 返回当前 UTC 时间。
type Clock interface {
	Now() time.Time
}

// Real 使用系统时间。
type Real struct{}

// Now 实现 Clock。
func (Real) Now() time.Time { return time.Now().UTC() }

// Fake 是测试用的可控时钟。
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

// NewFake 创建固定在 t 的时钟。
func NewFake(t time.Time) *Fake { return &Fake{t: t.UTC()} }

// Now 实现 Clock。
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Advance 前进 d。
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

// Set 设置为 t。
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t.UTC()
}
