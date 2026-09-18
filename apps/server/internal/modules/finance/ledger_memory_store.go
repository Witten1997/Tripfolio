package finance

import (
	"bytes"
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/trip"
)

// LedgerMemoryStore 是 LedgerRepo 与 LedgerReader 的内存实现，供本包与传输层单元测试使用；
// 配合 write.MemoryUnitOfWork 可回滚。旅行、分类与资产由测试通过 Put* 方法登记。
type LedgerMemoryStore struct {
	mu         sync.Mutex
	entries    map[uuid.UUID]LedgerResource
	owners     map[uuid.UUID]uuid.UUID
	trips      map[uuid.UUID]trip.Resource
	tripOwners map[uuid.UUID]uuid.UUID
	categories map[uuid.UUID]CategoryResource
	catOwners  map[uuid.UUID]uuid.UUID
	assets     map[uuid.UUID]memoryAsset
	tombstones map[uuid.UUID]struct{}
	merge      write.MergeSource
}

type memoryAsset struct {
	accountID uuid.UUID
	tripID    uuid.UUID
	deleted   bool
}

// NewLedgerMemoryStore 创建空存储。
func NewLedgerMemoryStore() *LedgerMemoryStore {
	return &LedgerMemoryStore{
		entries: map[uuid.UUID]LedgerResource{}, owners: map[uuid.UUID]uuid.UUID{},
		trips: map[uuid.UUID]trip.Resource{}, tripOwners: map[uuid.UUID]uuid.UUID{},
		categories: map[uuid.UUID]CategoryResource{}, catOwners: map[uuid.UUID]uuid.UUID{},
		assets: map[uuid.UUID]memoryAsset{}, tombstones: map[uuid.UUID]struct{}{},
	}
}

var (
	_ LedgerRepo        = (*LedgerMemoryStore)(nil)
	_ LedgerReader      = (*LedgerMemoryStore)(nil)
	_ write.Snapshotter = (*LedgerMemoryStore)(nil)
)

// SetMergeSource 绑定变更历史来源（通常是承载本存储的 MemoryUnitOfWork）。
func (m *LedgerMemoryStore) SetMergeSource(src write.MergeSource) { m.merge = src }

// PutTrip 登记一趟旅行，供测试构造上下文。
func (m *LedgerMemoryStore) PutTrip(accountID uuid.UUID, t trip.Resource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trips[t.ID] = t
	m.tripOwners[t.ID] = accountID
}

// TripResource 返回登记的旅行当前状态，供测试核对币种锁。
func (m *LedgerMemoryStore) TripResource(tripID uuid.UUID) (trip.Resource, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.trips[tripID]
	return t, ok
}

// PutCategory 登记一个分类。
func (m *LedgerMemoryStore) PutCategory(accountID uuid.UUID, c CategoryResource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.categories[c.ID] = c
	m.catOwners[c.ID] = accountID
}

// PutAsset 登记一个图片资产；deleted 为 true 表示已删除。
func (m *LedgerMemoryStore) PutAsset(accountID, tripID, assetID uuid.UUID, deleted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assets[assetID] = memoryAsset{accountID: accountID, tripID: tripID, deleted: deleted}
}

// AddTombstone 登记一个已被永久清理的 ID。
func (m *LedgerMemoryStore) AddTombstone(id uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tombstones[id] = struct{}{}
}

// Snapshot 实现 write.Snapshotter：账目与旅行（币种锁）在失败时一起回滚。
func (m *LedgerMemoryStore) Snapshot() func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := make(map[uuid.UUID]LedgerResource, len(m.entries))
	for k, v := range m.entries {
		entries[k] = v
	}
	owners := make(map[uuid.UUID]uuid.UUID, len(m.owners))
	for k, v := range m.owners {
		owners[k] = v
	}
	trips := make(map[uuid.UUID]trip.Resource, len(m.trips))
	for k, v := range m.trips {
		trips[k] = v
	}
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.entries, m.owners, m.trips = entries, owners, trips
	}
}

// MergeSource 实现 LedgerRepo。
func (m *LedgerMemoryStore) MergeSource() write.MergeSource { return m.merge }

