package trip

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
)

// MemoryStore 是 Repo 与 Reader 的内存实现，供本包与传输层单元测试使用；配合 write.MemoryUnitOfWork 可回滚。
type MemoryStore struct {
	mu         sync.Mutex
	trips      map[uuid.UUID]Resource
	owners     map[uuid.UUID]uuid.UUID
	tombstones map[uuid.UUID]struct{}
	jobs       map[uuid.UUID]DeletionJob
	merge      write.MergeSource

	// DefaultTimezone 是任何账号的默认时区。
	DefaultTimezone string
	// EstimatedAmounts、LocalTimes 按旅行模拟跨模块探针结果；ItineraryDates 是各旅行有效行程项目的归属日期。
	EstimatedAmounts map[uuid.UUID]bool
	LocalTimes       map[uuid.UUID]bool
	ItineraryDates   map[uuid.UUID][]types.Date
}

// NewMemoryStore 创建空存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		trips: map[uuid.UUID]Resource{}, owners: map[uuid.UUID]uuid.UUID{}, tombstones: map[uuid.UUID]struct{}{},
		jobs: map[uuid.UUID]DeletionJob{}, DefaultTimezone: "Asia/Shanghai",
		EstimatedAmounts: map[uuid.UUID]bool{}, LocalTimes: map[uuid.UUID]bool{}, ItineraryDates: map[uuid.UUID][]types.Date{},
	}
}

var (
	_ Repo              = (*MemoryStore)(nil)
	_ Reader            = (*MemoryStore)(nil)
	_ write.Snapshotter = (*MemoryStore)(nil)
)

// SetMergeSource 绑定变更历史来源（通常是承载本存储的 MemoryUnitOfWork）。
func (m *MemoryStore) SetMergeSource(src write.MergeSource) { m.merge = src }

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
	trips := make(map[uuid.UUID]Resource, len(m.trips))
	for k, v := range m.trips {
		trips[k] = v
	}
	owners := make(map[uuid.UUID]uuid.UUID, len(m.owners))
	for k, v := range m.owners {
		owners[k] = v
	}
	jobs := make(map[uuid.UUID]DeletionJob, len(m.jobs))
	for k, v := range m.jobs {
		jobs[k] = v
	}
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.trips, m.owners, m.jobs = trips, owners, jobs
	}
}

func (m *MemoryStore) MergeSource() write.MergeSource { return m.merge }

func (m *MemoryStore) get(accountID, id uuid.UUID) (Resource, bool) {
	if m.owners[id] != accountID {
		return Resource{}, false
	}
	r, ok := m.trips[id]
	return r, ok
}

func (m *MemoryStore) Get(_ context.Context, accountID, id uuid.UUID) (Resource, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.get(accountID, id)
	return r, ok, nil
}

func (m *MemoryStore) GetForUpdate(ctx context.Context, accountID, id uuid.UUID) (Resource, bool, error) {
	return m.Get(ctx, accountID, id)
}

func (m *MemoryStore) IDExists(_ context.Context, id uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.trips[id]; ok {
		return true, nil
	}
	_, ok := m.tombstones[id]
	return ok, nil
}

func (m *MemoryStore) AccountDefaultTimezone(context.Context, uuid.UUID) (string, error) {
	return m.DefaultTimezone, nil
}

func (m *MemoryStore) Insert(_ context.Context, accountID uuid.UUID, r Resource) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trips[r.ID] = r
	m.owners[r.ID] = accountID
	return r, nil
}

func (m *MemoryStore) mutate(accountID, id uuid.UUID, fn func(r *Resource)) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.get(accountID, id)
	if !ok {
		return Resource{}, errNoRow
	}
	fn(&r)
	r.Version++
	m.trips[id] = r
	return r, nil
}

type memoryError string

func (e memoryError) Error() string { return string(e) }

const errNoRow memoryError = "trip: no row"

func (m *MemoryStore) Update(_ context.Context, accountID, id uuid.UUID, v Values, now time.Time) (Resource, error) {
	return m.mutate(accountID, id, func(r *Resource) {
		r.Name, r.StartDate, r.EndDate, r.Destination, r.Notes = v.Name, v.StartDate, v.EndDate, v.Destination, v.Notes
		r.Timezone, r.CurrencyCode, r.BudgetAmount, r.UpdatedAt = v.Timezone, v.CurrencyCode, v.BudgetAmount, now
	})
}

func (m *MemoryStore) SetArchived(_ context.Context, accountID, id uuid.UUID, archivedAt *time.Time, now time.Time) (Resource, error) {
	return m.mutate(accountID, id, func(r *Resource) { r.ArchivedAt, r.UpdatedAt = archivedAt, now })
}

