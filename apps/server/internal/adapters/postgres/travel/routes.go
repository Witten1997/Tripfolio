package travelpg

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/modules/travel/routeplan"
)

type routePlanRepo struct{ q *dbgen.Queries }

func NewRoutePlanUnitOfWork(writer *pgcore.Writer) *pgcore.UnitOfWork[routeplan.Repo] {
	return pgcore.NewUnitOfWork(writer, func(scope *pgcore.TxScope) routeplan.Repo {
		return &routePlanRepo{q: scope.Queries}
	})
}

func routePreference(row dbgen.GetRoutePlanTripRow) routeplan.Preference {
	return routeplan.Preference{ShortMode: routeplan.Mode(row.RouteShortMode), ShortDistanceMeters: row.RouteShortDistanceMeters}
}

func (r *routePlanRepo) GetPreference(ctx context.Context, accountID, tripID uuid.UUID) (routeplan.Preference, bool, error) {
	row, err := r.q.GetRoutePlanTrip(ctx, dbgen.GetRoutePlanTripParams{AccountID: accountID, TripID: tripID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.DeletedAt != nil {
		return routeplan.Preference{}, false, nil
	}
	if err != nil {
		return routeplan.Preference{}, false, err
	}
	return routePreference(row), true, nil
}

func routeLeg(row dbgen.ItineraryRouteLeg) (routeplan.Leg, error) {
	return routeplan.Leg{ID: row.ID, FromItemID: row.FromItemID, ToItemID: row.ToItemID, Version: row.Version,
		Mode: routeplan.Mode(row.Mode), ModeSource: row.ModeSource, DirectDistanceMeters: row.DirectDistanceMeters,
		RouteDistanceMeters: row.RouteDistanceMeters, RouteDurationSeconds: row.RouteDurationSeconds,
		Status: row.Status, ErrorCode: row.ErrorCode, CalculatedAt: row.CalculatedAt}, nil
}

func (r *routePlanRepo) GetLegForUpdate(ctx context.Context, accountID, tripID, legID uuid.UUID) (routeplan.Leg, bool, error) {
	row, err := r.q.GetRoutePlanLegForUpdate(ctx, dbgen.GetRoutePlanLegForUpdateParams{AccountID: accountID, TripID: tripID, ID: legID})
	if errors.Is(err, pgx.ErrNoRows) {
		return routeplan.Leg{}, false, nil
	}
	if err != nil {
		return routeplan.Leg{}, false, err
	}
	leg, err := routeLeg(row)
	return leg, err == nil, err
}

func (r *routePlanRepo) UpdateLegMode(ctx context.Context, accountID, tripID, legID uuid.UUID, mode routeplan.Mode, source string, now time.Time) (routeplan.Leg, error) {
	row, err := r.q.UpdateRouteLegMode(ctx, dbgen.UpdateRouteLegModeParams{AccountID: accountID, TripID: tripID,
		ID: legID, Mode: string(mode), ModeSource: source, UpdatedAt: now})
	if err != nil {
		return routeplan.Leg{}, err
	}
	return routeLeg(row)
}

func (r *routePlanRepo) Invalidate(ctx context.Context, accountID, tripID uuid.UUID, now time.Time) (int64, error) {
	row, err := r.q.InvalidateRouteSummary(ctx, dbgen.InvalidateRouteSummaryParams{AccountID: accountID, TripID: tripID, UpdatedAt: now})
	return row.Revision, err
}

type RoutePlanStore struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewRoutePlanStore(pool *pgxpool.Pool) *RoutePlanStore {
	return &RoutePlanStore{pool: pool, q: dbgen.New(pool)}
}

func routeSummary(row dbgen.TripRouteSummary) routeplan.Summary {
	return routeplan.Summary{Revision: row.Revision, Status: row.Status, TotalDistanceMeters: row.TotalDistanceMeters,
		TotalDurationSeconds: row.TotalDurationSeconds, ReadyLegCount: row.ReadyLegCount, TotalLegCount: row.TotalLegCount,
		MissingPointCount: row.MissingPointCount, CalculatedAt: row.CalculatedAt}
}

func routeLegs(rows []dbgen.ItineraryRouteLeg) []routeplan.Leg {
	legs := make([]routeplan.Leg, 0, len(rows))
	for _, row := range rows {
		legs = append(legs, routeplan.Leg{
			ID: row.ID, FromItemID: row.FromItemID, ToItemID: row.ToItemID, Version: row.Version,
			Mode: routeplan.Mode(row.Mode), ModeSource: row.ModeSource, DirectDistanceMeters: row.DirectDistanceMeters,
			RouteDistanceMeters: row.RouteDistanceMeters, RouteDurationSeconds: row.RouteDurationSeconds,
			Status: row.Status, ErrorCode: row.ErrorCode, CalculatedAt: row.CalculatedAt,
		})
	}
	return legs
}

func (s *RoutePlanStore) Get(ctx context.Context, accountID, tripID uuid.UUID) (routeplan.Plan, bool, error) {
	tripRow, err := s.q.GetRoutePlanTrip(ctx, dbgen.GetRoutePlanTripParams{AccountID: accountID, TripID: tripID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && tripRow.DeletedAt != nil {
		return routeplan.Plan{}, false, nil
	}
	if err != nil {
		return routeplan.Plan{}, false, err
	}
	summaryRow, err := s.q.GetRouteSummary(ctx, dbgen.GetRouteSummaryParams{AccountID: accountID, TripID: tripID})
	if err != nil {
		return routeplan.Plan{}, false, err
	}
	rows, err := s.q.ListRoutePlanLegs(ctx, dbgen.ListRoutePlanLegsParams{AccountID: accountID, TripID: tripID})
	if err != nil {
		return routeplan.Plan{}, false, err
	}
	legs := routeLegs(rows)
	return routeplan.Plan{Preference: routePreference(tripRow), Summary: routeSummary(summaryRow), Legs: legs}, true, nil
}

func parseCoordinate(raw *string) *float64 {
	if raw == nil {
		return nil
	}
	value, err := strconv.ParseFloat(*raw, 64)
	if err != nil {
		return nil
	}
	return &value
}

func (s *RoutePlanStore) Snapshot(ctx context.Context, accountID, tripID uuid.UUID) (routeplan.Snapshot, bool, error) {
	plan, found, err := s.Get(ctx, accountID, tripID)
	if err != nil || !found {
		return routeplan.Snapshot{}, found, err
	}
	rows, err := s.q.ListRoutePlanPoints(ctx, dbgen.ListRoutePlanPointsParams{AccountID: accountID, TripID: tripID})
	if err != nil {
		return routeplan.Snapshot{}, false, err
	}
	points := make([]routeplan.Point, 0, len(rows))
	for _, row := range rows {
		points = append(points, routeplan.Point{ID: row.ID, Title: row.Title,
			Latitude: parseCoordinate(row.Latitude), Longitude: parseCoordinate(row.Longitude)})
	}
	return routeplan.Snapshot{Preference: plan.Preference, Summary: plan.Summary, Points: points, Legs: plan.Legs}, true, nil
}

func (s *RoutePlanStore) MarkCalculating(ctx context.Context, accountID, tripID uuid.UUID, revision int64, now time.Time) error {
	return s.q.SetRouteSummaryCalculating(ctx, dbgen.SetRouteSummaryCalculatingParams{AccountID: accountID, TripID: tripID, Revision: revision, UpdatedAt: now})
}

func (s *RoutePlanStore) Replace(ctx context.Context, accountID, tripID uuid.UUID, revision int64, legs []routeplan.Leg, summary routeplan.Summary, now time.Time) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	current, err := q.GetRouteSummaryForUpdate(ctx, dbgen.GetRouteSummaryForUpdateParams{AccountID: accountID, TripID: tripID})
	if err != nil {
		return false, err
	}
	if current.Revision != revision {
		return false, nil
	}
	if err := q.DeleteRoutePlanLegs(ctx, dbgen.DeleteRoutePlanLegsParams{AccountID: accountID, TripID: tripID}); err != nil {
		return false, err
	}
	for _, leg := range legs {
		if _, err := q.InsertRoutePlanLeg(ctx, dbgen.InsertRoutePlanLegParams{ID: leg.ID, AccountID: accountID, TripID: tripID,
			FromItemID: leg.FromItemID, ToItemID: leg.ToItemID, Version: leg.Version, Mode: string(leg.Mode), ModeSource: leg.ModeSource,
			DirectDistanceMeters: leg.DirectDistanceMeters, RouteDistanceMeters: leg.RouteDistanceMeters,
			RouteDurationSeconds: leg.RouteDurationSeconds, Status: leg.Status, ErrorCode: leg.ErrorCode,
			CalculatedAt: leg.CalculatedAt, CreatedAt: now, UpdatedAt: now}); err != nil {
			return false, err
		}
	}
	rows, err := q.ReplaceRouteSummary(ctx, dbgen.ReplaceRouteSummaryParams{AccountID: accountID, TripID: tripID, Revision: revision,
		Status: summary.Status, TotalDistanceMeters: summary.TotalDistanceMeters, TotalDurationSeconds: summary.TotalDurationSeconds,
		ReadyLegCount: summary.ReadyLegCount, TotalLegCount: summary.TotalLegCount, MissingPointCount: summary.MissingPointCount,
		CalculatedAt: summary.CalculatedAt, UpdatedAt: now})
	if err != nil || rows == 0 {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

var _ routeplan.Reader = (*RoutePlanStore)(nil)
var _ routeplan.WorkerStore = (*RoutePlanStore)(nil)