func (m *LedgerMemoryStore) tripInfo(accountID, tripID uuid.UUID) (LedgerTripInfo, bool) {
	if m.tripOwners[tripID] != accountID {
		return LedgerTripInfo{}, false
	}
	t, ok := m.trips[tripID]
	if !ok {
		return LedgerTripInfo{}, false
	}
	return LedgerTripInfo{
		Timezone: t.Timezone, CurrencyCode: t.CurrencyCode, BudgetAmount: t.BudgetAmount,
		CurrencyLockedAt: t.CurrencyLockedAt, DeletedAt: t.DeletedAt,
	}, true
}

// Trip 实现 LedgerRepo 与 LedgerReader。
func (m *LedgerMemoryStore) Trip(_ context.Context, accountID, tripID uuid.UUID) (LedgerTripInfo, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.tripInfo(accountID, tripID)
	return info, ok, nil
}

// SetTripCurrencyLock 实现 LedgerRepo。
func (m *LedgerMemoryStore) SetTripCurrencyLock(_ context.Context, accountID, tripID uuid.UUID, lockedAt *time.Time, now time.Time) (trip.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tripOwners[tripID] != accountID {
		return trip.Resource{}, errNoLedgerRow
	}
	t, ok := m.trips[tripID]
	if !ok {
		return trip.Resource{}, errNoLedgerRow
	}
	t.CurrencyLockedAt, t.UpdatedAt = lockedAt, now
	t.Version++
	m.trips[tripID] = t
	return t, nil
}

// CategoryActive 实现 LedgerRepo。
func (m *LedgerMemoryStore) CategoryActive(_ context.Context, accountID, categoryID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.catOwners[categoryID] != accountID {
		return false, nil
	}
	c, ok := m.categories[categoryID]
	return ok && c.DeletedAt == nil, nil
}

