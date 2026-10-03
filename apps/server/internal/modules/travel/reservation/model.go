package reservation

import (
	"github.com/google/uuid"
	"time"
	"tripfolio/server/internal/foundation/types"
)

const EntityType = "reservation"

type Resource struct {
	ID               uuid.UUID            `json:"id"`
	TripID           uuid.UUID            `json:"trip_id"`
	Kind             string               `json:"kind"`
	Title            string               `json:"title"`
	BookingReference string               `json:"booking_reference"`
	TransportNumber  *string              `json:"transport_number"`
	ProviderName     *string              `json:"provider_name"`
	StartLocal       *types.LocalDateTime `json:"start_local"`
	EndLocal         *types.LocalDateTime `json:"end_local"`
	Origin           *string              `json:"origin"`
	Destination      *string              `json:"destination"`
	Address          string               `json:"address"`
	ContactName      *string              `json:"contact_name"`
	ContactPhone     *string              `json:"contact_phone"`
	Notes            string               `json:"notes"`
	Version          types.Version        `json:"version"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
	DeletedAt        *time.Time           `json:"deleted_at"`
}

var Fields = []string{"kind", "title", "booking_reference", "transport_number", "provider_name", "start_local", "end_local", "origin", "destination", "address", "contact_name", "contact_phone", "notes"}
