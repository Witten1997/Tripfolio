package travelpg

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/content"
	"tripfolio/server/internal/modules/travel/document"
	"tripfolio/server/internal/modules/travel/reservation"
)

func toReservation(row dbgen.Reservation) (reservation.Resource, error) {
	return reservation.Resource{ID: row.ID, TripID: row.TripID, Version: types.Version(row.Version), CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt), DeletedAt: pgcore.UTCPtr(row.DeletedAt),
		Kind:             row.Kind,
		Title:            row.Title,
		BookingReference: row.BookingReference,
		TransportNumber:  row.TransportNumber,
		ProviderName:     row.ProviderName,
		StartLocal:       contentLocal(row.StartLocal),
		EndLocal:         contentLocal(row.EndLocal),
		Origin:           row.Origin,
		Destination:      row.Destination,
		Address:          row.Address,
		ContactName:      row.ContactName,
		ContactPhone:     row.ContactPhone,
		Notes:            row.Notes,
	}, nil
}

type reservationReader struct{ contentReader }

func NewReservationReader(pool *pgxpool.Pool) reservation.Reader {
	return &reservationReader{contentReader{dbgen.New(pool)}}
}
func (r *reservationReader) Get(ctx context.Context, accountID, tripID, id uuid.UUID) (reservation.Resource, bool, error) {
	row, err := r.q.GetReservation(ctx, dbgen.GetReservationParams{AccountID: accountID, TripID: tripID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		gone, e := r.q.ContentTombstoneExists(ctx, dbgen.ContentTombstoneExistsParams{AccountID: accountID, TripID: uuid.NullUUID{UUID: tripID, Valid: true}, EntityType: "reservation", EntityID: id})
		if e != nil {
			return reservation.Resource{}, false, e
		}
		if gone {
			return reservation.Resource{}, false, content.Gone()
		}
		return reservation.Resource{}, false, nil
	}
	if err != nil {
		return reservation.Resource{}, false, err
	}
	v, err := toReservation(row)
	return v, err == nil, err
}

type reservationRepo struct {
	reservationReader
	scope *pgcore.TxScope
}

func NewReservationUnitOfWork(w *pgcore.Writer) write.UnitOfWork[reservation.Repo] {
	return pgcore.NewUnitOfWork(w, func(s *pgcore.TxScope) reservation.Repo {
		return &reservationRepo{reservationReader: reservationReader{contentReader{s.Queries}}, scope: s}
	})
}
func (r *reservationRepo) MergeSource() write.MergeSource { return r.scope }
func (r *reservationRepo) IDExists(ctx context.Context, id uuid.UUID) (bool, error) {
	return r.q.ReservationIDExists(ctx, dbgen.ReservationIDExistsParams{ID: id, AccountID: r.scope.AccountID})
}
func (r *reservationRepo) UnlinkDocuments(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) ([]document.Resource, error) {
	rows, err := r.q.UnlinkReservationDocuments(ctx, dbgen.UnlinkReservationDocumentsParams{AccountID: accountID, TripID: tripID, ReservationID: uuid.NullUUID{UUID: id, Valid: true}, UpdatedAt: now})
	if err != nil {
		return nil, err
	}
	out := make([]document.Resource, 0, len(rows))
	for _, row := range rows {
		v, err := toDocument(row)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (r *reservationRepo) Insert(ctx context.Context, accountID uuid.UUID, v reservation.Resource) (reservation.Resource, error) {
	row, err := r.q.InsertReservation(ctx, dbgen.InsertReservationParams{AccountID: accountID, TripID: v.TripID, ID: v.ID, CreatedAt: v.CreatedAt,
		Kind:             v.Kind,
		Title:            v.Title,
		BookingReference: v.BookingReference,
		TransportNumber:  v.TransportNumber,
		ProviderName:     v.ProviderName,
		StartLocal:       contentTime(v.StartLocal),
		EndLocal:         contentTime(v.EndLocal),
		Origin:           v.Origin,
		Destination:      v.Destination,
		Address:          v.Address,
		ContactName:      v.ContactName,
		ContactPhone:     v.ContactPhone,
		Notes:            v.Notes,
	})
	if err != nil {
		return reservation.Resource{}, err
	}
	return toReservation(row)
}
func (r *reservationRepo) Update(ctx context.Context, accountID uuid.UUID, v reservation.Resource) (reservation.Resource, error) {
	row, err := r.q.UpdateReservation(ctx, dbgen.UpdateReservationParams{AccountID: accountID, TripID: v.TripID, ID: v.ID, UpdatedAt: v.UpdatedAt,
		Kind:             v.Kind,
		Title:            v.Title,
		BookingReference: v.BookingReference,
		TransportNumber:  v.TransportNumber,
		ProviderName:     v.ProviderName,
		StartLocal:       contentTime(v.StartLocal),
		EndLocal:         contentTime(v.EndLocal),
		Origin:           v.Origin,
		Destination:      v.Destination,
		Address:          v.Address,
		ContactName:      v.ContactName,
		ContactPhone:     v.ContactPhone,
		Notes:            v.Notes,
	})
	if err != nil {
		return reservation.Resource{}, err
	}
	return toReservation(row)
}
func (r *reservationRepo) SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (reservation.Resource, error) {
	row, err := r.q.SoftDeleteReservation(ctx, dbgen.SoftDeleteReservationParams{AccountID: accountID, TripID: tripID, ID: id, UpdatedAt: &now})
	if err != nil {
		return reservation.Resource{}, err
	}
	return toReservation(row)
}
func (r *reservationReader) List(ctx context.Context, accountID, tripID uuid.UUID, q reservation.ListQuery) ([]reservation.Resource, error) {
	p := dbgen.ListReservationsParams{AccountID: accountID, TripID: tripID, PageLimit: int32(q.Limit), Kind: contentKind(q.Kind)}
	if q.After != nil {
		p.AfterID = q.After.ID
		p.AfterTime = &q.After.Time
	}
	rows, err := r.q.ListReservations(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]reservation.Resource, 0, len(rows))
	for _, row := range rows {
		v, err := toReservation(row)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func NewReservationRepository(scope *pgcore.TxScope) reservation.Repo {
	return &reservationRepo{reservationReader: reservationReader{contentReader{scope.Queries}}, scope: scope}
}
