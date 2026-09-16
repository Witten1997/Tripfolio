// Package travelpg 是旅行各业务包的 PostgreSQL 适配；本文件实现 trip 模块的事务内仓储与只读仓储。
package travelpg

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	"tripfolio/server/internal/modules/travel/trip"
)

// ToTripResource 把 trips 行转为规范资源，预算按币种小数位转回规范字符串；供其他适配器（如账目的币种锁定）复用。
func ToTripResource(row dbgen.Trip) (trip.Resource, error) { return toResource(row) }

func toResource(row dbgen.Trip) (trip.Resource, error) {
	var budget *string
	if row.BudgetAmount != nil {
		units, ok := metadata.MinorUnits(row.CurrencyCode)
		if !ok {
			return trip.Resource{}, fmt.Errorf("旅行 %s 的币种 %q 不受支持", row.ID, row.CurrencyCode)
		}
		b, err := money.FromStorage(*row.BudgetAmount, units)
		if err != nil {
			return trip.Resource{}, fmt.Errorf("旅行 %s 的预算 %q 无法按 %s 表示: %w", row.ID, *row.BudgetAmount, row.CurrencyCode, err)
		}
		budget = &b
	}
	return trip.Resource{
		ID: row.ID, Name: row.Name, StartDate: types.DateOf(row.StartDate), EndDate: types.DateOf(row.EndDate),
		Destination: row.Destination, Notes: row.Notes, Timezone: row.Timezone, CurrencyCode: row.CurrencyCode, BudgetAmount: budget,
		Version: types.Version(row.Version), CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt),
		ArchivedAt: pgcore.UTCPtr(row.ArchivedAt), CurrencyLockedAt: pgcore.UTCPtr(row.CurrencyLockedAt), DeletedAt: pgcore.UTCPtr(row.DeletedAt),
		PurgeAfterAt: pgcore.UTCPtr(row.PurgeAfterAt), PurgeRequestedAt: pgcore.UTCPtr(row.PurgeRequestedAt),
	}, nil
}

func toDeletionJob(row dbgen.DeletionJob) trip.DeletionJob {
	return trip.DeletionJob{
		ID: row.ID, OwnerAccountID: row.OwnerAccountID, TargetTripID: row.TargetTripID.UUID, Status: row.Status, Stage: row.Stage,
		CreatedAt: pgcore.UTC(row.CreatedAt),
	}
}

func getTrip(ctx context.Context, q *dbgen.Queries, accountID, id uuid.UUID, forUpdate bool) (trip.Resource, bool, error) {
	var row dbgen.Trip
	var err error
	if forUpdate {
		row, err = q.GetTripForUpdate(ctx, dbgen.GetTripForUpdateParams{AccountID: accountID, ID: id})
	} else {
		row, err = q.GetTrip(ctx, dbgen.GetTripParams{AccountID: accountID, ID: id})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return trip.Resource{}, false, nil
	}
	if err != nil {
		return trip.Resource{}, false, err
	}
	r, err := toResource(row)
	return r, err == nil, err
}

func boolOf(v *bool) bool { return v != nil && *v }

// tripRepo 绑定到一次写事务。
type tripRepo struct {
	scope *pgcore.TxScope
}

var _ trip.Repo = (*tripRepo)(nil)

// NewTripUnitOfWork 创建旅行写事务入口。
func NewTripUnitOfWork(writer *pgcore.Writer) write.UnitOfWork[trip.Repo] {
	return pgcore.NewUnitOfWork(writer, func(scope *pgcore.TxScope) trip.Repo {
		return &tripRepo{scope: scope}
	})
}

func (r *tripRepo) MergeSource() write.MergeSource { return r.scope }

func (r *tripRepo) Get(ctx context.Context, accountID, id uuid.UUID) (trip.Resource, bool, error) {
	return getTrip(ctx, r.scope.Queries, accountID, id, false)
}

func (r *tripRepo) GetForUpdate(ctx context.Context, accountID, id uuid.UUID) (trip.Resource, bool, error) {
	return getTrip(ctx, r.scope.Queries, accountID, id, true)
}

func (r *tripRepo) IDExists(ctx context.Context, id uuid.UUID) (bool, error) {
	exists, err := r.scope.Queries.TripIDExists(ctx, dbgen.TripIDExistsParams{ID: id, AccountID: r.scope.AccountID})
	return boolOf(exists), err
}

func (r *tripRepo) AccountDefaultTimezone(ctx context.Context, accountID uuid.UUID) (string, error) {
	return r.scope.Queries.GetAccountDefaultTimezone(ctx, accountID)
}

