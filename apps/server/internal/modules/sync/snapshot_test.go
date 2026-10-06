package sync

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
)

type snapshotMemory struct {
	meta  Snapshot
	items []SnapshotItem
}

func (m *snapshotMemory) Create(context.Context, actor.Actor, uuid.UUID, SnapshotInput) (Snapshot, error) {
	return m.meta, nil
}
func (m *snapshotMemory) Build(context.Context, uuid.UUID, bool) error { return nil }
func (m *snapshotMemory) Inspect(_ context.Context, _ actor.Actor, _ uuid.UUID, fn func(Snapshot, func(int64, int) ([]SnapshotItem, error)) error) error {
	return fn(m.meta, func(after int64, limit int) ([]SnapshotItem, error) {
		end := min(int(after)+limit, len(m.items))
		return m.items[int(after):end], nil
	})
}

func TestSnapshotCursorPurposeAndCompletion(t *testing.T) {
	s, v, a := syncFixture(t)
	ctx := context.Background()
	high := "9007199254740993"
	now := s.clock.Now()
	m := &snapshotMemory{meta: Snapshot{ID: uuid.New(), Purpose: "baseline", Status: "ready", SchemaVersion: 2, SyncEpoch: v.state.Epoch, HighWaterSeq: &high, ItemCount: "2", CapturedAt: &now, ExpiresAt: now.Add(time.Hour)}, items: []SnapshotItem{{Ordinal: "1"}, {Ordinal: "2"}}}
	s.WithSnapshots(m)
	first, err := s.SnapshotItems(ctx, a, "2", m.meta.ID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || first.NextCursor == nil || first.BaselineCursor != nil {
		t.Fatalf("premature checkpoint: %+v", first)
	}
	last, err := s.SnapshotItems(ctx, a, "2", m.meta.ID, *first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if last.HasMore || last.NextCursor != nil || last.BaselineCursor == nil || last.HighWaterSeq != high {
		t.Fatalf("missing exact completed checkpoint: %+v", last)
	}
	bound, _, _, err := s.decode(a.AccountID, *last.BaselineCursor)
	if err != nil || bound.SnapshotID == nil || *bound.SnapshotID != m.meta.ID || bound.TerminalOrdinal == nil || *bound.TerminalOrdinal != "2" {
		t.Fatalf("missing activation binding: %+v, %v", bound, err)
	}
	_, err = s.SnapshotItems(ctx, a, "2", m.meta.ID, *last.BaselineCursor, 1)
	expectCode(t, err, "INVALID_CURSOR")
	_, err = s.SnapshotItems(ctx, a, "2", uuid.New(), *first.NextCursor, 1)
	expectCode(t, err, "INVALID_CURSOR")
	other := a
	other.AccountID = uuid.New()
	_, err = s.SnapshotItems(ctx, other, "2", m.meta.ID, *first.NextCursor, 1)
	expectCode(t, err, "INVALID_CURSOR")
	high = "9007199254740994"
	_, err = s.SnapshotItems(ctx, a, "2", m.meta.ID, *first.NextCursor, 1)
	expectCode(t, err, "INVALID_CURSOR")
	m.meta.Purpose = "trip_reload"
	page, err := s.SnapshotItems(ctx, a, "2", m.meta.ID, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	if page.BaselineCursor != nil {
		t.Fatal("trip reload advanced global checkpoint")
	}
	m.meta.ExpiresAt = now
	_, err = s.SnapshotItems(ctx, a, "2", m.meta.ID, "", 1)
	expectCode(t, err, "SNAPSHOT_EXPIRED")
	m.meta.ExpiresAt = now.Add(time.Hour)
	m.meta.Status = "invalidated"
	_, err = s.SnapshotItems(ctx, a, "2", m.meta.ID, "", 1)
	expectCode(t, err, "SNAPSHOT_INVALIDATED")
}

func TestEmptySnapshotBaselineBinding(t *testing.T) {
	s, v, a := syncFixture(t)
	now, high := s.clock.Now(), "0"
	m := &snapshotMemory{meta: Snapshot{ID: uuid.New(), Purpose: "baseline", Status: "ready", SchemaVersion: 2, SyncEpoch: v.state.Epoch, HighWaterSeq: &high, ItemCount: "0", CapturedAt: &now, ExpiresAt: now.Add(time.Hour)}}
	s.WithSnapshots(m)
	page, err := s.SnapshotItems(context.Background(), a, "2", m.meta.ID, "", 1)
	if err != nil || page.BaselineCursor == nil || page.HasMore {
		t.Fatalf("empty baseline: %+v %v", page, err)
	}
	c, _, _, err := s.decode(a.AccountID, *page.BaselineCursor)
	if err != nil || c.SnapshotID == nil || *c.SnapshotID != m.meta.ID || c.TerminalOrdinal == nil || *c.TerminalOrdinal != "0" {
		t.Fatalf("empty binding: %+v %v", c, err)
	}
	ordinary, err := s.BaselineCursor(a.AccountID, v.state.Epoch, 0)
	if err != nil {
		t.Fatal(err)
	}
	c, _, _, err = s.decode(a.AccountID, ordinary)
	if err != nil || c.SnapshotID != nil || c.TerminalOrdinal != nil {
		t.Fatalf("legacy checkpoint changed: %+v %v", c, err)
	}
}
