package travelpg

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/modules/travel/itinerary"
)

// tripCurrencyLookup 从 trips 读取币种，供派生 currency_code；行程项目本身不存币种列。
func tripCurrency(ctx context.Context, q *dbgen.Queries, accountID, tripID uuid.UUID) (string, error) {
	info, err := q.GetTripContentInfo(ctx, dbgen.GetTripContentInfoParams{AccountID: accountID, ID: tripID})
	if err != nil {
		return "", err
	}
	return info.CurrencyCode, nil
}

func localPtr(t *time.Time) *types.LocalDateTime {
	if t == nil {
		return nil
	}
	l := types.LocalDateTimeOf(*t)
	return &l
}

func localTime(l *types.LocalDateTime) *time.Time {
	if l == nil {
		return nil
	}
	t := l.Time()
	return &t
}

func floatPtr(s *string) (*float64, error) {
	if s == nil {
		return nil, nil
	}
	v, err := strconv.ParseFloat(*s, 64)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func floatText(v *float64) *string {
	if v == nil {
		return nil
	}
	s := strconv.FormatFloat(*v, 'f', 6, 64)
	return &s
}

func toItineraryResource(row dbgen.ItineraryItem, currency string) (itinerary.Resource, error) {
	lat, err := floatPtr(row.Latitude)
	if err != nil {
		return itinerary.Resource{}, fmt.Errorf("行程 %s 的纬度 %q 无法解析: %w", row.ID, *row.Latitude, err)
	}
	lng, err := floatPtr(row.Longitude)
	if err != nil {
		return itinerary.Resource{}, fmt.Errorf("行程 %s 的经度 %q 无法解析: %w", row.ID, *row.Longitude, err)
	}
	var amount, currencyCode *string
	if row.EstimatedAmount != nil {
		units, ok := metadata.MinorUnits(currency)
		if !ok {
			return itinerary.Resource{}, fmt.Errorf("旅行 %s 的币种 %q 不受支持", row.TripID, currency)
		}
		a, err := money.FromStorage(*row.EstimatedAmount, units)
		if err != nil {
			return itinerary.Resource{}, fmt.Errorf("行程 %s 的预计费用 %q 无法按 %s 表示: %w", row.ID, *row.EstimatedAmount, currency, err)
		}
		c := currency
		amount, currencyCode = &a, &c
	}
	return itinerary.Resource{
		ID: row.ID, TripID: row.TripID, Title: row.Title, Kind: itinerary.Kind(row.Kind), ScheduledOn: types.DateOf(row.ScheduledOn), SortOrder: row.SortOrder,
		PlannedStartLocal: localPtr(row.PlannedStartLocal), PlannedEndLocal: localPtr(row.PlannedEndLocal), PlannedDurationMinutes: row.PlannedDurationMinutes,
		PlaceName: row.PlaceName, Address: row.Address, Latitude: lat, Longitude: lng,
		EstimatedAmount: amount, CurrencyCode: currencyCode, Notes: row.Notes, Status: itinerary.Status(row.Status),
		ActualStartLocal: localPtr(row.ActualStartLocal), ActualEndLocal: localPtr(row.ActualEndLocal), ActualNotes: row.ActualNotes,
		Version: types.Version(row.Version), CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt), DeletedAt: pgcore.UTCPtr(row.DeletedAt),
	}, nil
}

func itineraryTripInfo(ctx context.Context, q *dbgen.Queries, accountID, tripID uuid.UUID) (itinerary.TripInfo, bool, error) {
	info, err := q.GetTripContentInfo(ctx, dbgen.GetTripContentInfoParams{AccountID: accountID, ID: tripID})
	if errors.Is(err, pgx.ErrNoRows) {
		return itinerary.TripInfo{}, false, nil
	}
	if err != nil {
		return itinerary.TripInfo{}, false, err
	}
	return itinerary.TripInfo{CurrencyCode: info.CurrencyCode, StartDate: types.DateOf(info.StartDate), EndDate: types.DateOf(info.EndDate), DeletedAt: pgcore.UTCPtr(info.DeletedAt)}, true, nil
}

func getItineraryItem(ctx context.Context, q *dbgen.Queries, accountID, tripID, id uuid.UUID, forUpdate bool) (itinerary.Resource, bool, error) {
	var row dbgen.ItineraryItem
	var err error
	if forUpdate {
		row, err = q.GetItineraryItemForUpdate(ctx, dbgen.GetItineraryItemForUpdateParams{AccountID: accountID, TripID: tripID, ID: id})
	} else {
		row, err = q.GetItineraryItem(ctx, dbgen.GetItineraryItemParams{AccountID: accountID, TripID: tripID, ID: id})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return itinerary.Resource{}, false, nil
	}
	if err != nil {
		return itinerary.Resource{}, false, err
	}
	currency, err := tripCurrency(ctx, q, accountID, tripID)
	if err != nil {
		return itinerary.Resource{}, false, err
	}
	r, err := toItineraryResource(row, currency)
	return r, err == nil, err
}

