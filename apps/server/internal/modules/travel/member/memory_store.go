package member

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/write"
)

// MemoryStore 是 Repo 与 Reader 的内存实现，供本包与传输层单元测试使用；配合 write.MemoryUnitOfWork 可回滚。
type MemoryStore struct {
	mu         sync.Mutex
	items      map[uuid.UUID]Resource
	owners     map[uuid.UUID]uuid.UUID
	trips      map[uuid.UUID]TripInfo
	tripOwners map[uuid.UUID]uuid.UUID
	tombstones map[uuid.UUID]struct{}
	references map[uuid.UUID]int64
}

// NewMemoryStore 创建空存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		items: map[uuid.UUID]Resource{}, owners: map[uuid.UUID]uuid.UUID{},
		trips: map[uuid.UUID]TripInfo{}, tripOwners: map[uuid.UUID]uuid.UUID{},
		tombstones: map[uuid.UUID]struct{}{}, references: map[uuid.UUID]int64{},
	}
}

var (
	_ Repo              = (*MemoryStore)(nil)
	_ Reader            = (*MemoryStore)(nil)
	_ write.Snapshotter = (*MemoryStore)(nil)
)

// PutTrip 登记一趟旅行，供测试构造上下文。
func (m *MemoryStore) PutTrip(accountID, tripID uuid.UUID, info TripInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trips[tripID] = info
	m.tripOwners[tripID] = accountID
}

// Put 直接登记一位成员（例如「我」）。
func (m *MemoryStore) Put(accountID uuid.UUID, r Resource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[r.ID] = r
	m.owners[r.ID] = accountID
}

// AddTombstone 登记一个已被永久清理的 ID。
func (m *MemoryStore) AddTombstone(id uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tombstones[id] = struct{}{}
}

// SetReferences 模拟账目对成员的引用数。
func (m *MemoryStore) SetReferences(memberID uuid.UUID, count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.references[memberID] = count
}

// Snapshot 实现 write.Snapshotter。
func (m *MemoryStore) Snapshot() func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make(map[uuid.UUID]Resource, len(m.items))
	for k, v := range m.items {
		items[k] = v
	}
	owners := make(map[uuid.UUID]uuid.UUID, len(m.owners))
	for k, v := range m.owners {
		owners[k] = v
	}
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.items, m.owners = items, owners
	}
}

// Trip 实现 Repo 与 Reader。
func (m *MemoryStore) Trip(_ context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tripOwners[tripID] != accountID {
		return TripInfo{}, false, nil
	}
	info, ok := m.trips[tripID]
	return info, ok, nil
}

func (m *MemoryStore) active(accountID, tripID uuid.UUID) []Resource {
	var out []Resource
	for id, r := range m.items {
		if m.owners[id] == accountID && r.TripID == tripID && r.DeletedAt == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	if out == nil {
		out = []Resource{}
	}
	return out
}

// List 实现 Reader。
func (m *MemoryStore) List(_ context.Context, accountID, tripID uuid.UUID) ([]Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active(accountID, tripID), nil
}

// ListForUpdate 实现 Repo。
func (m *MemoryStore) ListForUpdate(_ context.Context, accountID, tripID uuid.UUID) ([]Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active(accountID, tripID), nil
}

// IDExists 实现 Repo。
func (m *MemoryStore) IDExists(_ context.Context, id uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[id]; ok {
		return true, nil
	}
	_, ok := m.tombstones[id]
	return ok, nil
}

// Insert 实现 Repo。
func (m *MemoryStore) Insert(_ context.Context, accountID uuid.UUID, r Resource) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[r.ID] = r
	m.owners[r.ID] = accountID
	return r, nil
}

func (m *MemoryStore) mutate(accountID, tripID, id uuid.UUID, fn func(r *Resource)) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.items[id]
	if !ok || m.owners[id] != accountID || r.TripID != tripID {
		return Resource{}, errNotFound
	}
	fn(&r)
	r.Version++
	m.items[id] = r
	return r, nil
}

type memoryError string

func (e memoryError) Error() string { return string(e) }

const errNotFound = memoryError("member: not found")

// Update 实现 Repo。
func (m *MemoryStore) Update(_ context.Context, accountID, tripID, id uuid.UUID, v Values, now time.Time) (Resource, error) {
	return m.mutate(accountID, tripID, id, func(r *Resource) {
		r.Name, r.SharePercent, r.SortOrder, r.UpdatedAt = v.Name, v.SharePercent, v.SortOrder, now
	})
}

// SoftDelete 实现 Repo。
func (m *MemoryStore) SoftDelete(_ context.Context, accountID, tripID, id uuid.UUID, now time.Time) (Resource, error) {
	return m.mutate(accountID, tripID, id, func(r *Resource) {
		t := now
		r.DeletedAt, r.UpdatedAt = &t, now
	})
}

// ReferenceCount 实现 Repo。
func (m *MemoryStore) ReferenceCount(_ context.Context, _, _ uuid.UUID, memberID uuid.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.references[memberID], nil
}
