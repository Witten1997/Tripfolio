package httpapi

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/transport/httpapi/generated"
)

// 每日行程处理器（接口设计 2.3）。只做参数转换与分发，规则在 itinerary.Service。

// errNotWired 表示服务尚未装配（PostgreSQL 适配在切片 3.3 接入）。
var errNotWired = errors.New("service not wired")

func notWired() error { return apperr.Dependency(errNotWired) }

// nullableString 把 nullable 字段转为 (set, value)：set 表示字段出现（含显式 null），value 为非 null 时的值。
func nullableString(n nullable.Nullable[string]) (bool, *string) {
	if !n.IsSpecified() {
		return false, nil
	}
	if n.IsNull() {
		return true, nil
	}
	v, _ := n.Get()
	return true, &v
}

func nullableInt32(n nullable.Nullable[int32]) (bool, *int32) {
	if !n.IsSpecified() {
		return false, nil
	}
	if n.IsNull() {
		return true, nil
	}
	v, _ := n.Get()
	return true, &v
}

func nullableFloat(n nullable.Nullable[float64]) (bool, *float64) {
	if !n.IsSpecified() {
		return false, nil
	}
	if n.IsNull() {
		return true, nil
	}
	v, _ := n.Get()
	return true, &v
}

func currencyPtr(c *generated.CurrencyCode) *string {
	if c == nil {
		return nil
	}
	s := string(*c)
	return &s
}

// ListItineraryItems 实现 GET /trips/{trip_id}/itinerary-items。
func (h *Handler) ListItineraryItems(ctx context.Context, req generated.ListItineraryItemsRequestObject) (generated.ListItineraryItemsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.itinerary == nil {
		return nil, notWired()
	}
	f := itinerary.Filters{Limit: pageLimit(req.Params.Limit)}
	if req.Params.DateFrom != nil {
		f.DateFrom = string(*req.Params.DateFrom)
	}
	if req.Params.DateTo != nil {
		f.DateTo = string(*req.Params.DateTo)
	}
	if req.Params.Status != nil {
		f.Status = string(*req.Params.Status)
	}
	if req.Params.Cursor != nil {
		f.Cursor = *req.Params.Cursor
	}
	page, err := h.itinerary.List(ctx, a, uuid.UUID(req.TripId), f)
	if err != nil {
		return nil, err
	}
	return generated.ListItineraryItems200JSONResponse{Items: page.Items, NextCursor: nextCursor(page.NextCursor)}, nil
}

// CreateItineraryItem 实现 POST /trips/{trip_id}/itinerary-items。
func (h *Handler) CreateItineraryItem(ctx context.Context, req generated.CreateItineraryItemRequestObject) (generated.CreateItineraryItemResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.itinerary == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	b := req.Body
	cmd := itinerary.CreateCommand{
		ID: uuid.UUID(b.Id), Title: b.Title, Kind: itinerary.Kind(b.Kind), ScheduledOn: string(b.ScheduledOn),
		PlaceName: b.PlaceName, Address: b.Address, Notes: b.Notes, ActualNotes: b.ActualNotes, CurrencyCode: currencyPtr(b.CurrencyCode),
	}
	// 创建时显式 null 与缺省等价，都取默认值。
	_, cmd.PlannedStartLocal = nullableString(b.PlannedStartLocal)
	_, cmd.PlannedEndLocal = nullableString(b.PlannedEndLocal)
	_, cmd.PlannedDurationMinutes = nullableInt32(b.PlannedDurationMinutes)
	_, cmd.Latitude = nullableFloat(b.Latitude)
	_, cmd.Longitude = nullableFloat(b.Longitude)
	_, cmd.EstimatedAmount = nullableString(b.EstimatedAmount)
	_, cmd.ActualStartLocal = nullableString(b.ActualStartLocal)
	_, cmd.ActualEndLocal = nullableString(b.ActualEndLocal)
	if b.Status != nil {
		s := string(*b.Status)
		cmd.Status = &s
	}
	res, err := h.itinerary.Create(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), cmd)
	if err != nil {
		return nil, err
	}
	return generated.CreateItineraryItem201JSONResponse{Data: res}, nil
}

