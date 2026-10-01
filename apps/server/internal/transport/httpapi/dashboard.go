package httpapi

import (
	"context"

	"tripfolio/server/internal/modules/travel/dashboard"
	"tripfolio/server/internal/transport/httpapi/generated"
)

func (h *Handler) GetDashboard(ctx context.Context, req generated.GetDashboardRequestObject) (generated.GetDashboardResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.dashboard == nil {
		return nil, notWired()
	}
	q := dashboard.Query{}
	if req.Params.DateFrom != nil {
		q.DateFrom = *req.Params.DateFrom
	}
	if req.Params.DateTo != nil {
		q.DateTo = *req.Params.DateTo
	}
	if req.Params.LocationAfter != nil {
		q.LocationAfter = *req.Params.LocationAfter
	}
	snapshot, err := h.dashboard.Get(ctx, a, q)
	if err != nil {
		return nil, err
	}
	return generated.GetDashboard200JSONResponse{Data: snapshot}, nil
}
