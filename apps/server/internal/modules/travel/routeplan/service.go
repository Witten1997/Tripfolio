package routeplan

import (
	"context"
	"math"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/geo"
)

type Service struct {
	uow    UnitOfWork
	reader Reader
	store  WorkerStore
	router Router
	clock  clock.Clock
}

func NewService(uow UnitOfWork, reader Reader, store WorkerStore, router Router, clk clock.Clock) *Service {
	return &Service{uow: uow, reader: reader, store: store, router: router, clock: clk}
}

func (s *Service) Get(ctx context.Context, a actor.Actor, tripID uuid.UUID) (Plan, error) {
	plan, found, err := s.reader.Get(ctx, a.AccountID, tripID)
	if err != nil {
		return Plan{}, apperr.Internal(err)
	}
	if !found {
		return Plan{}, apperr.NotFound()
	}
	return plan, nil
}

func (s *Service) RequestRecalculate(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID) (write.Result, error) {
	req := write.Request{AccountID: a.AccountID, OperationID: operationID, OperationType: "route_plan.recalculate",
		Fingerprint: write.Fingerprint("route_plan.recalculate", tripID.String(), nil, struct{}{})}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if _, found, err := repo.GetPreference(ctx, a.AccountID, tripID); err != nil {
			return err
		} else if !found {
			return apperr.NotFound()
		}
		revision, err := repo.Invalidate(ctx, a.AccountID, tripID, s.clock.Now())
		if err != nil {
			return err
		}
		return scope.Enqueue(RecalculateJobArgs{AccountID: a.AccountID, TripID: tripID, Revision: revision})
	}, nil)
}

func (s *Service) SetLegMode(ctx context.Context, a actor.Actor, operationID, tripID, legID uuid.UUID, version int64, selected Mode) (write.Result, error) {
	if !selected.ValidSelection() {
		return write.Result{}, apperr.Validation(apperr.Field("mode", "INVALID", "请选择自动、驾车、步行或骑行"))
	}
	base := version
	cmd := struct {
		Mode Mode `json:"mode"`
	}{selected}
	req := write.Request{AccountID: a.AccountID, OperationID: operationID, OperationType: "route_leg.mode",
		Fingerprint: write.Fingerprint("route_leg.mode", legID.String(), &base, cmd)}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		leg, found, err := repo.GetLegForUpdate(ctx, a.AccountID, tripID, legID)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFound()
		}
		if leg.Version != version {
			return apperr.VersionConflict(&apperr.Conflict{EntityType: EntityType, EntityID: legID,
				ExpectedVersion: version, CurrentVersion: leg.Version, Current: leg})
		}
		mode, source := selected, "manual"
		if selected == ModeAuto {
			pref, found, err := repo.GetPreference(ctx, a.AccountID, tripID)
			if err != nil {
				return err
			}
			if !found {
				return apperr.NotFound()
			}
			mode, source = automaticMode(pref, leg.DirectDistanceMeters), "preference"
		}
		updated, err := repo.UpdateLegMode(ctx, a.AccountID, tripID, legID, mode, source, s.clock.Now())
		if err != nil {
			return err
		}
		revision, err := repo.Invalidate(ctx, a.AccountID, tripID, s.clock.Now())
		if err != nil {
			return err
		}
		if err := scope.Enqueue(RecalculateJobArgs{AccountID: a.AccountID, TripID: tripID, Revision: revision}); err != nil {
			return err
		}
		scope.SetPrimary(write.Ref(EntityType, updated.ID, updated.Version))
		return nil
	}, func(ctx context.Context, repo Repo) (any, error) {
		leg, found, err := repo.GetLegForUpdate(ctx, a.AccountID, tripID, legID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, apperr.NotFound()
		}
		return leg, nil
	})
}

func (s *Service) Recalculate(ctx context.Context, args RecalculateJobArgs) error {
	snapshot, found, err := s.store.Snapshot(ctx, args.AccountID, args.TripID)
	if err != nil || !found {
		return err
	}
	if snapshot.Summary.Revision != args.Revision {
		return nil
	}
	now := s.clock.Now()
	if err := s.store.MarkCalculating(ctx, args.AccountID, args.TripID, args.Revision, now); err != nil {
		return err
	}

	located := make([]Point, 0, len(snapshot.Points))
	missing := int32(0)
	for _, point := range snapshot.Points {
		if point.Latitude == nil || point.Longitude == nil {
			missing++
			continue
		}
		located = append(located, point)
	}
	existing := make(map[[2]uuid.UUID]Leg, len(snapshot.Legs))
	for _, leg := range snapshot.Legs {
		existing[[2]uuid.UUID{leg.FromItemID, leg.ToItemID}] = leg
	}

	legs := make([]Leg, 0, max(0, len(located)-1))
	ready := int32(0)
	var totalDistance, totalDuration int64
	for index := 0; index+1 < len(located); index++ {
		from, to := located[index], located[index+1]
		direct := haversineMeters(*from.Latitude, *from.Longitude, *to.Latitude, *to.Longitude)
		old, retained := existing[[2]uuid.UUID{from.ID, to.ID}]
		mode, source := automaticMode(snapshot.Preference, direct), "preference"
		id, version := uuid.New(), int64(1)
		if retained {
			id, version = old.ID, old.Version+1
			if old.ModeSource == "manual" {
				mode, source = old.Mode, "manual"
			}
		}
		leg := Leg{ID: id, FromItemID: from.ID, ToItemID: to.ID, Version: version, Mode: mode,
			ModeSource: source, DirectDistanceMeters: direct, Status: "failed", CalculatedAt: &now}
		if s.router == nil {
			code := "DEPENDENCY_UNAVAILABLE"
			leg.ErrorCode = &code
		} else {
			route, routeErr := s.router.RouteAs(ctx, "route-plan|"+args.AccountID.String(),
				geo.Coordinate{Latitude: *from.Latitude, Longitude: *from.Longitude},
				geo.Coordinate{Latitude: *to.Latitude, Longitude: *to.Longitude}, geo.Mode(mode))
			if routeErr != nil {
				code := "ROUTE_UNAVAILABLE"
				if app, ok := apperr.As(routeErr); ok && app.Code != "" {
					code = app.Code
				}
				leg.ErrorCode = &code
			} else {
				leg.Status = "ready"
				leg.RouteDistanceMeters, leg.RouteDurationSeconds = &route.DistanceMeters, &route.DurationSeconds
				ready++
				totalDistance += route.DistanceMeters
				totalDuration += route.DurationSeconds
			}
		}
		legs = append(legs, leg)
	}

	summary := Summary{Revision: args.Revision, Status: "incomplete", ReadyLegCount: ready,
		TotalLegCount: int32(len(legs)), MissingPointCount: missing, CalculatedAt: &now}
	switch {
	case len(legs) == 0 && missing == 0:
		summary.Status = "empty"
	case ready == int32(len(legs)) && missing == 0:
		summary.Status = "ready"
		summary.TotalDistanceMeters, summary.TotalDurationSeconds = &totalDistance, &totalDuration
	}
	_, err = s.store.Replace(ctx, args.AccountID, args.TripID, args.Revision, legs, summary, now)
	return err
}

func automaticMode(pref Preference, distance int64) Mode {
	if distance <= int64(pref.ShortDistanceMeters) {
		return pref.ShortMode
	}
	return ModeDriving
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) int64 {
	const earthRadius = 6371008.8
	toRadians := math.Pi / 180
	lat1, lat2 = lat1*toRadians, lat2*toRadians
	dLat, dLon := lat2-lat1, (lon2-lon1)*toRadians
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return int64(math.Round(earthRadius * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))))
}