// GetItineraryItem 实现 GET /trips/{trip_id}/itinerary-items/{item_id}。
func (h *Handler) GetItineraryItem(ctx context.Context, req generated.GetItineraryItemRequestObject) (generated.GetItineraryItemResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.itinerary == nil {
		return nil, notWired()
	}
	r, err := h.itinerary.Get(ctx, a, uuid.UUID(req.TripId), uuid.UUID(req.ItemId))
	if err != nil {
		return nil, err
	}
	tag := etag(r.Version)
	return generated.GetItineraryItem200JSONResponse{Body: generated.ItineraryItemResponse{Data: r}, Headers: generated.GetItineraryItem200ResponseHeaders{ETag: &tag}}, nil
}

// UpdateItineraryItem 实现 PATCH /trips/{trip_id}/itinerary-items/{item_id}。
func (h *Handler) UpdateItineraryItem(ctx context.Context, req generated.UpdateItineraryItemRequestObject) (generated.UpdateItineraryItemResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.itinerary == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	base, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	patch := itinerary.Patch{
		Title: b.Title, PlaceName: b.PlaceName, Address: b.Address, Notes: b.Notes, ActualNotes: b.ActualNotes,
		CurrencyCode: currencyPtr(b.CurrencyCode),
	}
	if b.Kind != nil {
		k := itinerary.Kind(*b.Kind)
		patch.Kind = &k
	}
	if b.Status != nil {
		s := itinerary.Status(*b.Status)
		patch.Status = &s
	}
	patch.PlannedStartSet, patch.PlannedStartLocal = nullableString(b.PlannedStartLocal)
	patch.PlannedEndSet, patch.PlannedEndLocal = nullableString(b.PlannedEndLocal)
	patch.PlannedDurationSet, patch.PlannedDurationMinutes = nullableInt32(b.PlannedDurationMinutes)
	patch.LatitudeSet, patch.Latitude = nullableFloat(b.Latitude)
	patch.LongitudeSet, patch.Longitude = nullableFloat(b.Longitude)
	patch.EstimatedAmountSet, patch.EstimatedAmount = nullableString(b.EstimatedAmount)
	patch.ActualStartSet, patch.ActualStartLocal = nullableString(b.ActualStartLocal)
	patch.ActualEndSet, patch.ActualEndLocal = nullableString(b.ActualEndLocal)
	res, err := h.itinerary.Update(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.ItemId), base, patch)
	if err != nil {
		return nil, err
	}
	return generated.UpdateItineraryItem200JSONResponse{Data: res}, nil
}

// DeleteItineraryItem 实现 DELETE /trips/{trip_id}/itinerary-items/{item_id}。
func (h *Handler) DeleteItineraryItem(ctx context.Context, req generated.DeleteItineraryItemRequestObject) (generated.DeleteItineraryItemResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.itinerary == nil {
		return nil, notWired()
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.itinerary.Delete(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.ItemId), version)
	if err != nil {
		return nil, err
	}
	return generated.DeleteItineraryItem200JSONResponse{Data: res}, nil
}

// ReorderItineraryItems 实现 POST /trips/{trip_id}/itinerary-items/reorder。
func (h *Handler) ReorderItineraryItems(ctx context.Context, req generated.ReorderItineraryItemsRequestObject) (generated.ReorderItineraryItemsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.itinerary == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	cmd := itinerary.ReorderCommand{Days: make([]itinerary.ReorderDay, len(req.Body.Days))}
	for i, day := range req.Body.Days {
		items := make([]itinerary.ReorderItem, len(day.Items))
		for j, it := range day.Items {
			v, err := parseVersionField(it.BaseVersion)
			if err != nil {
				return nil, err
			}
			items[j] = itinerary.ReorderItem{ID: uuid.UUID(it.Id), BaseVersion: v}
		}
		cmd.Days[i] = itinerary.ReorderDay{Date: string(day.Date), Items: items}
	}
	res, err := h.itinerary.Reorder(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), cmd)
	if err != nil {
		return nil, err
	}
	return generated.ReorderItineraryItems200JSONResponse{Data: res}, nil
}
