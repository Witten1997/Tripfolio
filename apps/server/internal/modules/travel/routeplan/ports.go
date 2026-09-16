package routeplan

import (
	"context"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/geo"
)

type Repo interface {
	GetPreference(ctx context.Context, accountID, tripID uuid.UUID) (Preference, bool, error)
	GetLegForUpdate(ctx context.Context, accountID, tripID, legID uuid.UUID) (Leg, bool, error)
	UpdateLegMode(ctx context.Context, accountID, tripID, legID uuid.UUID, mode Mode, source string, now time.Time) (Leg, error)
	Invalidate(ctx context.Context, accountID, tripID uuid.UUID, now time.Time) (int64, error)
}

type Reader interface {
	Get(ctx context.Context, accountID, tripID uuid.UUID) (Plan, bool, error)
}

type WorkerStore interface {
	Snapshot(ctx context.Context, accountID, tripID uuid.UUID) (Snapshot, bool, error)
	MarkCalculating(ctx context.Context, accountID, tripID uuid.UUID, revision int64, now time.Time) error
	Replace(ctx context.Context, accountID, tripID uuid.UUID, revision int64, legs []Leg, summary Summary, now time.Time) (bool, error)
}

type Router interface {
	RouteAs(ctx context.Context, limitKey string, origin, destination geo.Coordinate, mode geo.Mode) (geo.Route, error)
}

type UnitOfWork = write.UnitOfWork[Repo]
