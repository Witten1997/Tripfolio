package packing

import (
	"bytes"
	"context"
	"sort"
	"strings"
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
	merge      write.MergeSource
}

// NewMemoryStore 创建空存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		items: map[uuid.UUID]Resource{}, owners: map[uuid.UUID]uuid.UUID{},
		trips: map[uuid.UUID]TripInfo{}, tripOwners: map[uuid.UUID]uuid.UUID{},
		tombstones: map[uuid.UUID]struct{}{},
	}
}

var (
	_ Repo              = (*MemoryStore)(nil)
	_ Reader            = (*MemoryStore)(nil)
	_ write.Snapshotter = (*MemoryStore)(nil)
)

// SetMergeSource 绑定变更历史来源（通常是承载本存储的 MemoryUnitOfWork）。
func (m *MemoryStore) SetMergeSource(src write.MergeSource) { m.merge = src }

// PutTrip 登记一趟旅行，供测试构造上下文。
func (m *MemoryStore) PutTrip(accountID, tripID uuid.UUID, info TripInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trips[tripID] = info
	m.tripOwners[tripID] = accountID
}

// AddTombstone 登记一个已被永久清理的 ID，供测试 ID 复用规则。
func (m *MemoryStore) AddTombstone(id uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tombstones[id] = struct{}{}
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

// MergeSource 实现 Repo。
func (m *MemoryStore) MergeSource() write.MergeSource { return m.merge }

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

func (m *MemoryStore) get(accountID, tripID, id uuid.UUID) (Resource, bool) {
	if m.owners[id] != accountID {
		return Resource{}, false
	}
	r, ok := m.items[id]
	if !ok || r.TripID != tripID {
		return Resource{}, false
	}
	return r, true
}

// Get 实现 Repo 与 Reader。
func (m *MemoryStore) Get(_ context.Context, accountID, tripID, id uuid.UUID) (Resource, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.get(accountID, tripID, id)
	return r, ok, nil
}

// GetForUpdate 实现 Repo。
func (m *MemoryStore) GetForUpdate(ctx context.Context, accountID, tripID, id uuid.UUID) (Resource, bool, error) {
	return m.Get(ctx, accountID, tripID, id)
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

func normalizeName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// NameTaken 实现 Repo。
func (m *MemoryStore) NameTaken(_ context.Context, accountID, tripID uuid.UUID, category Category, name string, exclude uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	needle := normalizeName(name)
	for id, r := range m.items {
		if id == exclude || m.owners[id] != accountID || r.TripID != tripID || r.DeletedAt != nil {
			continue
		}
		if r.Category == category && normalizeName(r.Name) == needle {
			return true, nil
		}
	}
	return false, nil
}

func (m *MemoryStore) ProbeBatch(ctx context.Context, accountID, tripID uuid.UUID, items []BatchItem) (map[uuid.UUID]BatchProbe, error) {
	probes := make(map[uuid.UUID]BatchProbe, len(items))
	for _, item := range items {
		nameTaken, err := m.NameTaken(ctx, accountID, tripID, item.Category, item.Name, uuid.Nil)
		if err != nil {
			return nil, err
		}
		idUsed, err := m.IDExists(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		probes[item.ID] = BatchProbe{NameTaken: nameTaken, IDUsed: idUsed}
	}
	return probes, nil
}

// Insert 实现 Repo。
func (m *MemoryStore) Insert(_ context.Context, accountID uuid.UUID, r Resource) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[r.ID] = r
	m.owners[r.ID] = accountID
	return r, nil
}

func (m *MemoryStore) InsertBatch(ctx context.Context, accountID uuid.UUID, items []Resource) ([]Resource, error) {
	created := make([]Resource, 0, len(items))
	for _, item := range items {
		r, err := m.Insert(ctx, accountID, item)
		if err != nil {
			return nil, err
		}
		created = append(created, r)
	}
	return created, nil
}

type memoryError string

func (e memoryError) Error() string { return string(e) }

const errNoRow memoryError = "packing: no row"

func (m *MemoryStore) mutate(accountID, tripID, id uuid.UUID, fn func(r *Resource)) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.get(accountID, tripID, id)
	if !ok {
		return Resource{}, errNoRow
	}
	fn(&r)
	r.Version++
	m.items[id] = r
	return r, nil
}

// Update 实现 Repo。
func (m *MemoryStore) Update(_ context.Context, accountID, tripID, id uuid.UUID, v Values, now time.Time) (Resource, error) {
	return m.mutate(accountID, tripID, id, func(r *Resource) {
		r.Name, r.Category, r.Quantity, r.Notes, r.Status, r.UpdatedAt = v.Name, v.Category, v.Quantity, v.Notes, v.Status, now
	})
}

func (m *MemoryStore) UpdateStatusIfVersion(_ context.Context, accountID, tripID, id uuid.UUID, version int64, status Status, now time.Time) (Resource, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, found := m.get(accountID, tripID, id)
	trip, tripFound := m.trips[tripID]
	if !found || !tripFound || m.tripOwners[tripID] != accountID || trip.DeletedAt != nil || r.DeletedAt != nil || int64(r.Version) != version {
		return Resource{}, false, nil
	}
	r.Status, r.UpdatedAt = status, now
	r.Version++
	m.items[id] = r
	return r, true, nil
}

// SoftDelete 实现 Repo。
func (m *MemoryStore) SoftDelete(_ context.Context, accountID, tripID, id uuid.UUID, now time.Time) (Resource, error) {
	return m.mutate(accountID, tripID, id, func(r *Resource) {
		t := now
		r.DeletedAt, r.UpdatedAt = &t, now
	})
}

// List 实现 Reader。
func (m *MemoryStore) List(_ context.Context, accountID, tripID uuid.UUID, q ListQuery) ([]Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []Resource
	for id, r := range m.items {
		if m.owners[id] != accountID || r.TripID != tripID || r.DeletedAt != nil {
			continue
		}
		if q.Category != "" && r.Category != q.Category {
			continue
		}
		if q.Status != "" && r.Status != q.Status {
			continue
		}
		if !afterPosition(r, q.After) {
			continue
		}
		rows = append(rows, r)
	}
	sortRows(rows)
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, nil
}

// compareIDs 按字节比较 UUID，与 PostgreSQL 的 uuid 排序一致。
func compareIDs(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }

func sortRows(rows []Resource) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return compareIDs(a.ID, b.ID) < 0
	})
}

// afterPosition 判断 r 是否位于游标之后（键集分页的行比较）。
func afterPosition(r Resource, after *Position) bool {
	if after == nil {
		return true
	}
	if r.Category != after.Category {
		return r.Category > after.Category
	}
	if !r.CreatedAt.Equal(after.CreatedAt) {
		return r.CreatedAt.After(after.CreatedAt)
	}
	return compareIDs(r.ID, after.ID) > 0
}
