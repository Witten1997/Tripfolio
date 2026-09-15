package assets

import (
	"context"
	"encoding/hex"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore 是 Repo 与 Reader 的内存实现，供本包与传输层单元测试使用；
// 配合 write.MemoryUnitOfWork 可回滚。旅行由测试通过 PutTrip 登记。
type MemoryStore struct {
	mu         sync.Mutex
	assets     map[uuid.UUID]Resource
	owners     map[uuid.UUID]uuid.UUID
	attempts   map[uuid.UUID]AttemptInfo
	staging    map[uuid.UUID]string
	finalKeys  map[uuid.UUID]string
	thumbKeys  map[uuid.UUID]string
	trips      map[uuid.UUID]TripInfo
	tripOwners map[uuid.UUID]uuid.UUID
	tombstones map[uuid.UUID]struct{}
}

// NewMemoryStore 创建空存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		assets: map[uuid.UUID]Resource{}, owners: map[uuid.UUID]uuid.UUID{},
		attempts: map[uuid.UUID]AttemptInfo{}, staging: map[uuid.UUID]string{},
		finalKeys: map[uuid.UUID]string{}, thumbKeys: map[uuid.UUID]string{},
		trips: map[uuid.UUID]TripInfo{}, tripOwners: map[uuid.UUID]uuid.UUID{},
		tombstones: map[uuid.UUID]struct{}{},
	}
}

var (
	_ Repo   = (*MemoryStore)(nil)
	_ Reader = (*MemoryStore)(nil)
)

// PutTrip 登记一趟旅行，供测试构造上下文。
func (m *MemoryStore) PutTrip(accountID, tripID uuid.UUID, info TripInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trips[tripID] = info
	m.tripOwners[tripID] = accountID
}

// PutAsset 直接登记一个资产，供测试构造 ready、failed 等状态。
func (m *MemoryStore) PutAsset(accountID uuid.UUID, res Resource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assets[res.ID] = res
	m.owners[res.ID] = accountID
}

// PutAttempt 登记声明信息，供测试构造 worker 输入。
func (m *MemoryStore) PutAttempt(assetID uuid.UUID, info AttemptInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[assetID] = info
}

// StagingKey 返回资产当前的暂存键，供测试核对续签与新尝试的区别。
func (m *MemoryStore) StagingKey(assetID uuid.UUID) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.staging[assetID]
}

// FinalKey 返回校验通过后写入的最终键。
func (m *MemoryStore) FinalKey(assetID uuid.UUID) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.finalKeys[assetID]
}

// ThumbnailKey 返回缩略图键。
func (m *MemoryStore) ThumbnailKey(assetID uuid.UUID) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.thumbKeys[assetID]
}

// PutTombstone 登记一个已永久清理的 ID。
func (m *MemoryStore) PutTombstone(id uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tombstones[id] = struct{}{}
}

// IDExists 实现 Repo。
func (m *MemoryStore) IDExists(_ context.Context, id uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.assets[id]; ok {
		return true, nil
	}
	_, ok := m.tombstones[id]
	return ok, nil
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

// Insert 实现 Repo。
func (m *MemoryStore) Insert(_ context.Context, accountID uuid.UUID, res Resource, attempt AttemptInfo, stagingKey string) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assets[res.ID] = res
	m.owners[res.ID] = accountID
	m.attempts[res.ID] = attempt
	m.staging[res.ID] = stagingKey
	return res, nil
}

// Attempt 实现 Reader。
func (m *MemoryStore) Attempt(_ context.Context, accountID, assetID uuid.UUID) (AttemptInfo, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owners[assetID] != accountID {
		return AttemptInfo{}, false, nil
	}
	info, ok := m.attempts[assetID]
	return info, ok, nil
}

// GetForUpdate 实现 Repo。
func (m *MemoryStore) GetForUpdate(_ context.Context, accountID, assetID uuid.UUID) (Resource, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owners[assetID] != accountID {
		return Resource{}, false, nil
	}
	res, ok := m.assets[assetID]
	return res, ok, nil
}

// StartAttempt 实现 Repo：递增尝试序号并换用新暂存键。
func (m *MemoryStore) StartAttempt(_ context.Context, accountID, assetID uuid.UUID, attempt int32, stagingKey string, expiresAt, now time.Time) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := m.assets[assetID]
	res.Status = StatusUploading
	res.UploadAttempt = attempt
	res.UploadExpiresAt = &expiresAt
	// 新尝试清掉上一次的失败原因，避免旧错误码留在资源上。
	res.ErrorCode = nil
	res.Version++
	res.UpdatedAt = now
	m.assets[assetID] = res
	m.staging[assetID] = stagingKey
	return res, nil
}

