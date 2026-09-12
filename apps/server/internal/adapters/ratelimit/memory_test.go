package ratelimit_test

import (
	"testing"
	"time"

	"tripfolio/server/internal/adapters/ratelimit"
)

func TestFixedWindow(t *testing.T) {
	l := ratelimit.New()
	now := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("ip:1", 3, time.Minute, now); !ok {
			t.Fatalf("call %d should be allowed", i+1)
		}
	}
	ok, wait := l.Allow("ip:1", 3, time.Minute, now.Add(10*time.Second))
	if ok || wait != 50*time.Second {
		t.Fatalf("4th call: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("ip:2", 3, time.Minute, now); !ok {
		t.Fatal("other key must be independent")
	}
	if ok, _ := l.Allow("ip:1", 3, time.Minute, now.Add(time.Minute)); !ok {
		t.Fatal("new window should allow again")
	}
}
