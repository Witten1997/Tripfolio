package account

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
)

// authCache 是进程内的鉴权结果缓存：按会话 ID 保存 Actor，使绝大多数请求不必跨网络读取会话与账号。
// 条目在 TTL 内直接命中；登出、撤销、改密与重新认证会主动失效，跨设备撤销最多延迟一个 TTL 生效。
// 单二进制只有一个进程，进程内缓存不存在多实例不一致的问题。
type authCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[uuid.UUID]*authEntry
}

type authEntry struct {
	actor     actor.Actor
	expiresAt time.Time // 缓存到期：写入时间加 TTL 与会话过期时间的较早者
	lastSeen  time.Time // 上次写回 last_seen_at 的时间，把触碰节流到每分钟一次
}

// sweepThreshold 条目数达到此值时在写入时顺带清理过期条目，避免长期运行缓慢增长。
const sweepThreshold = 1024

func newAuthCache(ttl time.Duration) *authCache {
	return &authCache{ttl: ttl, entries: map[uuid.UUID]*authEntry{}}
}

// lookup 返回命中的 Actor；touch 为 true 表示距上次触碰已满一分钟，调用方应写回 last_seen_at。
func (c *authCache) lookup(id uuid.UUID, now time.Time) (a actor.Actor, touch, ok bool) {
	if c.ttl <= 0 {
		return actor.Actor{}, false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, found := c.entries[id]
	if !found {
		return actor.Actor{}, false, false
	}
	if !now.Before(e.expiresAt) {
		delete(c.entries, id)
		return actor.Actor{}, false, false
	}
	if now.Sub(e.lastSeen) >= time.Minute {
		e.lastSeen = now
		touch = true
	}
	return e.actor, touch, true
}

// store 写入一条鉴权结果；lastSeen 是数据库中的 last_seen_at。返回值含义同 lookup 的 touch。
func (c *authCache) store(id uuid.UUID, a actor.Actor, sessionExpiresAt, lastSeen, now time.Time) (touch bool) {
	touch = now.Sub(lastSeen) >= time.Minute
	if touch {
		lastSeen = now
	}
	if c.ttl <= 0 {
		return touch
	}
	expiresAt := now.Add(c.ttl)
	if sessionExpiresAt.Before(expiresAt) {
		expiresAt = sessionExpiresAt
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= sweepThreshold {
		for k, e := range c.entries {
			if !now.Before(e.expiresAt) {
				delete(c.entries, k)
			}
		}
	}
	c.entries[id] = &authEntry{actor: a, expiresAt: expiresAt, lastSeen: lastSeen}
	return touch
}

func (c *authCache) remove(id uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

func (c *authCache) removeAccount(accountID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, e := range c.entries {
		if e.actor.AccountID == accountID {
			delete(c.entries, id)
		}
	}
}
