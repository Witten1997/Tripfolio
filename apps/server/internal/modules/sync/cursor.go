package sync

import (
	"strconv"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/paging"
)

const changesScope = "sync:changes:v2"

func (s *Service) commitCursor(owner, epoch, operation uuid.UUID, seq string) (string, error) {
	if n, ok := decimal(seq); !ok || n == 0 {
		return "", paging.ErrInvalidCursor
	}
	return s.codec.Encode(owner, "sync:commit:v2", struct {
		Epoch     uuid.UUID `json:"epoch"`
		Operation uuid.UUID `json:"operation_id"`
		Seq       string    `json:"seq"`
	}{epoch, operation, seq})
}

type cursor struct {
	Protocol        int        `json:"protocol"`
	Epoch           uuid.UUID  `json:"epoch"`
	Purpose         string     `json:"purpose"`
	After           string     `json:"after_seq"`
	Upper           *string    `json:"through_seq,omitempty"`
	SnapshotID      *uuid.UUID `json:"snapshot_id,omitempty"`
	TerminalOrdinal *string    `json:"terminal_ordinal,omitempty"`
}

// completedBaselineCursor binds activation evidence to this published snapshot.
// It remains a changes checkpoint, but ordinary checkpoints cannot activate sync.
func (s *Service) completedBaselineCursor(owner uuid.UUID, meta Snapshot, high, end int64) (string, error) {
	if owner == uuid.Nil || meta.ID == uuid.Nil || meta.SyncEpoch == uuid.Nil || high < 0 || end < 0 {
		return "", paging.ErrInvalidCursor
	}
	ordinal := strconv.FormatInt(end, 10)
	return s.codec.Encode(owner, changesScope, cursor{
		Protocol: ProtocolVersion, Epoch: meta.SyncEpoch, Purpose: "checkpoint",
		After: strconv.FormatInt(high, 10), SnapshotID: &meta.ID, TerminalOrdinal: &ordinal,
	})
}

func decimal(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n >= 0 && strconv.FormatInt(n, 10) == s
}

// BaselineCursor is only for the snapshot publisher after a complete baseline.
// It is deliberately not exposed as an HTTP operation or a status field.
func (s *Service) BaselineCursor(accountID, epoch uuid.UUID, highWater int64) (string, error) {
	if accountID == uuid.Nil || epoch == uuid.Nil || highWater < 0 {
		return "", paging.ErrInvalidCursor
	}
	return s.codec.Encode(accountID, changesScope, cursor{
		Protocol: ProtocolVersion, Epoch: epoch, Purpose: "checkpoint", After: strconv.FormatInt(highWater, 10),
	})
}

func (s *Service) decode(accountID uuid.UUID, token string) (cursor, int64, *int64, error) {
	var c cursor
	if token == "" || len(token) > 8192 || s.codec.Decode(accountID, changesScope, token, &c) != nil {
		return c, 0, nil, paging.InvalidCursor()
	}
	after, ok := decimal(c.After)
	if !ok || c.Protocol != ProtocolVersion || c.Epoch == uuid.Nil {
		return c, 0, nil, paging.InvalidCursor()
	}
	switch c.Purpose {
	case "checkpoint":
		if c.Upper == nil {
			return c, after, nil, nil
		}
	case "page":
		if c.Upper != nil {
			h, valid := decimal(*c.Upper)
			if valid && h > after {
				return c, after, &h, nil
			}
		}
	}
	return c, 0, nil, paging.InvalidCursor()
}
