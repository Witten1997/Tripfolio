package travelpg

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/travel/content"
)

type contentReader struct{ q *dbgen.Queries }

func (r contentReader) Trip(ctx context.Context, accountID, tripID uuid.UUID) (content.TripInfo, bool, error) {
	v, err := r.q.GetTripContentInfo(ctx, dbgen.GetTripContentInfoParams{AccountID: accountID, ID: tripID})
	if errors.Is(err, pgx.ErrNoRows) {
		return content.TripInfo{}, false, nil
	}
	return content.TripInfo{Timezone: v.Timezone, DeletedAt: pgcore.UTCPtr(v.DeletedAt)}, err == nil, err
}
func (r contentReader) Asset(ctx context.Context, accountID, tripID, id uuid.UUID) (content.AssetInfo, bool, error) {
	v, err := r.q.GetContentAssetReference(ctx, dbgen.GetContentAssetReferenceParams{AccountID: accountID, TripID: uuid.NullUUID{UUID: tripID, Valid: true}, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return content.AssetInfo{}, false, nil
	}
	return content.AssetInfo{MediaType: v.MediaType, DeletedAt: pgcore.UTCPtr(v.DeletedAt)}, err == nil, err
}
func (r contentReader) Reservation(ctx context.Context, accountID, tripID, id uuid.UUID) (bool, error) {
	deleted, err := r.q.GetContentReservationReference(ctx, dbgen.GetContentReservationReferenceParams{AccountID: accountID, TripID: tripID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if deleted != nil {
		return false, content.Invalid("reservation_id", "预订已删除")
	}
	return true, nil
}
func contentLocal(t *time.Time) *types.LocalDateTime {
	if t == nil {
		return nil
	}
	v := types.LocalDateTimeOf(*t)
	return &v
}
func contentTime(t *types.LocalDateTime) *time.Time {
	if t == nil {
		return nil
	}
	v := t.Time()
	return &v
}
func contentUUID(u *uuid.UUID) uuid.NullUUID {
	if u == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *u, Valid: true}
}
func contentUUIDPtr(u uuid.NullUUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	v := u.UUID
	return &v
}
func contentNumber(v *string) (*float64, error) {
	if v == nil {
		return nil, nil
	}
	n, err := strconv.ParseFloat(*v, 64)
	return &n, err
}
func contentNumeric(v *float64) *string {
	if v == nil {
		return nil
	}
	s := strconv.FormatFloat(*v, 'f', 6, 64)
	return &s
}
func contentDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	v := types.Date(s).Time()
	return &v
}
func contentKind(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
