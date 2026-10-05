package sync

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
)

type SnapshotInput struct {
	SyncEpoch       uuid.UUID   `json:"sync_epoch"`
	Purpose         string      `json:"purpose"`
	SelectedTripIDs []uuid.UUID `json:"selected_trip_ids"`
}

type Snapshot struct {
	ID              uuid.UUID   `json:"id"`
	Purpose         string      `json:"purpose"`
	SelectedTripIDs []uuid.UUID `json:"selected_trip_ids"`
	Status          string      `json:"status"`
	SchemaVersion   int         `json:"schema_version"`
	SyncEpoch       uuid.UUID   `json:"sync_epoch"`
	HighWaterSeq    *string     `json:"high_water_seq"`
	ItemCount       string      `json:"item_count"`
	CapturedAt      *time.Time  `json:"captured_at"`
	ExpiresAt       time.Time   `json:"expires_at"`
	ErrorCode       *string     `json:"error_code"`
}

type SnapshotItem struct {
	Ordinal    string          `json:"ordinal"`
	EntityType string          `json:"entity_type"`
	EntityID   uuid.UUID       `json:"entity_id"`
	TripID     *uuid.UUID      `json:"trip_id"`
	Version    string          `json:"version"`
	Data       json.RawMessage `json:"data"`
}

type SnapshotPage struct {
	SnapshotID     uuid.UUID      `json:"snapshot_id"`
	SyncEpoch      uuid.UUID      `json:"sync_epoch"`
	Items          []SnapshotItem `json:"items"`
	NextCursor     *string        `json:"next_cursor"`
	HasMore        bool           `json:"has_more"`
	ItemCount      string         `json:"item_count"`
	HighWaterSeq   string         `json:"high_water_seq"`
	BaselineCursor *string        `json:"baseline_cursor"`
}

// Inspect keeps current authorization and snapshot validity stable while reading a page.
type SnapshotRepository interface {
	Create(context.Context, actor.Actor, uuid.UUID, SnapshotInput) (Snapshot, error)
	Inspect(context.Context, actor.Actor, uuid.UUID, func(Snapshot, func(int64, int) ([]SnapshotItem, error)) error) error
	Build(context.Context, uuid.UUID, bool) error
}

type SnapshotArgs struct {
	ID uuid.UUID `json:"id"`
}

func (SnapshotArgs) Kind() string { return "sync_snapshot" }
