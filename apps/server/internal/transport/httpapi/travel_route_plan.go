package httpapi

import (
	"context"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/routeplan"
	"tripfolio/server/internal/transport/httpapi/generated"
)

func (h *Handler) GetRoutePlan(ctx context.Context, req generated.GetRoutePlanRequestObject) (generated.GetRoutePlanResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	plan, err := h.routePlans.Get(ctx, a, uuid.UUID(req.TripId))
	if err != nil {
		return nil, err
	}
	return generated.GetRoutePlan200JSONResponse{Data: plan}, nil
}

func (h *Handler) RecalculateRoutePlan(ctx context.Context, req generated.RecalculateRoutePlanRequestObject) (generated.RecalculateRoutePlanResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	result, err := h.routePlans.RequestRecalculate(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId))
	if err != nil {
		return nil, err
	}
	return generated.RecalculateRoutePlan200JSONResponse{Data: result}, nil
}

func (h *Handler) UpdateRouteLegMode(ctx context.Context, req generated.UpdateRouteLegModeRequestObject) (generated.UpdateRouteLegModeResponseObject, error) {
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
	result, err := h.routePlans.SetLegMode(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId),
		uuid.UUID(req.LegId), version, routeplan.Mode(req.Body.Mode))
	if err != nil {
		return nil, err
	}
	return generated.UpdateRouteLegMode200JSONResponse{Data: result}, nil
}
