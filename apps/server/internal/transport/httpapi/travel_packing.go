package httpapi

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/packing"
	"tripfolio/server/internal/transport/httpapi/generated"
	"tripfolio/server/internal/transport/httpapi/middleware"
)

// 行李清单处理器（接口设计 2.4）。只做参数转换与分发，规则在 packing.Service。

// ListPackingLibrary 实现 GET /packing-library。
func (h *Handler) ListPackingLibrary(ctx context.Context, _ generated.ListPackingLibraryRequestObject) (generated.ListPackingLibraryResponseObject, error) {
	if _, err := mustActor(ctx); err != nil {
		return nil, err
	}
	// 物品库随程序发布，不依赖数据库，服务未装配时也可直接返回。
	return generated.ListPackingLibrary200JSONResponse{Data: packing.BuiltinLibrary()}, nil
}

// ListPackingItems 实现 GET /trips/{trip_id}/packing-items。
func (h *Handler) ListPackingItems(ctx context.Context, req generated.ListPackingItemsRequestObject) (generated.ListPackingItemsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.packing == nil {
		return nil, notWired()
	}
	f := packing.Filters{Limit: pageLimit(req.Params.Limit)}
	if req.Params.Category != nil {
		f.Category = string(*req.Params.Category)
	}
	if req.Params.Status != nil {
		f.Status = string(*req.Params.Status)
	}
	if req.Params.Cursor != nil {
		f.Cursor = *req.Params.Cursor
	}
	page, err := h.packing.List(ctx, a, uuid.UUID(req.TripId), f)
	if err != nil {
		return nil, err
	}
	return generated.ListPackingItems200JSONResponse{Items: page.Items, NextCursor: nextCursor(page.NextCursor)}, nil
}

// CreatePackingItem 实现 POST /trips/{trip_id}/packing-items。
func (h *Handler) CreatePackingItem(ctx context.Context, req generated.CreatePackingItemRequestObject) (generated.CreatePackingItemResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.packing == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	b := req.Body
	cmd := packing.CreateCommand{ID: uuid.UUID(b.Id), Name: b.Name, Category: packing.Category(b.Category), Quantity: b.Quantity, Notes: b.Notes}
	if b.Status != nil {
		s := string(*b.Status)
		cmd.Status = &s
	}
	res, err := h.packing.Create(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), cmd)
	if err != nil {
		return nil, err
	}
	return generated.CreatePackingItem201JSONResponse{Data: res}, nil
}

// GetPackingItem 实现 GET /trips/{trip_id}/packing-items/{item_id}。
func (h *Handler) GetPackingItem(ctx context.Context, req generated.GetPackingItemRequestObject) (generated.GetPackingItemResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.packing == nil {
		return nil, notWired()
	}
	r, err := h.packing.Get(ctx, a, uuid.UUID(req.TripId), uuid.UUID(req.ItemId))
	if err != nil {
		return nil, err
	}
	tag := etag(r.Version)
	return generated.GetPackingItem200JSONResponse{Body: generated.PackingItemResponse{Data: r}, Headers: generated.GetPackingItem200ResponseHeaders{ETag: &tag}}, nil
}

// UpdatePackingItem 实现 PATCH /trips/{trip_id}/packing-items/{item_id}。
func (h *Handler) UpdatePackingItem(ctx context.Context, req generated.UpdatePackingItemRequestObject) (generated.UpdatePackingItemResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.packing == nil {
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
	patch := packing.Patch{Name: b.Name, Quantity: b.Quantity, Notes: b.Notes}
	if b.Category != nil {
		c := packing.Category(*b.Category)
		patch.Category = &c
	}
	if b.Status != nil {
		s := packing.Status(*b.Status)
		patch.Status = &s
	}
	ctx, timings := write.WithTimings(ctx)
	started := time.Now()
	res, err := h.packing.Update(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.ItemId), base, patch)
	h.logPackingTiming(ctx, "packing.update", uuid.UUID(req.TripId), uuid.UUID(req.Params.IdempotencyKey), 1, len(res.Affected), res.Replayed, started, timings, err)
	if err != nil {
		return nil, err
	}
	return generated.UpdatePackingItem200JSONResponse{Data: res}, nil
}

// DeletePackingItem 实现 DELETE /trips/{trip_id}/packing-items/{item_id}。
func (h *Handler) DeletePackingItem(ctx context.Context, req generated.DeletePackingItemRequestObject) (generated.DeletePackingItemResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.packing == nil {
		return nil, notWired()
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.packing.Delete(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.ItemId), version)
	if err != nil {
		return nil, err
	}
	return generated.DeletePackingItem200JSONResponse{Data: res}, nil
}

// CreatePackingItems 实现 POST /trips/{trip_id}/packing-items/batch。
func (h *Handler) CreatePackingItems(ctx context.Context, req generated.CreatePackingItemsRequestObject) (generated.CreatePackingItemsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.packing == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	cmd := packing.BatchCommand{Items: make([]packing.BatchItem, len(req.Body.Items))}
	for i, it := range req.Body.Items {
		cmd.Items[i] = packing.BatchItem{ID: uuid.UUID(it.Id), Name: it.Name, Category: packing.Category(it.Category), Quantity: it.Quantity, Notes: it.Notes}
	}
	ctx, timings := write.WithTimings(ctx)
	started := time.Now()
	res, err := h.packing.CreateBatch(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), cmd)
	h.logPackingTiming(ctx, "packing.batch_create", uuid.UUID(req.TripId), uuid.UUID(req.Params.IdempotencyKey), len(cmd.Items), len(res.CreatedIDs), res.Replayed, started, timings, err)
	if err != nil {
		return nil, err
	}
	return generated.CreatePackingItems201JSONResponse{Data: res}, nil
}

func (h *Handler) logPackingTiming(ctx context.Context, operation string, tripID, operationID uuid.UUID, items, changed int, replayed bool, started time.Time, timings *write.Timings, err error) {
	stages := timings.Stages()
	keys := make([]string, 0, len(stages))
	for key := range stages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	attrs := make([]slog.Attr, 0, len(keys))
	for _, key := range keys {
		attrs = append(attrs, slog.Float64(key, float64(stages[key].Microseconds())/1000))
	}
	code := ""
	if err != nil {
		code = "INTERNAL_ERROR"
		if appErr, ok := apperr.As(err); ok {
			code = appErr.Code
		}
	}
	h.logger.InfoContext(ctx, "行李清单写入耗时",
		"request_id", middleware.RequestIDFrom(ctx),
		"operation", operation,
		"trip_id", tripID,
		"operation_id", operationID,
		"items", items,
		"changed", changed,
		"replayed", replayed,
		"error_code", code,
		"service_ms", float64(time.Since(started).Microseconds())/1000,
		"stages_ms", slog.GroupValue(attrs...),
	)
}
