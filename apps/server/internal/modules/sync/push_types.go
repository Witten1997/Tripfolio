package sync

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/write"
)

const MaxPushBytes = 1 << 20
const MaxOperationBytes = 65536
const MaxOperations = 100

type PushInput struct {
	SyncEpoch  uuid.UUID   `json:"sync_epoch"`
	ClientID   uuid.UUID   `json:"client_id"`
	Operations []Operation `json:"operations"`
}

type BaseReference struct {
	Version     *string    `json:"version,omitempty"`
	OperationID *uuid.UUID `json:"operation_id,omitempty"`
}
type GuardReference struct {
	Kind        string     `json:"kind"`
	ScopeID     string     `json:"scope_id"`
	Revision    *string    `json:"revision,omitempty"`
	OperationID *uuid.UUID `json:"operation_id,omitempty"`
}
type Operation struct {
	OperationID uuid.UUID        `json:"operation_id"`
	Type        string           `json:"type"`
	EntityType  string           `json:"entity_type"`
	EntityID    *uuid.UUID       `json:"entity_id"`
	TripID      *uuid.UUID       `json:"trip_id"`
	Base        *BaseReference   `json:"base"`
	Guards      []GuardReference `json:"guards"`
	DependsOn   []uuid.UUID      `json:"depends_on"`
	Payload     json.RawMessage  `json:"payload"`
}

type PreparedOperation struct {
	Operation
	Fingerprint         [32]byte
	BlockedDependencies map[uuid.UUID]bool
}

type PushResult struct {
	OperationID uuid.UUID        `json:"operation_id"`
	Status      string           `json:"status"`
	Result      *OperationResult `json:"result,omitempty"`
	Error       *OperationError  `json:"error,omitempty"`
}
type OperationResult struct {
	References     []write.EntityRef     `json:"references"`
	ScopeRevisions []write.ScopeRevision `json:"scope_revisions"`
	CommitCursor   *string               `json:"commit_cursor"`
	Warnings       []string              `json:"warnings"`
	Data           any                   `json:"data"`
}
type OperationError struct {
	Code       string `json:"code"`
	HTTPStatus int    `json:"http_status"`
	Retryable  bool   `json:"retryable"`
	Detail     string `json:"detail"`
	Conflict   any    `json:"conflict,omitempty"`
}
type PushOutput struct {
	SyncEpoch uuid.UUID    `json:"sync_epoch"`
	Results   []PushResult `json:"results"`
}

type PushRepository interface {
	Execute(context.Context, actor.Actor, uuid.UUID, PreparedOperation) (write.Result, error)
}