// MissingAssets 实现 LedgerRepo。
func (m *LedgerMemoryStore) MissingAssets(_ context.Context, accountID, tripID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var missing []uuid.UUID
	for _, id := range ids {
		a, ok := m.assets[id]
		if !ok || a.accountID != accountID || a.tripID != tripID || a.deleted {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

func (m *LedgerMemoryStore) get(accountID, tripID, id uuid.UUID) (LedgerResource, bool) {
	if m.owners[id] != accountID {
		return LedgerResource{}, false
	}
	r, ok := m.entries[id]
	if !ok || r.TripID != tripID {
		return LedgerResource{}, false
	}
	return cloneLedger(r), true
}

func cloneLedger(r LedgerResource) LedgerResource {
	r.AttachmentAssetIDs = append([]uuid.UUID{}, r.AttachmentAssetIDs...)
	return r
}

// Get 实现 LedgerRepo 与 LedgerReader。
func (m *LedgerMemoryStore) Get(_ context.Context, accountID, tripID, id uuid.UUID) (LedgerResource, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.get(accountID, tripID, id)
	return r, ok, nil
}

// GetForUpdate 实现 LedgerRepo。
func (m *LedgerMemoryStore) GetForUpdate(ctx context.Context, accountID, tripID, id uuid.UUID) (LedgerResource, bool, error) {
	return m.Get(ctx, accountID, tripID, id)
}

// IDExists 实现 LedgerRepo。
func (m *LedgerMemoryStore) IDExists(_ context.Context, id uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[id]; ok {
		return true, nil
	}
	_, ok := m.tombstones[id]
	return ok, nil
}

// Insert 实现 LedgerRepo。
func (m *LedgerMemoryStore) Insert(_ context.Context, accountID uuid.UUID, r LedgerResource) (LedgerResource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.AttachmentAssetIDs == nil {
		r.AttachmentAssetIDs = []uuid.UUID{}
	}
	m.entries[r.ID] = cloneLedger(r)
	m.owners[r.ID] = accountID
	return cloneLedger(r), nil
}

type ledgerMemoryError string

func (e ledgerMemoryError) Error() string { return string(e) }

const errNoLedgerRow ledgerMemoryError = "ledger: no row"

func (m *LedgerMemoryStore) mutate(accountID, tripID, id uuid.UUID, fn func(r *LedgerResource)) (LedgerResource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.get(accountID, tripID, id)
	if !ok {
		return LedgerResource{}, errNoLedgerRow
	}
	fn(&r)
	r.Version++
	m.entries[id] = cloneLedger(r)
	return r, nil
}

// Update 实现 LedgerRepo。
func (m *LedgerMemoryStore) Update(_ context.Context, accountID, tripID, id uuid.UUID, v LedgerValues, now time.Time) (LedgerResource, error) {
	return m.mutate(accountID, tripID, id, func(r *LedgerResource) {
		r.Amount, r.SplitCount, r.PersonalAmount = v.Amount, v.SplitCount, v.PersonalAmount
		r.CategoryID, r.OccurredOn, r.Notes = v.CategoryID, v.OccurredOn, v.Notes
		r.RefundedEntryID = v.RefundedEntryID
		r.AttachmentAssetIDs = append([]uuid.UUID{}, v.AttachmentAssetIDs...)
		r.UpdatedAt = now
	})
}

// SoftDelete 实现 LedgerRepo。
func (m *LedgerMemoryStore) SoftDelete(_ context.Context, accountID, tripID, id uuid.UUID, now time.Time) (LedgerResource, error) {
	return m.mutate(accountID, tripID, id, func(r *LedgerResource) {
		t := now
		r.DeletedAt, r.UpdatedAt = &t, now
	})
}

// LinkedRefundsForUpdate 实现 LedgerRepo：按 created_at、id 升序。
func (m *LedgerMemoryStore) LinkedRefundsForUpdate(_ context.Context, accountID, tripID, expenseID uuid.UUID) ([]LedgerResource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []LedgerResource
	for id, r := range m.entries {
		if m.owners[id] != accountID || r.TripID != tripID || r.DeletedAt != nil || r.Kind != KindRefund {
			continue
		}
		if r.RefundedEntryID == nil || *r.RefundedEntryID != expenseID {
			continue
		}
		rows = append(rows, cloneLedger(r))
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.Before(rows[j].CreatedAt)
		}
		return compareLedgerIDs(rows[i].ID, rows[j].ID) < 0
	})
	return rows, nil
}

// CountActive 实现 LedgerRepo。
func (m *LedgerMemoryStore) CountActive(_ context.Context, accountID, tripID uuid.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, r := range m.entries {
		if m.owners[id] == accountID && r.TripID == tripID && r.DeletedAt == nil {
			n++
		}
	}
	return n, nil
}

// compareLedgerIDs 按字节比较 UUID，与 PostgreSQL 的 uuid 排序一致。
func compareLedgerIDs(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }

func inDateRange(on types.Date, from, to *types.Date) bool {
	if from != nil && on.Before(*from) {
		return false
	}
	if to != nil && on.After(*to) {
		return false
	}
	return true
}

// List 实现 LedgerReader。
func (m *LedgerMemoryStore) List(_ context.Context, accountID, tripID uuid.UUID, q LedgerListQuery) ([]LedgerResource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []LedgerResource
	for id, r := range m.entries {
		if m.owners[id] != accountID || r.TripID != tripID || r.DeletedAt != nil {
			continue
		}
		if !inDateRange(r.OccurredOn, q.DateFrom, q.DateTo) {
			continue
		}
		if q.CategoryID != nil && r.CategoryID != *q.CategoryID {
			continue
		}
		if q.Kind != nil && r.Kind != *q.Kind {
			continue
		}
		if q.RefundedEntryID != nil && (r.RefundedEntryID == nil || *r.RefundedEntryID != *q.RefundedEntryID) {
			continue
		}
		if q.After != nil {
			// 降序：位于游标之后意味着日期更早，或同日且 ID 更小。
			if r.OccurredOn != q.After.OccurredOn {
				if !r.OccurredOn.Before(q.After.OccurredOn) {
					continue
				}
			} else if compareLedgerIDs(r.ID, q.After.ID) >= 0 {
				continue
			}
		}
		rows = append(rows, cloneLedger(r))
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].OccurredOn != rows[j].OccurredOn {
			return rows[i].OccurredOn.After(rows[j].OccurredOn)
		}
		return compareLedgerIDs(rows[i].ID, rows[j].ID) > 0
	})
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, nil
}

