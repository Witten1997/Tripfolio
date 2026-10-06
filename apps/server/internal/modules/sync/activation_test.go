package sync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type activationMemory struct {
	meta     Snapshot
	epoch    uuid.UUID
	retained int64
	now      time.Time
	writes   int
}

func (m *activationMemory) Activate(_ context.Context, owner, _ uuid.UUID, validate func(Snapshot, uuid.UUID, int64, time.Time) error) (Activation, error) {
	if err := validate(m.meta, m.epoch, m.retained, m.now); err != nil {
		return Activation{}, err
	}
	m.writes++
	return Activation{AccountID: owner, SyncEpoch: m.epoch, EnabledAt: m.now}, nil
}

type testReleaseVerifier struct {
	err   error
	calls int
}

func (v *testReleaseVerifier) Verify(context.Context) error { v.calls++; return v.err }

func TestActivationBoundBaseline(t *testing.T) {
	s, v, a := syncFixture(t)
	now, high := s.clock.Now(), "9007199254740993"
	base := Snapshot{ID: uuid.New(), Purpose: "baseline", Status: "ready", SchemaVersion: 2, SyncEpoch: v.state.Epoch, HighWaterSeq: &high, ItemCount: "2", CapturedAt: &now, ExpiresAt: now.Add(time.Hour)}
	for _, tc := range []struct {
		name   string
		change func(*activationMemory, *cursor)
	}{
		{"ordinary_checkpoint", func(_ *activationMemory, c *cursor) { c.SnapshotID = nil; c.TerminalOrdinal = nil }},
		{"missing_terminal", func(_ *activationMemory, c *cursor) { c.TerminalOrdinal = nil }},
		{"other_snapshot", func(_ *activationMemory, c *cursor) { id := uuid.New(); c.SnapshotID = &id }},
		{"old_epoch", func(m *activationMemory, _ *cursor) { m.epoch = uuid.New() }},
		{"zero_epoch", func(m *activationMemory, c *cursor) { m.epoch = uuid.Nil; c.Epoch = uuid.Nil }},
		{"protocol", func(_ *activationMemory, c *cursor) { c.Protocol = 1 }},
		{"page", func(_ *activationMemory, c *cursor) { c.Purpose = "page"; c.Upper = &high }},
		{"upper", func(_ *activationMemory, c *cursor) { c.Upper = &high }},
		{"trip_reload", func(m *activationMemory, _ *cursor) { m.meta.Purpose = "trip_reload" }},
		{"invalidated", func(m *activationMemory, _ *cursor) { m.meta.Status = "invalidated" }},
		{"building", func(m *activationMemory, _ *cursor) { m.meta.Status = "building" }},
		{"schema", func(m *activationMemory, _ *cursor) { m.meta.SchemaVersion = 1 }},
		{"expired", func(m *activationMemory, _ *cursor) { m.meta.ExpiresAt = now }},
		{"extended_ttl", func(m *activationMemory, _ *cursor) { m.meta.ExpiresAt = now.Add(25 * time.Hour) }},
		{"no_capture", func(m *activationMemory, _ *cursor) { m.meta.CapturedAt = nil }},
		{"future_capture", func(m *activationMemory, _ *cursor) { future := now.Add(time.Minute); m.meta.CapturedAt = &future }},
		{"no_high", func(m *activationMemory, _ *cursor) { m.meta.HighWaterSeq = nil }},
		{"high_mismatch", func(_ *activationMemory, c *cursor) { c.After = "9007199254740992" }},
		{"retention", func(m *activationMemory, _ *cursor) { m.retained = 9007199254740994 }},
		{"negative_retention", func(m *activationMemory, _ *cursor) { m.retained = -1 }},
		{"incomplete", func(_ *activationMemory, c *cursor) { x := "1"; c.TerminalOrdinal = &x }},
		{"noncanonical", func(_ *activationMemory, c *cursor) { x := "02"; c.TerminalOrdinal = &x }},
		{"overflow", func(_ *activationMemory, c *cursor) { x := "9223372036854775808"; c.TerminalOrdinal = &x }},
		{"count", func(m *activationMemory, _ *cursor) { m.meta.ItemCount = "3" }},
		{"meta_epoch", func(m *activationMemory, _ *cursor) { m.meta.SyncEpoch = uuid.New() }},
		{"meta_id", func(m *activationMemory, _ *cursor) { m.meta.ID = uuid.New() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &activationMemory{meta: base, epoch: base.SyncEpoch, now: now}
			terminal := "2"
			c := cursor{Protocol: 2, Epoch: base.SyncEpoch, Purpose: "checkpoint", After: high, SnapshotID: &base.ID, TerminalOrdinal: &terminal}
			tc.change(m, &c)
			proof, err := s.codec.Encode(a.AccountID, changesScope, c)
			if err != nil {
				t.Fatal(err)
			}
			release := &testReleaseVerifier{}
			_, err = NewActivationService(m, s.codec, release).Activate(context.Background(), a.AccountID, base.ID, proof)
			if err == nil || m.writes != 0 || release.calls != 0 {
				t.Fatalf("invalid proof accepted or reached release: err=%v writes=%d release=%d", err, m.writes, release.calls)
			}
		})
	}
	for _, count := range []int64{0, 2} {
		m := &activationMemory{meta: base, epoch: base.SyncEpoch, now: now, retained: 9007199254740993}
		if count == 0 {
			m.meta.ItemCount = "0"
		}
		proof, err := s.completedBaselineCursor(a.AccountID, m.meta, 9007199254740993, count)
		if err != nil {
			t.Fatal(err)
		}
		release := &testReleaseVerifier{}
		result, err := NewActivationService(m, s.codec, release).Activate(context.Background(), a.AccountID, base.ID, proof)
		if err != nil || m.writes != 1 || release.calls != 1 || result.SyncEpoch != base.SyncEpoch {
			t.Fatalf("valid activation: result=%+v err=%v", result, err)
		}
		if _, _, _, err = s.decode(a.AccountID, proof); err != nil {
			t.Fatalf("changes compatibility: %v", err)
		}
		for _, bad := range []string{"", strings.Repeat("x", 8193), proof + "!"} {
			_, err = NewActivationService(m, s.codec, release).Activate(context.Background(), a.AccountID, base.ID, bad)
			if err == nil || m.writes != 1 {
				t.Fatal("bad proof wrote")
			}
		}
		_, err = NewActivationService(m, s.codec, release).Activate(context.Background(), uuid.New(), base.ID, proof)
		if err == nil || m.writes != 1 {
			t.Fatal("other account accepted")
		}
		for _, verifier := range []ReleaseVerifier{nil, &testReleaseVerifier{err: errors.New("secret verifier detail")}} {
			_, err = NewActivationService(m, s.codec, verifier).Activate(context.Background(), a.AccountID, base.ID, proof)
			expectCode(t, err, "SYNC_NOT_READY")
			if m.writes != 1 || strings.Contains(err.Error(), "secret") {
				t.Fatal("release denial leaked or wrote")
			}
		}
	}
	var missing *ActivationService
	_, err := missing.Activate(context.Background(), a.AccountID, base.ID, "proof")
	expectCode(t, err, "SYNC_NOT_READY")
}
