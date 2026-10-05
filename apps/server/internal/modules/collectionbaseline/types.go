package collectionbaseline

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/write"
)

type Query struct {
	Kind, ScopeID, Cursor string
	Limit                 int
}

type Page struct {
	Kind       string            `json:"kind"`
	ScopeID    string            `json:"scope_id"`
	SyncEpoch  uuid.UUID         `json:"sync_epoch"`
	Revision   string            `json:"revision"`
	ExpiresAt  time.Time         `json:"expires_at"`
	Items      []json.RawMessage `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

type State struct {
	Epoch         uuid.UUID
	AccountStatus string
	SessionActive bool
}

// Entry keeps the database identity/version alongside its canonical projection.
type Entry struct {
	ID      uuid.UUID
	Version int64
	Data    json.RawMessage
}

// Read must run the callback in one repeatable-read, read-only transaction.
type Reader interface {
	Read(context.Context, actor.Actor, func(View) error) error
}

type View interface {
	State(context.Context) (State, error)
	Revision(context.Context, uuid.UUID, collectionguard.Scope) (write.ScopeRevision, error)
	Entries(context.Context, collectionguard.Scope, uuid.UUID, int) ([]Entry, error)
}
