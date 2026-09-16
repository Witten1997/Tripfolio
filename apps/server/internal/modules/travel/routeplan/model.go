// Package routeplan manages persisted routes between adjacent itinerary points.
package routeplan

import (
	"time"

	"github.com/google/uuid"
)

const EntityType = "itinerary_route_leg"

type Mode string

const (
	ModeDriving Mode = "driving"
	ModeWalking Mode = "walking"
	ModeCycling Mode = "cycling"
	ModeAuto    Mode = "auto"
)

func (m Mode) ValidSelection() bool {
	return m == ModeAuto || m == ModeDriving || m == ModeWalking || m == ModeCycling
}

type Preference struct {
	ShortMode           Mode  `json:"short_mode"`
	ShortDistanceMeters int32 `json:"short_distance_meters"`
}

type Leg struct {
	ID                   uuid.UUID  `json:"id"`
	FromItemID           uuid.UUID  `json:"from_item_id"`
	ToItemID             uuid.UUID  `json:"to_item_id"`
	Version              int64      `json:"version,string"`
	Mode                 Mode       `json:"mode"`
	ModeSource           string     `json:"mode_source"`
	DirectDistanceMeters int64      `json:"direct_distance_meters"`
	RouteDistanceMeters  *int64     `json:"route_distance_meters"`
	RouteDurationSeconds *int64     `json:"route_duration_seconds"`
	Status               string     `json:"status"`
	ErrorCode            *string    `json:"error_code"`
	CalculatedAt         *time.Time `json:"calculated_at"`
}

type Summary struct {
	Revision             int64      `json:"revision,string"`
	Status               string     `json:"status"`
	TotalDistanceMeters  *int64     `json:"total_distance_meters"`
	TotalDurationSeconds *int64     `json:"total_duration_seconds"`
	ReadyLegCount        int32      `json:"ready_leg_count"`
	TotalLegCount        int32      `json:"total_leg_count"`
	MissingPointCount    int32      `json:"missing_point_count"`
	CalculatedAt         *time.Time `json:"calculated_at"`
}

type Plan struct {
	Preference Preference `json:"preference"`
	Summary    Summary    `json:"summary"`
	Legs       []Leg      `json:"legs"`
}

type Point struct {
	ID        uuid.UUID
	Title     string
	Latitude  *float64
	Longitude *float64
}

type Snapshot struct {
	Preference Preference
	Summary    Summary
	Points     []Point
	Legs       []Leg
}

type RecalculateJobArgs struct {
	AccountID uuid.UUID `json:"account_id"`
	TripID    uuid.UUID `json:"trip_id"`
	Revision  int64     `json:"revision"`
}

func (RecalculateJobArgs) Kind() string { return "trip_route_recalculate" }
