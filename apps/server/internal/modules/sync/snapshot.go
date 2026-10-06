package sync

import (
	"context"
	"sort"
	"strconv"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/paging"
)

const snapshotScope = "sync:snapshot:v2"

type snapshotCursor struct {
	Snapshot uuid.UUID `json:"snapshot"`
	Epoch    uuid.UUID `json:"epoch"`
	High     string    `json:"high_water_seq"`
	After    string    `json:"after_ordinal"`
}

func (s *Service) WithSnapshots(repo SnapshotRepository) *Service { s.snapshots = repo; return s }

func (s *Service) snapshotRequest(a actor.Actor, protocol string) error {
	if err := nativeActor(a); err != nil {
		return err
	}
	if protocol != "2" {
		return apperr.New(426, "SYNC_PROTOCOL_UNSUPPORTED", "需要同步协议版本 2")
	}
	if s.snapshots == nil {
		return apperr.New(503, "DEPENDENCY_UNAVAILABLE", "快照服务尚不可用")
	}
	return nil
}

func (s *Service) CreateSnapshot(ctx context.Context, a actor.Actor, protocol string, operation uuid.UUID, in SnapshotInput) (Snapshot, error) {
	if err := s.snapshotRequest(a, protocol); err != nil {
		return Snapshot{}, err
	}
	if operation == uuid.Nil || in.SyncEpoch == uuid.Nil || (in.Purpose != "baseline" && in.Purpose != "trip_reload") || in.SelectedTripIDs == nil {
		return Snapshot{}, apperr.Validation(apperr.Field("snapshot", "INVALID", "请提供同步代次、用途及旅行范围"))
	}
	ids := make([]uuid.UUID, 0, len(in.SelectedTripIDs))
	seen := make(map[uuid.UUID]bool)
	for _, id := range in.SelectedTripIDs {
		if id == uuid.Nil {
			return Snapshot{}, apperr.NotFound()
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	if in.Purpose == "trip_reload" && len(ids) == 0 {
		return Snapshot{}, apperr.Validation(apperr.Field("selected_trip_ids", "REQUIRED", "详情快照至少选择一趟旅行"))
	}
	in.SelectedTripIDs = ids
	return s.snapshots.Create(ctx, a, operation, in)
}

func (s *Service) snapshotStatus(snapshot Snapshot) Snapshot {
	if (snapshot.Status == "ready" || snapshot.Status == "queued" || snapshot.Status == "building") && !s.clock.Now().Before(snapshot.ExpiresAt) {
		snapshot.Status = "expired"
	}
	return snapshot
}

func (s *Service) GetSnapshot(ctx context.Context, a actor.Actor, protocol string, id uuid.UUID) (Snapshot, error) {
	var out Snapshot
	if err := s.snapshotRequest(a, protocol); err != nil {
		return out, err
	}
	err := s.snapshots.Inspect(ctx, a, id, func(meta Snapshot, _ func(int64, int) ([]SnapshotItem, error)) error {
		out = s.snapshotStatus(meta)
		return nil
	})
	return out, err
}

func (s *Service) SnapshotItems(ctx context.Context, a actor.Actor, protocol string, id uuid.UUID, token string, limit int) (SnapshotPage, error) {
	var out SnapshotPage
	if err := s.snapshotRequest(a, protocol); err != nil {
		return out, err
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return out, apperr.Validation(apperr.Field("limit", "INVALID", "limit 须在 1–500 之间"))
	}
	var cursor snapshotCursor
	var after int64
	if token != "" {
		if len(token) > 8192 || s.codec.Decode(a.AccountID, snapshotScope, token, &cursor) != nil || cursor.Snapshot != id {
			return out, paging.InvalidCursor()
		}
		var ok bool
		after, ok = decimal(cursor.After)
		if !ok {
			return out, paging.InvalidCursor()
		}
	}
	err := s.snapshots.Inspect(ctx, a, id, func(meta Snapshot, items func(int64, int) ([]SnapshotItem, error)) error {
		meta = s.snapshotStatus(meta)
		switch meta.Status {
		case "expired":
			return apperr.Gone("SNAPSHOT_EXPIRED", "快照已过期，请重新创建")
		case "invalidated":
			return apperr.Gone("SNAPSHOT_INVALIDATED", "快照已失效，请重新创建")
		case "ready":
		default:
			return apperr.Conflicted("SNAPSHOT_NOT_READY", "快照尚未就绪")
		}
		count, ok := decimal(meta.ItemCount)
		if !ok || meta.HighWaterSeq == nil || meta.CapturedAt == nil || meta.SchemaVersion != ProtocolVersion {
			return unavailableProjection()
		}
		high, ok := decimal(*meta.HighWaterSeq)
		if !ok {
			return unavailableProjection()
		}
		if after > count || (token != "" && (cursor.Epoch != meta.SyncEpoch || cursor.High != *meta.HighWaterSeq)) {
			return paging.InvalidCursor()
		}
		rows, err := items(after, limit)
		if err != nil {
			return err
		}
		for i, row := range rows {
			if row.Ordinal != strconv.FormatInt(after+int64(i)+1, 10) {
				return unavailableProjection()
			}
		}
		end := after + int64(len(rows))
		if end > count || (end < count && len(rows) < limit) {
			return unavailableProjection()
		}
		out = SnapshotPage{SnapshotID: id, SyncEpoch: meta.SyncEpoch, Items: rows, ItemCount: meta.ItemCount, HighWaterSeq: *meta.HighWaterSeq, HasMore: end < count}
		if out.HasMore {
			next, err := s.codec.Encode(a.AccountID, snapshotScope, snapshotCursor{id, meta.SyncEpoch, *meta.HighWaterSeq, strconv.FormatInt(end, 10)})
			if err != nil {
				return apperr.Internal(err)
			}
			out.NextCursor = &next
		} else if meta.Purpose == "baseline" {
			next, err := s.completedBaselineCursor(a.AccountID, meta, high, end)
			if err != nil {
				return apperr.Internal(err)
			}
			out.BaselineCursor = &next
		}
		return nil
	})
	if err != nil {
		return SnapshotPage{}, err
	}
	return out, nil
}

func (s *Service) BuildSnapshot(ctx context.Context, id uuid.UUID, lastAttempt bool) error {
	if s.snapshots == nil {
		return apperr.New(503, "DEPENDENCY_UNAVAILABLE", "快照服务尚不可用")
	}
	return s.snapshots.Build(ctx, id, lastAttempt)
}
