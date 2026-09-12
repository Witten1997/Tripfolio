package httpapi

import (
	"context"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/trip"
	"tripfolio/server/internal/transport/httpapi/generated"
)

// 旅行与回收站处理器（接口设计 2.2）。只做参数转换与分发，规则在 trip.Service。

func nextCursor(c *string) nullable.Nullable[string] {
	if c == nil {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(*c)
}

// pageLimit 把 limit 查询参数转为服务层页大小：缺省为 0（取默认值）；显式 0 不是缺省，转为无效值交给服务校验。
func pageLimit(p *int) int {
	if p == nil {
		return 0
	}
	if *p == 0 {
		return -1
	}
	return *p
}

// ListTrips 实现 GET /trips。
func (h *Handler) ListTrips(ctx context.Context, req generated.ListTripsRequestObject) (generated.ListTripsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	f := trip.Filters{}
	if req.Params.Q != nil {
		f.Query = *req.Params.Q
	}
	if req.Params.Phase != nil {
		p := trip.Phase(*req.Params.Phase)
		f.Phase = &p
	}
	if req.Params.Archived != nil {
		f.Archived = trip.ArchivedFilter(*req.Params.Archived)
	}
	if req.Params.Sort != nil {
		f.Sort = trip.Sort(*req.Params.Sort)
	}
	f.Limit = pageLimit(req.Params.Limit)
	if req.Params.Cursor != nil {
		f.Cursor = *req.Params.Cursor
	}
	page, err := h.trips.List(ctx, a, f)
	if err != nil {
		return nil, err
	}
	return generated.ListTrips200JSONResponse{Items: page.Items, NextCursor: nextCursor(page.NextCursor)}, nil
}

// CreateTrip 实现 POST /trips。
func (h *Handler) CreateTrip(ctx context.Context, req generated.CreateTripRequestObject) (generated.CreateTripResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	b := req.Body
	cmd := trip.CreateCommand{
		ID: uuid.UUID(b.Id), Name: b.Name, StartDate: b.StartDate, EndDate: b.EndDate,
		Destination: b.Destination, Notes: b.Notes, Timezone: b.Timezone,
	}
	if b.CurrencyCode != nil {
		c := string(*b.CurrencyCode)
		cmd.CurrencyCode = &c
	}
	if b.BudgetAmount.IsSpecified() && !b.BudgetAmount.IsNull() {
		amount, _ := b.BudgetAmount.Get()
		cmd.BudgetAmount = &amount
	}
	res, err := h.trips.Create(ctx, a, uuid.UUID(req.Params.IdempotencyKey), cmd)
	if err != nil {
		return nil, err
	}
	return generated.CreateTrip201JSONResponse{Data: res}, nil
}

// GetTrip 实现 GET /trips/{trip_id}。
func (h *Handler) GetTrip(ctx context.Context, req generated.GetTripRequestObject) (generated.GetTripResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	r, err := h.trips.Get(ctx, a, uuid.UUID(req.TripId))
	if err != nil {
		return nil, err
	}
	tag := etag(r.Version)
	return generated.GetTrip200JSONResponse{Body: generated.TripResponse{Data: r}, Headers: generated.GetTrip200ResponseHeaders{ETag: &tag}}, nil
}

// UpdateTrip 实现 PATCH /trips/{trip_id}。
func (h *Handler) UpdateTrip(ctx context.Context, req generated.UpdateTripRequestObject) (generated.UpdateTripResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	base, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	patch := trip.Patch{Name: b.Name, Destination: b.Destination, Notes: b.Notes, Timezone: b.Timezone}
	if b.StartDate != nil {
		s := string(*b.StartDate)
		patch.StartDate = &s
	}
	if b.EndDate != nil {
		s := string(*b.EndDate)
		patch.EndDate = &s
	}
	if b.CurrencyCode != nil {
		c := string(*b.CurrencyCode)
		patch.CurrencyCode = &c
	}
	if b.BudgetAmount.IsSpecified() {
		patch.BudgetSet = true
		if !b.BudgetAmount.IsNull() {
			amount, _ := b.BudgetAmount.Get()
			patch.BudgetAmount = &amount
		}
	}
	res, err := h.trips.Update(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), base, patch)
	if err != nil {
		return nil, err
	}
	return generated.UpdateTrip200JSONResponse{Data: res}, nil
}

// TrashTrip 实现 DELETE /trips/{trip_id}。
func (h *Handler) TrashTrip(ctx context.Context, req generated.TrashTripRequestObject) (generated.TrashTripResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.trips.Trash(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), version)
	if err != nil {
		return nil, err
	}
	return generated.TrashTrip200JSONResponse{Data: res}, nil
}

// SetTripArchived 实现 POST /trips/{trip_id}/archive。
func (h *Handler) SetTripArchived(ctx context.Context, req generated.SetTripArchivedRequestObject) (generated.SetTripArchivedResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.trips.SetArchived(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), version, req.Body.Archived)
	if err != nil {
		return nil, err
	}
	return generated.SetTripArchived200JSONResponse{Data: res}, nil
}

// ListTrashedTrips 实现 GET /recycle-bin/trips。
func (h *Handler) ListTrashedTrips(ctx context.Context, req generated.ListTrashedTripsRequestObject) (generated.ListTrashedTripsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	limit, cursor := pageLimit(req.Params.Limit), ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	page, err := h.trips.ListTrashed(ctx, a, limit, cursor)
	if err != nil {
		return nil, err
	}
	return generated.ListTrashedTrips200JSONResponse{Items: page.Items, NextCursor: nextCursor(page.NextCursor)}, nil
}

// GetTrashedTrip 实现 GET /recycle-bin/trips/{trip_id}。
func (h *Handler) GetTrashedTrip(ctx context.Context, req generated.GetTrashedTripRequestObject) (generated.GetTrashedTripResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	r, err := h.trips.GetTrashed(ctx, a, uuid.UUID(req.TripId))
	if err != nil {
		return nil, err
	}
	tag := etag(r.Version)
	return generated.GetTrashedTrip200JSONResponse{Body: generated.TrashedTripResponse{Data: r}, Headers: generated.GetTrashedTrip200ResponseHeaders{ETag: &tag}}, nil
}

// RestoreTrip 实现 POST /recycle-bin/trips/{trip_id}/restore。
func (h *Handler) RestoreTrip(ctx context.Context, req generated.RestoreTripRequestObject) (generated.RestoreTripResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.trips.Restore(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), version)
	if err != nil {
		return nil, err
	}
	return generated.RestoreTrip200JSONResponse{Data: res}, nil
}

// PurgeTrip 实现 POST /recycle-bin/trips/{trip_id}/purge。
func (h *Handler) PurgeTrip(ctx context.Context, req generated.PurgeTripRequestObject) (generated.PurgeTripResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.trips.Purge(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), version, req.Body.Confirm)
	if err != nil {
		return nil, err
	}
	return generated.PurgeTrip202JSONResponse{Data: res}, nil
}