func (r *tripRepo) Insert(ctx context.Context, accountID uuid.UUID, t trip.Resource) (trip.Resource, error) {
	row, err := r.scope.Queries.InsertTrip(ctx, dbgen.InsertTripParams{
		ID: t.ID, AccountID: accountID, Name: t.Name, StartDate: t.StartDate.Time(), EndDate: t.EndDate.Time(),
		Destination: t.Destination, Notes: t.Notes, Timezone: t.Timezone, CurrencyCode: t.CurrencyCode, BudgetAmount: t.BudgetAmount,
		CreatedAt: t.CreatedAt,
	})
	if err != nil {
		return trip.Resource{}, err
	}
	return toResource(row)
}

func (r *tripRepo) Update(ctx context.Context, accountID, id uuid.UUID, v trip.Values, now time.Time) (trip.Resource, error) {
	row, err := r.scope.Queries.UpdateTrip(ctx, dbgen.UpdateTripParams{
		AccountID: accountID, ID: id, Name: v.Name, StartDate: v.StartDate.Time(), EndDate: v.EndDate.Time(), Destination: v.Destination,
		Notes: v.Notes, Timezone: v.Timezone, CurrencyCode: v.CurrencyCode, BudgetAmount: v.BudgetAmount, UpdatedAt: now,
	})
	if err != nil {
		return trip.Resource{}, err
	}
	return toResource(row)
}

func (r *tripRepo) SetArchived(ctx context.Context, accountID, id uuid.UUID, archivedAt *time.Time, now time.Time) (trip.Resource, error) {
	row, err := r.scope.Queries.SetTripArchived(ctx, dbgen.SetTripArchivedParams{AccountID: accountID, ID: id, ArchivedAt: archivedAt, UpdatedAt: now})
	if err != nil {
		return trip.Resource{}, err
	}
	return toResource(row)
}

func (r *tripRepo) Trash(ctx context.Context, accountID, id uuid.UUID, now, purgeAfter time.Time) (trip.Resource, error) {
	row, err := r.scope.Queries.TrashTrip(ctx, dbgen.TrashTripParams{AccountID: accountID, ID: id, DeletedAt: &now, PurgeAfterAt: &purgeAfter})
	if err != nil {
		return trip.Resource{}, err
	}
	return toResource(row)
}

func (r *tripRepo) Restore(ctx context.Context, accountID, id uuid.UUID, now time.Time) (trip.Resource, error) {
	row, err := r.scope.Queries.RestoreTrip(ctx, dbgen.RestoreTripParams{AccountID: accountID, ID: id, UpdatedAt: now})
	if err != nil {
		return trip.Resource{}, err
	}
	return toResource(row)
}

func (r *tripRepo) RequestPurge(ctx context.Context, accountID, id uuid.UUID, now time.Time) (trip.Resource, error) {
	row, err := r.scope.Queries.RequestTripPurge(ctx, dbgen.RequestTripPurgeParams{AccountID: accountID, ID: id, RequestedAt: &now})
	if err != nil {
		return trip.Resource{}, err
	}
	return toResource(row)
}