// itineraryRepo 绑定到一次写事务。
type itineraryRepo struct {
	scope *pgcore.TxScope
}

var _ itinerary.Repo = (*itineraryRepo)(nil)

// NewItineraryUnitOfWork 创建行程写事务入口。
func NewItineraryUnitOfWork(writer *pgcore.Writer) write.UnitOfWork[itinerary.Repo] {
	return pgcore.NewUnitOfWork(writer, func(scope *pgcore.TxScope) itinerary.Repo {
		return &itineraryRepo{scope: scope}
	})
}

func (r *itineraryRepo) MergeSource() write.MergeSource { return r.scope }

func (r *itineraryRepo) Trip(ctx context.Context, accountID, tripID uuid.UUID) (itinerary.TripInfo, bool, error) {
	return itineraryTripInfo(ctx, r.scope.Queries, accountID, tripID)
}

func (r *itineraryRepo) Get(ctx context.Context, accountID, tripID, id uuid.UUID) (itinerary.Resource, bool, error) {
	return getItineraryItem(ctx, r.scope.Queries, accountID, tripID, id, false)
}

func (r *itineraryRepo) GetForUpdate(ctx context.Context, accountID, tripID, id uuid.UUID) (itinerary.Resource, bool, error) {
	return getItineraryItem(ctx, r.scope.Queries, accountID, tripID, id, true)
}

func (r *itineraryRepo) IDExists(ctx context.Context, id uuid.UUID) (bool, error) {
	exists, err := r.scope.Queries.ItineraryItemIDExists(ctx, dbgen.ItineraryItemIDExistsParams{ID: id, AccountID: r.scope.AccountID})
	return boolOf(exists), err
}

func (r *itineraryRepo) MaxSortOrder(ctx context.Context, accountID, tripID uuid.UUID, on types.Date) (int32, bool, error) {
	row, err := r.scope.Queries.MaxItinerarySortOrder(ctx, dbgen.MaxItinerarySortOrderParams{AccountID: accountID, TripID: tripID, ScheduledOn: on.Time()})
	if err != nil {
		return 0, false, err
	}
	return row.MaxOrder, row.ItemCount > 0, nil
}

func (r *itineraryRepo) Insert(ctx context.Context, accountID uuid.UUID, it itinerary.Resource) (itinerary.Resource, error) {
	row, err := r.scope.Queries.InsertItineraryItem(ctx, dbgen.InsertItineraryItemParams{
		ID: it.ID, AccountID: accountID, TripID: it.TripID, Title: it.Title, Kind: string(it.Kind), ScheduledOn: it.ScheduledOn.Time(), SortOrder: it.SortOrder,
		PlannedStartLocal: localTime(it.PlannedStartLocal), PlannedEndLocal: localTime(it.PlannedEndLocal), PlannedDurationMinutes: it.PlannedDurationMinutes,
		PlaceName: it.PlaceName, Address: it.Address, Latitude: floatText(it.Latitude), Longitude: floatText(it.Longitude),
		EstimatedAmount: it.EstimatedAmount, Notes: it.Notes, Status: string(it.Status),
		ActualStartLocal: localTime(it.ActualStartLocal), ActualEndLocal: localTime(it.ActualEndLocal), ActualNotes: it.ActualNotes, CreatedAt: it.CreatedAt,
	})
	if err != nil {
		return itinerary.Resource{}, err
	}
	currency, err := tripCurrency(ctx, r.scope.Queries, accountID, it.TripID)
	if err != nil {
		return itinerary.Resource{}, err
	}
	return toItineraryResource(row, currency)
}

func (r *itineraryRepo) Update(ctx context.Context, accountID, tripID, id uuid.UUID, v itinerary.Values, now time.Time) (itinerary.Resource, error) {
	row, err := r.scope.Queries.UpdateItineraryItem(ctx, dbgen.UpdateItineraryItemParams{
		AccountID: accountID, TripID: tripID, ID: id, Title: v.Title, Kind: string(v.Kind),
		PlannedStartLocal: localTime(v.PlannedStartLocal), PlannedEndLocal: localTime(v.PlannedEndLocal), PlannedDurationMinutes: v.PlannedDurationMinutes,
		PlaceName: v.PlaceName, Address: v.Address, Latitude: floatText(v.Latitude), Longitude: floatText(v.Longitude),
		EstimatedAmount: v.EstimatedAmount, Notes: v.Notes, Status: string(v.Status),
		ActualStartLocal: localTime(v.ActualStartLocal), ActualEndLocal: localTime(v.ActualEndLocal), ActualNotes: v.ActualNotes, UpdatedAt: now,
	})
	if err != nil {
		return itinerary.Resource{}, err
	}
	currency, err := tripCurrency(ctx, r.scope.Queries, accountID, tripID)
	if err != nil {
		return itinerary.Resource{}, err
	}
	return toItineraryResource(row, currency)
}

