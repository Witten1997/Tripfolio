package sync

import (
	"context"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/paging"
)

// ReleaseVerifier must verify the accepted running artifact, including its Web
// assets. A concrete production verifier is required before wiring activation.
type ReleaseVerifier interface {
	Verify(context.Context) error
}

type Activation struct {
	AccountID uuid.UUID
	SyncEpoch uuid.UUID
	EnabledAt time.Time
}

// Activate holds the account, sync state and snapshot locks through validation
// and commit. Implementations also verify the stored item count and ordinals.
type ActivationRepository interface {
	Activate(context.Context, uuid.UUID, uuid.UUID, func(Snapshot, uuid.UUID, int64, time.Time) error) (Activation, error)
}

type ActivationService struct {
	store   ActivationRepository
	codec   paging.Codec
	release ReleaseVerifier
}

func NewActivationService(store ActivationRepository, codec paging.Codec, release ReleaseVerifier) *ActivationService {
	return &ActivationService{store: store, codec: codec, release: release}
}

func activationNotReady() error {
	return apperr.New(503, "SYNC_NOT_READY", "同步激活条件尚未满足")
}

// Activate is an explicit operator action, not an HTTP operation. The proof
// establishes a completed server baseline, not that a client applied its data.
func (s *ActivationService) Activate(ctx context.Context, owner, snapshotID uuid.UUID, proof string) (Activation, error) {
	if s == nil || s.store == nil || s.codec == nil || s.release == nil {
		return Activation{}, activationNotReady()
	}
	if owner == uuid.Nil || snapshotID == uuid.Nil || len(proof) == 0 || len(proof) > 8192 {
		return Activation{}, paging.InvalidCursor()
	}
	return s.store.Activate(ctx, owner, snapshotID, func(meta Snapshot, epoch uuid.UUID, retained int64, now time.Time) error {
		var c cursor
		if s.codec.Decode(owner, changesScope, proof, &c) != nil ||
			c.Protocol != ProtocolVersion || c.Purpose != "checkpoint" || c.Upper != nil ||
			c.Epoch == uuid.Nil || c.Epoch != epoch || c.SnapshotID == nil || *c.SnapshotID != snapshotID || c.TerminalOrdinal == nil {
			return paging.InvalidCursor()
		}
		high, highOK := decimal(c.After)
		end, endOK := decimal(*c.TerminalOrdinal)
		count, countOK := decimal(meta.ItemCount)
		if !highOK || !endOK || !countOK || meta.ID != snapshotID || meta.SyncEpoch != epoch ||
			meta.Purpose != "baseline" || meta.Status != "ready" || meta.SchemaVersion != ProtocolVersion ||
			meta.HighWaterSeq == nil || *meta.HighWaterSeq != c.After || count != end || retained < 0 || high < retained ||
			meta.CapturedAt == nil || meta.CapturedAt.IsZero() || now.IsZero() || meta.CapturedAt.After(now) ||
			!now.Before(meta.ExpiresAt) || !meta.CapturedAt.Before(meta.ExpiresAt) ||
			meta.ExpiresAt.After(meta.CapturedAt.Add(24*time.Hour)) {
			return activationNotReady()
		}
		if err := s.release.Verify(ctx); err != nil {
			// Neither proof nor verifier internals belong in operator error output.
			return activationNotReady()
		}
		return nil
	})
}
