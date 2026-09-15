package httpapi

import (
	"context"

	"github.com/google/uuid"

	"tripfolio/server/internal/transport/httpapi/generated"
)

// 旅行分享处理器（接口设计 2.11）。写接口不带幂等键与 If-Match：分享不进入同步体系。

func (h *Handler) GetTripShare(ctx context.Context, req generated.GetTripShareRequestObject) (generated.GetTripShareResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.shares == nil {
		return nil, notWired()
	}
	r, err := h.shares.Get(ctx, a, uuid.UUID(req.TripId))
	if err != nil {
		return nil, err
	}
	return generated.GetTripShare200JSONResponse{Data: r}, nil
}

func (h *Handler) EnableTripShare(ctx context.Context, req generated.EnableTripShareRequestObject) (generated.EnableTripShareResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.shares == nil {
		return nil, notWired()
	}
	r, err := h.shares.Enable(ctx, a, uuid.UUID(req.TripId))
	if err != nil {
		return nil, err
	}
	return generated.EnableTripShare200JSONResponse{Data: r}, nil
}

func (h *Handler) RotateTripShare(ctx context.Context, req generated.RotateTripShareRequestObject) (generated.RotateTripShareResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.shares == nil {
		return nil, notWired()
	}
	r, err := h.shares.Rotate(ctx, a, uuid.UUID(req.TripId))
	if err != nil {
		return nil, err
	}
	return generated.RotateTripShare200JSONResponse{Data: r}, nil
}

func (h *Handler) DisableTripShare(ctx context.Context, req generated.DisableTripShareRequestObject) (generated.DisableTripShareResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.shares == nil {
		return nil, notWired()
	}
	if err := h.shares.Disable(ctx, a, uuid.UUID(req.TripId)); err != nil {
		return nil, err
	}
	return generated.DisableTripShare204Response{}, nil
}
