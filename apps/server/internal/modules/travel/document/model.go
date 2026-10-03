package document

import (
	"github.com/google/uuid"
	"time"
	"tripfolio/server/internal/foundation/types"
)

const EntityType = "document"

type Resource struct {
	ID            uuid.UUID     `json:"id"`
	TripID        uuid.UUID     `json:"trip_id"`
	Title         string        `json:"title"`
	Notes         string        `json:"notes"`
	AssetID       uuid.UUID     `json:"asset_id"`
	ReservationID *uuid.UUID    `json:"reservation_id"`
	Version       types.Version `json:"version"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	DeletedAt     *time.Time    `json:"deleted_at"`
}

var Fields = []string{"title", "notes", "asset_id", "reservation_id"}