func (m *MemoryStore) Trash(_ context.Context, accountID, id uuid.UUID, now, purgeAfter time.Time) (Resource, error) {
	return m.mutate(accountID, id, func(r *Resource) {
		d, p := now, purgeAfter
		r.DeletedAt, r.PurgeAfterAt, r.UpdatedAt = &d, &p, now
	})
}

func (m *MemoryStore) Restore(_ context.Context, accountID, id uuid.UUID, now time.Time) (Resource, error) {
	return m.mutate(accountID, id, func(r *Resource) { r.DeletedAt, r.PurgeAfterAt, r.UpdatedAt = nil, nil, now })
}

func (m *MemoryStore) RequestPurge(_ context.Context, accountID, id uuid.UUID, now time.Time) (Resource, error) {
	return m.mutate(accountID, id, func(r *Resource) {
		t := now
		r.PurgeRequestedAt, r.UpdatedAt = &t, now
	})
}

func (m *MemoryStore) ActiveDeletionJob(_ context.Context, accountID, tripID uuid.UUID) (DeletionJob, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.OwnerAccountID == accountID && j.TargetTripID == tripID && j.Status != "completed" {
			return j, true, nil
		}
	}
	return DeletionJob{}, false, nil
}

func (m *MemoryStore) InsertDeletionJob(_ context.Context, job DeletionJob) (DeletionJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = job
	return job, nil
}

func (m *MemoryStore) HasEstimatedAmounts(_ context.Context, _, tripID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.EstimatedAmounts[tripID], nil
}

func (m *MemoryStore) HasLocalTimes(_ context.Context, _, tripID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.LocalTimes[tripID], nil
}

func (m *MemoryStore) HasItineraryOutside(_ context.Context, _, tripID uuid.UUID, start, end types.Date) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.ItineraryDates[tripID] {
		if d.Before(start) || d.After(end) {
			return true, nil
		}
	}
	return false, nil
}

// compareIDs 按字节比较 UUID，与 PostgreSQL 的 uuid 排序一致。
func compareIDs(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }

// afterPosition 判断 r 在给定排序下是否位于游标之后（键集分页的行比较）。
func afterPosition(r Resource, sortBy Sort, after *Position) bool {
	if after == nil {
		return true
	}
	switch sortBy {
	case SortUpdatedAtDesc:
		if after.UpdatedAt == nil {
			return true
		}
		if !r.UpdatedAt.Equal(*after.UpdatedAt) {
			return r.UpdatedAt.Before(*after.UpdatedAt)
		}
	default:
		if r.StartDate != after.StartDate {
			return r.StartDate.Before(after.StartDate)
		}
	}
	return compareIDs(r.ID, after.ID) < 0
}

func (m *MemoryStore) List(_ context.Context, accountID uuid.UUID, q ListQuery) ([]ListItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	needle := strings.ToLower(q.Filters.Query)
	var rows []ListItem
	for id, r := range m.trips {
		if m.owners[id] != accountID || r.DeletedAt != nil {
			continue
		}
		switch q.Filters.Archived {
		case ArchivedOnly:
			if r.ArchivedAt == nil {
				continue
			}
		case ArchivedNone:
			if r.ArchivedAt != nil {
				continue
			}
		}
		phase := PhaseOf(r, q.Now)
		if q.Filters.Phase != nil && phase != *q.Filters.Phase {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(r.Name), needle) && !strings.Contains(strings.ToLower(r.Destination), needle) {
			continue
		}
		if !afterPosition(r, q.Filters.Sort, q.After) {
			continue
		}
		rows = append(rows, ListItem{Resource: r, Phase: phase})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].Resource, rows[j].Resource
		if q.Filters.Sort == SortUpdatedAtDesc {
			if !a.UpdatedAt.Equal(b.UpdatedAt) {
				return a.UpdatedAt.After(b.UpdatedAt)
			}
		} else if a.StartDate != b.StartDate {
			return a.StartDate.After(b.StartDate)
		}
		return compareIDs(a.ID, b.ID) > 0
	})
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, nil
}

func (m *MemoryStore) ListTrashed(_ context.Context, accountID uuid.UUID, q TrashedQuery) ([]Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []Resource
	for id, r := range m.trips {
		if m.owners[id] != accountID || r.DeletedAt == nil {
			continue
		}
		if q.After != nil && q.After.DeletedAt != nil {
			if !r.DeletedAt.Equal(*q.After.DeletedAt) {
				if !r.DeletedAt.Before(*q.After.DeletedAt) {
					continue
				}
			} else if compareIDs(r.ID, q.After.ID) >= 0 {
				continue
			}
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].DeletedAt.Equal(*rows[j].DeletedAt) {
			return rows[i].DeletedAt.After(*rows[j].DeletedAt)
		}
		return compareIDs(rows[i].ID, rows[j].ID) > 0
	})
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, nil
}
