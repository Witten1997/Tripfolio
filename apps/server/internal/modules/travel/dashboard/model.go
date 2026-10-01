package dashboard

import (
	"context"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/geo"
)

type Query struct{ DateFrom, DateTo, LocationAfter string }

type Region struct {
	ProvinceCode string `json:"province_code"`
	ProvinceName string `json:"province_name"`
	CityCode     string `json:"city_code"`
	CityName     string `json:"city_name"`
}

type Place struct {
	ID          uuid.UUID     `json:"id"`
	TripID      uuid.UUID     `json:"trip_id"`
	Version     types.Version `json:"version"`
	Name        string        `json:"name"`
	Kind        string        `json:"kind"`
	ScheduledOn types.Date    `json:"scheduled_on"`
	Address     string        `json:"address"`
	Latitude    *float64      `json:"latitude"`
	Longitude   *float64      `json:"longitude"`
	Excluded    bool          `json:"excluded"`
	POIID       string        `json:"poi_id"`
	Region      *Region       `json:"region"`
}

type Amounts struct {
	Expense string `json:"expense"`
	Refund  string `json:"refund"`
	Net     string `json:"net"`
}

type Category struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Personal Amounts   `json:"personal"`
	Whole    Amounts   `json:"whole"`
}

type Trip struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	StartDate    types.Date `json:"start_date"`
	EndDate      types.Date `json:"end_date"`
	CurrencyCode string     `json:"currency_code"`
	Days         int        `json:"days"`
	Categories   []Category `json:"categories"`
}

type Snapshot struct {
	GeneratedAt        time.Time `json:"generated_at"`
	Years              []int     `json:"years"`
	Trips              []Trip    `json:"trips"`
	Places             []Place   `json:"places"`
	Days               int       `json:"days"`
	MonthlyDays        []int     `json:"monthly_days"`
	UnresolvedPlaces   int       `json:"unresolved_places"`
	MissingCoordinates int       `json:"missing_coordinates"`
	ResolvedLocations  int       `json:"resolved_locations"`
	LocationWarning    string    `json:"location_warning"`
	NextLocationAfter  string    `json:"next_location_after"`
}

type Store interface {
	Read(context.Context, uuid.UUID, Query, time.Time) (Snapshot, error)
	SaveRegion(context.Context, geo.Coordinate, Region, time.Time) error
}

type Geocoder interface {
	Reverse(context.Context, actor.Actor, geo.Coordinate) (geo.Place, error)
}
