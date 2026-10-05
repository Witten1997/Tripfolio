// Package sync implements the versioned account synchronization protocol.
package sync

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
)

const ProtocolVersion = 2
const DefaultLimit = 200
const MaxLimit = 500

type Limits struct {
	MaxOperations     int `json:"max_operations"`
	MaxOperationBytes int `json:"max_operation_bytes"`
	MaxRequestBytes   int `json:"max_request_bytes"`
	MaxDependencies   int `json:"max_dependencies"`
}

type Status struct {
	SupportedProtocolVersions  []int     `json:"supported_protocol_versions"`
	RecommendedProtocolVersion int       `json:"recommended_protocol_version"`
	SyncEpoch                  uuid.UUID `json:"sync_epoch"`
	ServerTime                 time.Time `json:"server_time"`
	RetentionDays              int       `json:"retention_days"`
	SnapshotTTLSeconds         int       `json:"snapshot_ttl_seconds"`
	Limits                     Limits    `json:"limits"`
}

type Change struct {
	Seq              string          `json:"seq"`
	BatchID          uuid.UUID       `json:"batch_id"`
	BatchEndSeq      string          `json:"batch_end_seq"`
	EntityType       string          `json:"entity_type"`
	EntityID         uuid.UUID       `json:"entity_id"`
	TripID           *uuid.UUID      `json:"trip_id"`
	Version          string          `json:"version"`
	Kind             string          `json:"kind"`
	SchemaVersion    int             `json:"schema_version"`
	RequiresSnapshot bool            `json:"requires_snapshot"`
	ChangedFields    []string        `json:"changed_fields"`
	Data             json.RawMessage `json:"data"`
}

type Page struct {
	SyncEpoch  uuid.UUID `json:"sync_epoch"`
	Changes    []Change  `json:"changes"`
	NextCursor string    `json:"next_cursor"`
	HasMore    bool      `json:"has_more"`
}

// State and Event keep database integers exact until their wire conversion.
type State struct {
	Epoch                     uuid.UUID
	LastSeq, RetainedAfterSeq int64
	AccountStatus             string
	SessionActive             bool
}

type Event struct {
	Seq, BatchEndSeq, Version int64
	BatchID, EntityID         uuid.UUID
	TripID                    *uuid.UUID
	EntityType, Kind          string
	SchemaVersion             int
	RequiresSnapshot          bool
	ChangedFields             []string
	Data                      json.RawMessage
}

// A reader holds the state and all page rows in one immutable database view.
type View interface {
	State(context.Context) (State, error)
	Changes(context.Context, int64, int64, int) ([]Event, error)
}

type Reader interface {
	Read(context.Context, actor.Actor, func(View) error) error
}