func (r *tripRepo) ActiveDeletionJob(ctx context.Context, accountID, tripID uuid.UUID) (trip.DeletionJob, bool, error) {
	row, err := r.scope.Queries.GetActiveTripDeletionJob(ctx, dbgen.GetActiveTripDeletionJobParams{AccountID: accountID, TripID: uuid.NullUUID{UUID: tripID, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return trip.DeletionJob{}, false, nil
	}
	if err != nil {
		return trip.DeletionJob{}, false, err
	}
	return toDeletionJob(row), true, nil
}

func (r *tripRepo) InsertDeletionJob(ctx context.Context, job trip.DeletionJob) (trip.DeletionJob, error) {
	row, err := r.scope.Queries.InsertTripDeletionJob(ctx, dbgen.InsertTripDeletionJobParams{
		ID: job.ID, AccountID: job.OwnerAccountID, TripID: uuid.NullUUID{UUID: job.TargetTripID, Valid: true},
		Status: job.Status, Stage: job.Stage, CreatedAt: job.CreatedAt,
	})
	if err != nil {
		return trip.DeletionJob{}, err
	}
	return toDeletionJob(row), nil
}

func (r *tripRepo) HasEstimatedAmounts(ctx context.Context, accountID, tripID uuid.UUID) (bool, error) {
	exists, err := r.scope.Queries.TripHasEstimatedAmounts(ctx, dbgen.TripHasEstimatedAmountsParams{AccountID: accountID, TripID: tripID})
	return boolOf(&exists), err
}

func (r *tripRepo) HasLocalTimes(ctx context.Context, accountID, tripID uuid.UUID) (bool, error) {
	exists, err := r.scope.Queries.TripHasLocalTimes(ctx, dbgen.TripHasLocalTimesParams{AccountID: accountID, TripID: tripID})
	return boolOf(exists), err
}

func (r *tripRepo) HasItineraryOutside(ctx context.Context, accountID, tripID uuid.UUID, start, end types.Date) (bool, error) {
	exists, err := r.scope.Queries.TripHasItineraryOutside(ctx, dbgen.TripHasItineraryOutsideParams{
		AccountID: accountID, TripID: tripID, StartDate: start.Time(), EndDate: end.Time(),
	})
	return boolOf(&exists), err
}

// TripReader 是事务外只读仓储。
type TripReader struct {
	q *dbgen.Queries
}

// NewTripReader 创建只读仓储。
func NewTripReader(pool *pgxpool.Pool) *TripReader {
	return &TripReader{q: dbgen.New(pool)}
}

var _ trip.Reader = (*TripReader)(nil)

// Get 返回旅行（含回收站中的）。
func (r *TripReader) Get(ctx context.Context, accountID, id uuid.UUID) (trip.Resource, bool, error) {
	return getTrip(ctx, r.q, accountID, id, false)
}

// likeEscaper 转义 ILIKE 通配符，使 q 只做字面子串匹配。
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// List 实现 trip.Reader。
func (r *TripReader) List(ctx context.Context, accountID uuid.UUID, q trip.ListQuery) ([]trip.ListItem, error) {
	var needle, phase *string
	if q.Filters.Query != "" {
		s := likeEscaper.Replace(q.Filters.Query)
		needle = &s
	}
	if q.Filters.Phase != nil {
		p := string(*q.Filters.Phase)
		phase = &p
	}
	var cursorID uuid.NullUUID
	if q.After != nil {
		cursorID = uuid.NullUUID{UUID: q.After.ID, Valid: true}
	}
	archived := string(q.Filters.Archived)
	if archived == "" {
		archived = string(trip.ArchivedAll)
	}
	out := make([]trip.ListItem, 0, q.Limit)
	if q.Filters.Sort == trip.SortUpdatedAtDesc {
		var cursorAt *time.Time
		if q.After != nil {
			cursorAt = q.After.UpdatedAt
		}
		rows, err := r.q.ListTripsByUpdatedAt(ctx, dbgen.ListTripsByUpdatedAtParams{
			Now: q.Now, AccountID: accountID, Archived: archived, Q: needle, Phase: phase,
			CursorUpdatedAt: cursorAt, CursorID: cursorID, RowLimit: int32(q.Limit),
		})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			res, err := toResource(row.Trip)
			if err != nil {
				return nil, err
			}
			out = append(out, trip.ListItem{Resource: res, Phase: trip.Phase(row.Phase)})
		}
		return out, nil
	}
	if q.Filters.Sort == trip.SortStartDateAsc {
		var cursorDate *time.Time
		if q.After != nil && q.After.StartDate != "" {
			d := q.After.StartDate.Time()
			cursorDate = &d
		}
		rows, err := r.q.ListTripsByStartDateAsc(ctx, dbgen.ListTripsByStartDateAscParams{
			Now: q.Now, AccountID: accountID, Archived: archived, Q: needle, Phase: phase,
			CursorStartDate: cursorDate, CursorID: cursorID, RowLimit: int32(q.Limit),
		})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			res, err := toResource(row.Trip)
			if err != nil {
				return nil, err
			}
			out = append(out, trip.ListItem{Resource: res, Phase: trip.Phase(row.Phase)})
		}
		return out, nil
	}
	var cursorDate *time.Time
	if q.After != nil && q.After.StartDate != "" {
		d := q.After.StartDate.Time()
		cursorDate = &d
	}
	rows, err := r.q.ListTripsByStartDate(ctx, dbgen.ListTripsByStartDateParams{
		Now: q.Now, AccountID: accountID, Archived: archived, Q: needle, Phase: phase,
		CursorStartDate: cursorDate, CursorID: cursorID, RowLimit: int32(q.Limit),
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		res, err := toResource(row.Trip)
		if err != nil {
			return nil, err
		}
		out = append(out, trip.ListItem{Resource: res, Phase: trip.Phase(row.Phase)})
	}
	return out, nil
}

// ListTrashed 实现 trip.Reader。
func (r *TripReader) ListTrashed(ctx context.Context, accountID uuid.UUID, q trip.TrashedQuery) ([]trip.Resource, error) {
	var cursorAt *time.Time
	var cursorID uuid.NullUUID
	if q.After != nil {
		cursorAt = q.After.DeletedAt
		cursorID = uuid.NullUUID{UUID: q.After.ID, Valid: true}
	}
	rows, err := r.q.ListTrashedTrips(ctx, dbgen.ListTrashedTripsParams{AccountID: accountID, CursorDeletedAt: cursorAt, CursorID: cursorID, RowLimit: int32(q.Limit)})
	if err != nil {
		return nil, err
	}
	out := make([]trip.Resource, 0, len(rows))
	for _, row := range rows {
		res, err := toResource(row)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}