func (r *itineraryRepo) SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (itinerary.Resource, error) {
	row, err := r.scope.Queries.SoftDeleteItineraryItem(ctx, dbgen.SoftDeleteItineraryItemParams{AccountID: accountID, TripID: tripID, ID: id, DeletedAt: &now})
	if err != nil {
		return itinerary.Resource{}, err
	}
	currency, err := tripCurrency(ctx, r.scope.Queries, accountID, tripID)
	if err != nil {
		return itinerary.Resource{}, err
	}
	return toItineraryResource(row, currency)
}

func (r *itineraryRepo) Reposition(ctx context.Context, accountID, tripID, id uuid.UUID, on types.Date, sortOrder int32, now time.Time) (itinerary.Resource, error) {
	row, err := r.scope.Queries.RepositionItineraryItem(ctx, dbgen.RepositionItineraryItemParams{
		AccountID: accountID, TripID: tripID, ID: id, ScheduledOn: on.Time(), SortOrder: sortOrder, UpdatedAt: now,
	})
	if err != nil {
		return itinerary.Resource{}, err
	}
	currency, err := tripCurrency(ctx, r.scope.Queries, accountID, tripID)
	if err != nil {
		return itinerary.Resource{}, err
	}
	return toItineraryResource(row, currency)
}

func (r *itineraryRepo) ListDaysForUpdate(ctx context.Context, accountID, tripID uuid.UUID, dates []types.Date) ([]itinerary.Resource, error) {
	ts := make([]time.Time, len(dates))
	for i, d := range dates {
		ts[i] = d.Time()
	}
	rows, err := r.scope.Queries.ListItineraryItemsForDays(ctx, dbgen.ListItineraryItemsForDaysParams{AccountID: accountID, TripID: tripID, Dates: ts})
	if err != nil {
		return nil, err
	}
	currency, err := tripCurrency(ctx, r.scope.Queries, accountID, tripID)
	if err != nil {
		return nil, err
	}
	return itineraryResources(rows, currency)
}

func itineraryResources(rows []dbgen.ItineraryItem, currency string) ([]itinerary.Resource, error) {
	out := make([]itinerary.Resource, 0, len(rows))
	for _, row := range rows {
		res, err := toItineraryResource(row, currency)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// ItineraryReader 是事务外只读仓储。
type ItineraryReader struct {
	q *dbgen.Queries
}

// NewItineraryReader 创建只读仓储。
func NewItineraryReader(pool *pgxpool.Pool) *ItineraryReader {
	return &ItineraryReader{q: dbgen.New(pool)}
}

var _ itinerary.Reader = (*ItineraryReader)(nil)

// Trip 实现 itinerary.Reader。
func (r *ItineraryReader) Trip(ctx context.Context, accountID, tripID uuid.UUID) (itinerary.TripInfo, bool, error) {
	return itineraryTripInfo(ctx, r.q, accountID, tripID)
}

// Get 实现 itinerary.Reader。
func (r *ItineraryReader) Get(ctx context.Context, accountID, tripID, id uuid.UUID) (itinerary.Resource, bool, error) {
	return getItineraryItem(ctx, r.q, accountID, tripID, id, false)
}

// List 实现 itinerary.Reader。
func (r *ItineraryReader) List(ctx context.Context, accountID, tripID uuid.UUID, q itinerary.ListQuery) ([]itinerary.Resource, error) {
	p := dbgen.ListItineraryItemsParams{AccountID: accountID, TripID: tripID, RowLimit: int32(q.Limit)}
	if q.DateFrom != "" {
		d := q.DateFrom.Time()
		p.DateFrom = &d
	}
	if q.DateTo != "" {
		d := q.DateTo.Time()
		p.DateTo = &d
	}
	if q.Status != "" {
		s := string(q.Status)
		p.Status = &s
	}
	if q.After != nil {
		d := q.After.ScheduledOn.Time()
		so := q.After.SortOrder
		p.CursorScheduledOn, p.CursorSortOrder, p.CursorID = &d, &so, uuid.NullUUID{UUID: q.After.ID, Valid: true}
	}
	rows, err := r.q.ListItineraryItems(ctx, p)
	if err != nil {
		return nil, err
	}
	currency, err := tripCurrency(ctx, r.q, accountID, tripID)
	if err != nil {
		return nil, err
	}
	return itineraryResources(rows, currency)
}