// RenewAttempt 实现 Repo：只延长截止时间，暂存键与序号不变。
func (m *MemoryStore) RenewAttempt(_ context.Context, accountID, assetID uuid.UUID, expiresAt, now time.Time) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := m.assets[assetID]
	res.UploadExpiresAt = &expiresAt
	res.Version++
	res.UpdatedAt = now
	m.assets[assetID] = res
	return res, nil
}

// MarkProcessing 实现 Repo。
func (m *MemoryStore) MarkProcessing(_ context.Context, accountID, assetID uuid.UUID, now time.Time) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := m.assets[assetID]
	res.Status = StatusProcessing
	// 进入 processing 后不再有确认窗口。
	res.UploadExpiresAt = nil
	res.Version++
	res.UpdatedAt = now
	m.assets[assetID] = res
	return res, nil
}

// MarkReady 实现 Repo。
func (m *MemoryStore) MarkReady(_ context.Context, accountID, assetID uuid.UUID, info VerifiedInfo, now time.Time) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := m.assets[assetID]
	res.Status = StatusReady
	res.UploadExpiresAt = nil
	res.ErrorCode = nil
	mediaType := info.MediaType
	res.MediaType = &mediaType
	size := info.ByteSize
	res.ByteSize = &size
	sum := hex.EncodeToString(info.SHA256)
	res.SHA256 = &sum
	res.ThumbnailStatus = ThumbnailNone
	if info.Image != nil {
		w, h := info.Image.Width, info.Image.Height
		res.Width, res.Height = &w, &h
		res.ExifTakenAt = info.Image.TakenAt
		res.ExifLatitude, res.ExifLongitude = info.Image.Latitude, info.Image.Longitude
		res.ThumbnailStatus = ThumbnailProcessing
	}
	res.Version++
	res.UpdatedAt = now
	m.assets[assetID] = res
	m.finalKeys[assetID] = info.FinalKey
	// ready 后暂存键必须为空（数据库 CHECK）。
	delete(m.staging, assetID)
	return res, nil
}

// MarkFailed 实现 Repo。
func (m *MemoryStore) MarkFailed(_ context.Context, accountID, assetID uuid.UUID, errorCode string, now time.Time) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := m.assets[assetID]
	res.Status = StatusFailed
	res.UploadExpiresAt = nil
	res.ErrorCode = &errorCode
	res.Version++
	res.UpdatedAt = now
	m.assets[assetID] = res
	delete(m.staging, assetID)
	return res, nil
}

// SetThumbnail 实现 Repo。
func (m *MemoryStore) SetThumbnail(_ context.Context, accountID, assetID uuid.UUID, status ThumbnailStatus, thumbnailKey string, now time.Time) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := m.assets[assetID]
	res.ThumbnailStatus = status
	res.Version++
	res.UpdatedAt = now
	m.assets[assetID] = res
	if thumbnailKey != "" {
		m.thumbKeys[assetID] = thumbnailKey
	}
	return res, nil
}

// Get 实现 Reader。
func (m *MemoryStore) Get(_ context.Context, accountID, assetID uuid.UUID) (Resource, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owners[assetID] != accountID {
		return Resource{}, false, nil
	}
	res, ok := m.assets[assetID]
	return res, ok, nil
}

// GetMany 实现 Reader：只返回属于本账号的资产，顺序与输入一致。
func (m *MemoryStore) GetMany(_ context.Context, accountID uuid.UUID, ids []uuid.UUID) ([]Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Resource, 0, len(ids))
	for _, id := range ids {
		if m.owners[id] != accountID {
			continue
		}
		if res, ok := m.assets[id]; ok {
			out = append(out, res)
		}
	}
	return out, nil
}

// ListByTrip 实现 Reader：按 created_at、id 均降序，应用状态与 ID 筛选及键集分页。
func (m *MemoryStore) ListByTrip(_ context.Context, accountID, tripID uuid.UUID, q ListQuery) ([]Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idSet := map[uuid.UUID]struct{}{}
	for _, id := range q.IDs {
		idSet[id] = struct{}{}
	}

	var rows []Resource
	for id, res := range m.assets {
		if m.owners[id] != accountID || res.TripID == nil || *res.TripID != tripID {
			continue
		}
		if res.DeletedAt != nil {
			continue
		}
		if q.Status != nil && res.Status != *q.Status {
			continue
		}
		if len(idSet) > 0 {
			if _, ok := idSet[id]; !ok {
				continue
			}
		}
		rows = append(rows, res)
	}

	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].ID.String() > rows[j].ID.String()
	})

	if q.After != nil {
		filtered := rows[:0]
		for _, r := range rows {
			after := r.CreatedAt.Before(q.After.CreatedAt) ||
				(r.CreatedAt.Equal(q.After.CreatedAt) && r.ID.String() < q.After.ID.String())
			if after {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, nil
}
