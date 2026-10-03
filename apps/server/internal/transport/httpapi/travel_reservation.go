package httpapi

import (
	"context"
	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/reservation"
	"tripfolio/server/internal/transport/httpapi/generated"
)

func (h *Handler) ListReservations(ctx context.Context, req generated.ListReservationsRequestObject) (generated.ListReservationsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.reservations == nil {
		return nil, notWired()
	}
	f := reservation.Filters{Limit: pageLimit(req.Params.Limit)}
	if req.Params.Cursor != nil {
		f.Cursor = *req.Params.Cursor
	}
	if req.Params.Kind != nil {
		f.Kind = string(*req.Params.Kind)
	}
	page, err := h.reservations.List(ctx, a, uuid.UUID(req.TripId), f)
	if err != nil {
		return nil, err
	}
	return generated.ListReservations200JSONResponse{Items: page.Items, NextCursor: nextCursor(page.NextCursor)}, nil
}
func (h *Handler) GetReservation(ctx context.Context, req generated.GetReservationRequestObject) (generated.GetReservationResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.reservations == nil {
		return nil, notWired()
	}
	v, err := h.reservations.Get(ctx, a, uuid.UUID(req.TripId), uuid.UUID(req.ReservationId))
	if err != nil {
		return nil, err
	}
	tag := etag(v.Version)
	return generated.GetReservation200JSONResponse{Body: generated.ReservationResponse{Data: v}, Headers: generated.GetReservation200ResponseHeaders{ETag: &tag}}, nil
}
func (h *Handler) CreateReservation(ctx context.Context, req generated.CreateReservationRequestObject) (generated.CreateReservationResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.reservations == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	b := req.Body
	p := reservation.Patch{}
	k := string(b.Kind)
	p.Kind = &k
	p.Title = &b.Title
	p.BookingReference = b.BookingReference
	p.TransportNumberSet, p.TransportNumber = nullableString(b.TransportNumber)
	p.ProviderNameSet, p.ProviderName = nullableString(b.ProviderName)
	p.StartLocalSet, p.StartLocal = contentNullableLocal(b.StartLocal)
	p.EndLocalSet, p.EndLocal = contentNullableLocal(b.EndLocal)
	p.OriginSet, p.Origin = nullableString(b.Origin)
	p.DestinationSet, p.Destination = nullableString(b.Destination)
	p.Address = b.Address
	p.ContactNameSet, p.ContactName = nullableString(b.ContactName)
	p.ContactPhoneSet, p.ContactPhone = nullableString(b.ContactPhone)
	p.Notes = b.Notes
	cmd := reservation.CreateCommand{ID: uuid.UUID(b.Id), Patch: p}
	res, err := h.reservations.Create(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), cmd)
	if err != nil {
		return nil, err
	}
	return generated.CreateReservation201JSONResponse{Data: res}, nil
}
func (h *Handler) UpdateReservation(ctx context.Context, req generated.UpdateReservationRequestObject) (generated.UpdateReservationResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.reservations == nil {
		return nil, notWired()
	}
	base, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	b := req.Body
	p := reservation.Patch{}
	if b.Kind != nil {
		k := string(*b.Kind)
		p.Kind = &k
	}
	p.Title = b.Title
	p.BookingReference = b.BookingReference
	p.TransportNumberSet, p.TransportNumber = nullableString(b.TransportNumber)
	p.ProviderNameSet, p.ProviderName = nullableString(b.ProviderName)
	p.StartLocalSet, p.StartLocal = contentNullableLocal(b.StartLocal)
	p.EndLocalSet, p.EndLocal = contentNullableLocal(b.EndLocal)
	p.OriginSet, p.Origin = nullableString(b.Origin)
	p.DestinationSet, p.Destination = nullableString(b.Destination)
	p.Address = b.Address
	p.ContactNameSet, p.ContactName = nullableString(b.ContactName)
	p.ContactPhoneSet, p.ContactPhone = nullableString(b.ContactPhone)
	p.Notes = b.Notes
	res, err := h.reservations.Update(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.ReservationId), base, p)
	if err != nil {
		return nil, err
	}
	return generated.UpdateReservation200JSONResponse{Data: res}, nil
}
func (h *Handler) DeleteReservation(ctx context.Context, req generated.DeleteReservationRequestObject) (generated.DeleteReservationResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.reservations == nil {
		return nil, notWired()
	}
	base, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.reservations.Delete(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.ReservationId), base)
	if err != nil {
		return nil, err
	}
	return generated.DeleteReservation200JSONResponse{Data: res}, nil
}
