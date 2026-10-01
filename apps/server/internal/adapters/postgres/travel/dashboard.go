package travelpg

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/geo"
	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/modules/travel/dashboard"
)

type DashboardStore struct{ pool *pgxpool.Pool }

func NewDashboardStore(pool *pgxpool.Pool) *DashboardStore { return &DashboardStore{pool: pool} }

func (s *DashboardStore) Read(ctx context.Context, accountID uuid.UUID, filter dashboard.Query, now time.Time) (dashboard.Snapshot, error) {
	out := dashboard.Snapshot{Years: []int{}, Trips: []dashboard.Trip{}, Places: []dashboard.Place{}}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	rows, err := q.DashboardTrips(ctx, dbgen.DashboardTripsParams{AccountID: accountID, AsOf: now})
	if err != nil {
		return out, err
	}
	years := map[int]bool{}
	ids := []uuid.UUID{}
	indices := map[uuid.UUID]int{}
	for _, t := range rows {
		years[t.StartDate.Year()] = true
		from := types.DateOf(t.StartDate)
		if filter.DateFrom != "" && string(from) < filter.DateFrom || filter.DateTo != "" && string(from) > filter.DateTo {
			continue
		}
		indices[t.ID] = len(out.Trips)
		ids = append(ids, t.ID)
		out.Trips = append(out.Trips, dashboard.Trip{ID: t.ID, Name: t.Name, StartDate: from, EndDate: types.DateOf(t.EndDate), CurrencyCode: t.CurrencyCode, Categories: []dashboard.Category{}})
	}
	for y := range years {
		out.Years = append(out.Years, y)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out.Years)))
	if len(ids) == 0 {
		return out, tx.Commit(ctx)
	}
	places, err := q.DashboardPlaces(ctx, dbgen.DashboardPlacesParams{AccountID: accountID, TripIds: ids})
	if err != nil {
		return out, err
	}
	for _, p := range places {
		lat, err := floatPtr(p.Latitude)
		if err != nil {
			return out, err
		}
		lng, err := floatPtr(p.Longitude)
		if err != nil {
			return out, err
		}
		name := strings.TrimSpace(p.PlaceName)
		if name == "" {
			name = p.Title
		}
		place := dashboard.Place{ID: p.ID, TripID: p.TripID, Version: types.Version(p.Version), Name: name, Kind: p.Kind, ScheduledOn: types.DateOf(p.ScheduledOn), Address: p.Address, Latitude: lat, Longitude: lng, Excluded: p.FootprintExcluded, POIID: p.PoiID}
		if p.CityCode != nil && p.ProvinceCode != nil && p.CityName != nil && p.ProvinceName != nil {
			place.Region = &dashboard.Region{ProvinceCode: *p.ProvinceCode, ProvinceName: *p.ProvinceName, CityCode: *p.CityCode, CityName: *p.CityName}
		}
		out.Places = append(out.Places, place)
	}
	categories, err := q.DashboardCategories(ctx, dbgen.DashboardCategoriesParams{AccountID: accountID, TripIds: ids})
	if err != nil {
		return out, err
	}
	for _, c := range categories {
		trip := &out.Trips[indices[c.TripID]]
		units, ok := metadata.MinorUnits(trip.CurrencyCode)
		if !ok {
			return out, fmt.Errorf("unsupported currency %s", trip.CurrencyCode)
		}
		personal, err := dashboardAmounts(c.PersonalExpense, c.PersonalRefund, units)
		if err != nil {
			return out, err
		}
		whole, err := dashboardAmounts(c.WholeExpense, c.WholeRefund, units)
		if err != nil {
			return out, err
		}
		trip.Categories = append(trip.Categories, dashboard.Category{ID: c.CategoryID, Name: c.CategoryName, Personal: personal, Whole: whole})
	}
	return out, tx.Commit(ctx)
}

func dashboardAmounts(expense, refund string, units int) (dashboard.Amounts, error) {
	e, err := money.ParseDecimal(expense)
	if err != nil {
		return dashboard.Amounts{}, err
	}
	r, err := money.ParseDecimal(refund)
	if err != nil {
		return dashboard.Amounts{}, err
	}
	return dashboard.Amounts{Expense: e.Format(units), Refund: r.Format(units), Net: e.Sub(r).Format(units)}, nil
}

func (s *DashboardStore) SaveRegion(ctx context.Context, point geo.Coordinate, r dashboard.Region, now time.Time) error {
	return dbgen.New(s.pool).SaveDashboardRegion(ctx, dbgen.SaveDashboardRegionParams{
		Latitude: strconv.FormatFloat(point.Latitude, 'f', 6, 64), Longitude: strconv.FormatFloat(point.Longitude, 'f', 6, 64),
		ProvinceCode: r.ProvinceCode, ProvinceName: r.ProvinceName, CityCode: r.CityCode, CityName: r.CityName, ResolvedAt: now,
	})
}
