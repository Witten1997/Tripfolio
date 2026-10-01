package adminpg

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/modules/travel/member"
	"tripfolio/server/internal/modules/travel/packing"
	"tripfolio/server/internal/modules/travel/todo"
	"tripfolio/server/internal/modules/travel/trip"
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

func readAdminTrip(ctx context.Context, tx pgx.Tx, id uuid.UUID, now time.Time) (admin.TripDetail, bool, error) {
	var out admin.TripDetail
	err := tx.QueryRow(ctx, `SELECT a.id,a.email,a.nickname FROM trips t JOIN accounts a ON a.id=t.account_id WHERE t.id=$1`, id).Scan(&out.Owner.ID, &out.Owner.Email, &out.Owner.Nickname)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	row, err := dbgen.New(tx).GetTrip(ctx, dbgen.GetTripParams{ID: id, AccountID: out.Owner.ID})
	if err != nil {
		return out, false, err
	}
	out.Trip, err = travelpg.ToTripResource(row)
	if err != nil {
		return out, false, err
	}
	out.Phase = trip.PhaseOf(out.Trip, now)
	return out, true, nil
}
func (s *Store) Trip(ctx context.Context, id uuid.UUID, now time.Time) (admin.TripDetail, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return admin.TripDetail{}, false, err
	}
	defer tx.Rollback(ctx)
	out, found, err := readAdminTrip(ctx, tx, id, now)
	if err != nil {
		return out, found, err
	}
	return out, found, tx.Commit(ctx)
}

func contentRows[T any](ctx context.Context, tx pgx.Tx, query string, args ...any) ([]T, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[T])
}
func (s *Store) TripContent(ctx context.Context, id uuid.UUID, f admin.ContentFilter) (admin.ContentPage, bool, error) {
	out := admin.ContentPage{TripID: id, Kind: f.Kind, Page: f.Page, PageSize: f.PageSize, Itinerary: []itinerary.Resource{}, Packing: []packing.Resource{}, Todos: []todo.Resource{}, Members: []member.Resource{}}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, false, err
	}
	defer tx.Rollback(ctx)
	var currency string
	err = tx.QueryRow(ctx, `SELECT account_id,currency_code FROM trips WHERE id=$1`, id).Scan(&out.OwnerID, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	// 表名与排序仅由固定白名单选择，任何客户端文本都不能拼入 SQL。
	var table, order string
	switch f.Kind {
	case "itinerary":
		table, order = "itinerary_items", "scheduled_on,sort_order,id"
	case "packing":
		table, order = "packing_items", "category,created_at,id"
	case "todos":
		table, order = "todo_items", "due_on NULLS LAST,created_at,id"
	case "members":
		table, order = "trip_members", "sort_order,id"
	default:
		return out, false, errors.New("unknown admin content kind")
	}
	where := ` FROM ` + table + ` WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL`
	if err = tx.QueryRow(ctx, `SELECT count(*)`+where, out.OwnerID, id).Scan(&out.Total); err != nil {
		return out, false, err
	}
	query := `SELECT *` + where + ` ORDER BY ` + order + ` LIMIT $3 OFFSET $4`
	args := []any{out.OwnerID, id, f.PageSize, (f.Page - 1) * f.PageSize}
	switch f.Kind {
	case "itinerary":
		rows, e := contentRows[dbgen.ItineraryItem](ctx, tx, query, args...)
		if e != nil {
			return out, false, e
		}
		for _, row := range rows {
			v, e := travelpg.ToItineraryResource(row, currency)
			if e != nil {
				return out, false, e
			}
			out.Itinerary = append(out.Itinerary, v)
		}
	case "packing":
		rows, e := contentRows[dbgen.PackingItem](ctx, tx, query, args...)
		if e != nil {
			return out, false, e
		}
		for _, row := range rows {
			out.Packing = append(out.Packing, travelpg.ToPackingResource(row))
		}
	case "todos":
		rows, e := contentRows[dbgen.TodoItem](ctx, tx, query, args...)
		if e != nil {
			return out, false, e
		}
		for _, row := range rows {
			out.Todos = append(out.Todos, travelpg.ToTodoResource(row))
		}
	case "members":
		rows, e := contentRows[dbgen.TripMember](ctx, tx, query, args...)
		if e != nil {
			return out, false, e
		}
		for _, row := range rows {
			v, e := travelpg.ToMemberResource(row)
			if e != nil {
				return out, false, e
			}
			out.Members = append(out.Members, v)
		}
	}
	return out, true, tx.Commit(ctx)
}