// Statistics 实现 LedgerReader：用与 SQL 相同的语义在内存中聚合。
func (m *LedgerMemoryStore) Statistics(_ context.Context, accountID, tripID uuid.UUID, q StatisticsQuery) (StatisticsData, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.tripInfo(accountID, tripID)
	if !ok {
		return StatisticsData{}, false, nil
	}
	data := StatisticsData{Trip: info}
	type agg struct{ expense, refund money.Decimal }
	add := func(a *agg, r LedgerResource) {
		amount := r.Amount
		if r.Kind == KindExpense {
			amount = r.PersonalAmount
		}
		d, err := money.ParseDecimal(amount)
		if err != nil {
			return
		}
		if r.Kind == KindExpense {
			a.expense = a.expense.Add(d)
		} else {
			a.refund = a.refund.Add(d)
		}
	}
	var filtered, whole agg
	filteredByCat := map[uuid.UUID]*agg{}
	tripByCat := map[uuid.UUID]*agg{}
	daily := map[types.Date]*agg{}
	referenced := map[uuid.UUID]struct{}{}
	for id, r := range m.entries {
		if m.owners[id] != accountID || r.TripID != tripID || r.DeletedAt != nil {
			continue
		}
		referenced[r.CategoryID] = struct{}{}
		add(&whole, r)
		if tripByCat[r.CategoryID] == nil {
			tripByCat[r.CategoryID] = &agg{}
		}
		add(tripByCat[r.CategoryID], r)
		if !inDateRange(r.OccurredOn, q.DateFrom, q.DateTo) || (q.CategoryID != nil && r.CategoryID != *q.CategoryID) {
			continue
		}
		add(&filtered, r)
		data.FilteredCount++
		if filteredByCat[r.CategoryID] == nil {
			filteredByCat[r.CategoryID] = &agg{}
		}
		add(filteredByCat[r.CategoryID], r)
		if daily[r.OccurredOn] == nil {
			daily[r.OccurredOn] = &agg{}
		}
		add(daily[r.OccurredOn], r)
	}
	fmt4 := func(d money.Decimal) string { return d.Format(money.Scale) }
	data.FilteredExpense, data.FilteredRefund = fmt4(filtered.expense), fmt4(filtered.refund)
	data.TripExpense, data.TripRefund = fmt4(whole.expense), fmt4(whole.refund)

	var cats []CategoryResource
	for id, c := range m.categories {
		if m.catOwners[id] != accountID {
			continue
		}
		if _, used := referenced[id]; c.DeletedAt != nil && !used {
			continue
		}
		cats = append(cats, c)
	}
	sort.Slice(cats, func(i, j int) bool {
		if cats[i].SortOrder != cats[j].SortOrder {
			return cats[i].SortOrder < cats[j].SortOrder
		}
		return compareLedgerIDs(cats[i].ID, cats[j].ID) < 0
	})
	for _, c := range cats {
		f, t := filteredByCat[c.ID], tripByCat[c.ID]
		if f == nil {
			f = &agg{}
		}
		if t == nil {
			t = &agg{}
		}
		data.Categories = append(data.Categories, CategoryAggregate{
			CategoryID: c.ID, Name: c.Name, Icon: c.Icon, SortOrder: c.SortOrder,
			FilteredExpense: fmt4(f.expense), FilteredRefund: fmt4(f.refund),
			TripExpense: fmt4(t.expense), TripRefund: fmt4(t.refund),
		})
	}

	var dates []types.Date
	for d := range daily {
		if q.DailyAfter != nil && !d.Before(*q.DailyAfter) {
			continue
		}
		dates = append(dates, d)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].After(dates[j]) })
	if len(dates) > q.DailyLimit {
		dates = dates[:q.DailyLimit]
	}
	for _, d := range dates {
		data.Daily = append(data.Daily, DailyAggregate{Date: d, Expense: fmt4(daily[d].expense), Refund: fmt4(daily[d].refund)})
	}
	return data, true, nil
}
