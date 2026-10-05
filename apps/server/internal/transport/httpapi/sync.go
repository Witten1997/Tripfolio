package httpapi

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/transport/httpapi/generated"
	"tripfolio/server/internal/transport/httpapi/middleware"
)

func syncProtocol(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (h *Handler) CreateSyncSnapshot(ctx context.Context, req generated.CreateSyncSnapshotRequestObject) (generated.CreateSyncSnapshotResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if err = syncBearer(ctx); err != nil {
		return nil, err
	}
	if h.sync == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	result, err := h.sync.CreateSnapshot(ctx, a, syncProtocol(req.Params.XTripfolioSyncVersion), uuid.UUID(req.Params.IdempotencyKey), *req.Body)
	if err != nil {
		return nil, err
	}
	return generated.CreateSyncSnapshot202JSONResponse{Data: result}, nil
}

func (h *Handler) GetSyncSnapshot(ctx context.Context, req generated.GetSyncSnapshotRequestObject) (generated.GetSyncSnapshotResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if err = syncBearer(ctx); err != nil {
		return nil, err
	}
	if h.sync == nil {
		return nil, notWired()
	}
	result, err := h.sync.GetSnapshot(ctx, a, syncProtocol(req.Params.XTripfolioSyncVersion), uuid.UUID(req.SnapshotId))
	if err != nil {
		return nil, err
	}
	return generated.GetSyncSnapshot200JSONResponse(result), nil
}

func (h *Handler) GetSyncSnapshotItems(ctx context.Context, req generated.GetSyncSnapshotItemsRequestObject) (generated.GetSyncSnapshotItemsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if err = syncBearer(ctx); err != nil {
		return nil, err
	}
	if h.sync == nil {
		return nil, notWired()
	}
	token := ""
	if req.Params.Cursor != nil {
		token = *req.Params.Cursor
	}
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
		if limit == 0 {
			return nil, apperr.Validation(apperr.Field("limit", "INVALID", "limit 须在 1–500 之间"))
		}
	}
	result, err := h.sync.SnapshotItems(ctx, a, syncProtocol(req.Params.XTripfolioSyncVersion), uuid.UUID(req.SnapshotId), token, limit)
	if err != nil {
		return nil, err
	}
	return generated.GetSyncSnapshotItems200JSONResponse(result), nil
}

func syncBearer(ctx context.Context) error {
	r := middleware.RequestFrom(ctx)
	if r == nil || !strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ") {
		return apperr.Unauthorized("AUTH_REQUIRED", "同步需要原生 Bearer 会话")
	}
	if r.Header.Get("X-Tripfolio-Share-Token") != "" {
		return apperr.Forbidden("NATIVE_SESSION_REQUIRED", "同步不接受分享身份")
	}
	return nil
}

func (h *Handler) GetSyncStatus(ctx context.Context, _ generated.GetSyncStatusRequestObject) (generated.GetSyncStatusResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if err := syncBearer(ctx); err != nil {
		return nil, err
	}
	if h.sync == nil {
		return nil, notWired()
	}
	result, err := h.sync.Status(ctx, a)
	if err != nil {
		return nil, err
	}
	return generated.GetSyncStatus200JSONResponse(result), nil
}

func (h *Handler) GetSyncChanges(ctx context.Context, req generated.GetSyncChangesRequestObject) (generated.GetSyncChangesResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if err := syncBearer(ctx); err != nil {
		return nil, err
	}
	if h.sync == nil {
		return nil, notWired()
	}
	protocol := ""
	if req.Params.XTripfolioSyncVersion != nil {
		protocol = *req.Params.XTripfolioSyncVersion
	}
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
		if limit == 0 {
			return nil, apperr.Validation(apperr.Field("limit", "INVALID", "limit 须在 1–500 之间"))
		}
	}
	result, err := h.sync.Changes(ctx, a, protocol, req.Params.Cursor, limit)
	if err != nil {
		return nil, err
	}
	return generated.GetSyncChanges200JSONResponse(result), nil
}
