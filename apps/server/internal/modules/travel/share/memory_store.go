package share

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore 是 Store 的内存实现，供单元测试与传输层测试使用。
type MemoryStore struct {
	mu      sync.Mutex
	rows    map[uuid.UUID]Record     // 分享 ID → 行
	owners  map[uuid.UUID]string     // 账号 ID → 状态，缺省 active
	trashed map[uuid.UUID]*time.Time // 旅行 ID → 回收站时间
	// Resolves 是 ResolveToken 的调用次数，供断言格式不符的令牌不查库。
	Resolves int
}

// NewMemoryStore 创建空存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: map[uuid.UUID]Record{}, owners: map[uuid.UUID]string{}, trashed: map[uuid.UUID]*time.Time{}}
}

var _ Store = (*MemoryStore)(nil)

// SetOwnerStatus 设置主人账号状态（active / deleting）。
func (m *MemoryStore) SetOwnerStatus(accountID uuid.UUID, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.owners[accountID] = status
}

// SetTripDeleted 设置旅行的回收站时间；nil 表示已恢复。
func (m *MemoryStore) SetTripDeleted(tripID uuid.UUID, at *time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trashed[tripID] = at
}

func (m *MemoryStore) GetByTrip(_ context.Context, accountID, tripID uuid.UUID) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if r.AccountID == accountID && r.TripID == tripID {
			return r, true, nil
		}
	}
	return Record{}, false, nil
}

func (m *MemoryStore) ResolveToken(_ context.Context, token string) (Resolved, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Resolves++
	for _, r := range m.rows {
		if r.Token == token {
			status, ok := m.owners[r.AccountID]
			if !ok {
				status = "active"
			}
			return Resolved{Record: r, OwnerStatus: status, TripDeletedAt: m.trashed[r.TripID]}, true, nil
		}
	}
	return Resolved{}, false, nil
}

func (m *MemoryStore) Insert(_ context.Context, r Record) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.rows {
		if existing.AccountID == r.AccountID && existing.TripID == r.TripID {
			return existing, false, nil
		}
	}
	m.rows[r.ID] = r
	return r, true, nil
}

func (m *MemoryStore) Rotate(_ context.Context, accountID, tripID uuid.UUID, token string, now time.Time) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.rows {
		if r.AccountID == accountID && r.TripID == tripID {
			at := now
			r.Token, r.RotatedAt = token, &at
			m.rows[id] = r
			return r, true, nil
		}
	}
	return Record{}, false, nil
}

func (m *MemoryStore) Delete(_ context.Context, accountID, tripID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.rows {
		if r.AccountID == accountID && r.TripID == tripID {
			delete(m.rows, id)
		}
	}
	return nil
}

func (m *MemoryStore) RecordView(_ context.Context, id uuid.UUID, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return nil
	}
	at := now
	r.ViewCount++
	r.LastViewedAt = &at
	m.rows[id] = r
	return nil
}
