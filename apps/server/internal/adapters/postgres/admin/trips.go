package adminpg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/modules/metadata"
)

func (s *Store) Trips(ctx context.Context, f admin.TripFilter, now time.Time) (admin.TripPage, error) {
	out := admin.TripPage{Data: []admin.TripSummary{}, Page: f.Page, PageSize: f.PageSize}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	const source = ` FROM trips t JOIN accounts a ON a.id=t.account_id
		CROSS JOIN LATERAL (SELECT CASE WHEN ($8::timestamptz AT TIME ZONE t.timezone)::date<t.start_date THEN 'planned' WHEN ($8::timestamptz AT TIME ZONE t.timezone)::date>t.end_date THEN 'ended' ELSE 'ongoing' END AS phase) p
		WHERE ($1::uuid IS NULL OR t.account_id=$1) AND ($2='' OR t.name ILIKE '%'||$2||'%')
		AND ($3='' OR p.phase=$3) AND ($4='all' OR (t.archived_at IS NOT NULL)=($4='true'))
		AND ($5='all' OR (t.deleted_at IS NOT NULL)=($5='only'))
		AND (nullif($6,'')::date IS NULL OR t.end_date>=nullif($6,'')::date)
		AND (nullif($7,'')::date IS NULL OR t.start_date<=nullif($7,'')::date)`
	search := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Query)
	args := []any{f.AccountID, search, f.Phase, f.Archived, f.Trash, f.DateFrom, f.DateTo, now}
	if err = tx.QueryRow(ctx, `SELECT count(*)`+source, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := tx.Query(ctx, `SELECT t.id,a.id,a.email,a.nickname,t.name,t.destination,to_char(t.start_date,'YYYY-MM-DD'),to_char(t.end_date,'YYYY-MM-DD'),p.phase,t.archived_at,t.deleted_at,t.updated_at`+source+` ORDER BY t.updated_at DESC,t.id DESC LIMIT $9 OFFSET $10`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v admin.TripSummary
		if err = rows.Scan(&v.ID, &v.Owner.ID, &v.Owner.Email, &v.Owner.Nickname, &v.Name, &v.Destination, &v.StartDate, &v.EndDate, &v.Phase, &v.ArchivedAt, &v.DeletedAt, &v.UpdatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Data = append(out.Data, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	return out, tx.Commit(ctx)
}

func (s *Store) Trip(ctx context.Context, id uuid.UUID, now time.Time) (admin.TripDetail, bool, error) {
	var out admin.TripDetail
	err := s.pool.QueryRow(ctx, `SELECT a.id,a.email,a.nickname,
		t.id,t.name,t.destination,to_char(t.start_date,'YYYY-MM-DD'),to_char(t.end_date,'YYYY-MM-DD'),
		t.budget_amount::text,t.currency_code,t.archived_at,t.deleted_at,t.purge_requested_at,t.updated_at,
		CASE WHEN ($2::timestamptz AT TIME ZONE t.timezone)::date<t.start_date THEN 'planned' WHEN ($2::timestamptz AT TIME ZONE t.timezone)::date>t.end_date THEN 'ended' ELSE 'ongoing' END,
		(SELECT count(*) FROM itinerary_items i WHERE i.account_id=t.account_id AND i.trip_id=t.id AND i.deleted_at IS NULL),
		(SELECT count(*) FROM packing_items p WHERE p.account_id=t.account_id AND p.trip_id=t.id AND p.deleted_at IS NULL),
		(SELECT count(*) FROM todo_items d WHERE d.account_id=t.account_id AND d.trip_id=t.id AND d.deleted_at IS NULL),
		(SELECT count(*) FROM trip_members m WHERE m.account_id=t.account_id AND m.trip_id=t.id AND m.deleted_at IS NULL)
		FROM trips t JOIN accounts a ON a.id=t.account_id WHERE t.id=$1`, id, now).Scan(
		&out.Owner.ID, &out.Owner.Email, &out.Owner.Nickname,
		&out.Trip.ID, &out.Trip.Name, &out.Trip.Destination, &out.Trip.StartDate, &out.Trip.EndDate,
		&out.Trip.BudgetAmount, &out.Trip.CurrencyCode, &out.Trip.ArchivedAt, &out.Trip.DeletedAt, &out.Trip.PurgeRequestedAt, &out.Trip.UpdatedAt,
		&out.Phase, &out.Counts.Itinerary, &out.Counts.Packing, &out.Counts.Todos, &out.Counts.Members)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if out.Trip.BudgetAmount != nil {
		units, ok := metadata.MinorUnits(out.Trip.CurrencyCode)
		if !ok {
			return out, false, fmt.Errorf("unsupported currency %s", out.Trip.CurrencyCode)
		}
		amount, err := money.FromStorage(*out.Trip.BudgetAmount, units)
		if err != nil {
			return out, false, err
		}
		out.Trip.BudgetAmount = &amount
	}
	return out, true, nil
}
