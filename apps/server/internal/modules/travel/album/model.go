package album

import (
	"github.com/google/uuid"
	"time"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/assets"
)

const EntityType = "photo"

type Resource struct {
	ID           uuid.UUID            `json:"id"`
	TripID       uuid.UUID            `json:"trip_id"`
	AssetID      uuid.UUID            `json:"asset_id"`
	TakenAtLocal *types.LocalDateTime `json:"taken_at_local"`
	RecordedOn   types.Date           `json:"recorded_on"`
	Caption      string               `json:"caption"`
	SortOrder    int32                `json:"sort_order"`
	PlaceName    string               `json:"place_name"`
	Address      string               `json:"address"`
	Latitude     *float64             `json:"latitude"`
	Longitude    *float64             `json:"longitude"`
	Version      types.Version        `json:"version"`
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
	DeletedAt    *time.Time           `json:"deleted_at"`
}

var Fields = []string{"asset_id", "taken_at_local", "recorded_on", "caption", "sort_order", "place_name", "address", "latitude", "longitude"}

type WriteResult struct {
	write.Result
	UploadAuthorization *assets.UploadAuthorization `json:"upload_authorization"`
}
