package httpapi

import (
	"context"

	"tripfolio/server/internal/modules/geo"
	"tripfolio/server/internal/modules/travel/share"
	"tripfolio/server/internal/transport/httpapi/generated"
)

// 访客分享处理器（接口设计 2.11）。只从 share.Viewer 取身份，永远不读 actor.Actor。

func (h *Handler) GetSharedTrip(ctx context.Context, _ generated.GetSharedTripRequestObject) (generated.GetSharedTripResponseObject, error) {
	v, err := mustViewer(ctx)
	if err != nil {
		return nil, err
	}
	if h.shares == nil {
		return nil, notWired()
	}
	t, err := h.shares.ViewTrip(ctx, v)
	if err != nil {
		return nil, err
	}
	return generated.GetSharedTrip200JSONResponse{Body: generated.PublicTripResponse{Data: t}}, nil
}

func (h *Handler) ListSharedItineraryItems(ctx context.Context, req generated.ListSharedItineraryItemsRequestObject) (generated.ListSharedItineraryItemsResponseObject, error) {
	v, err := mustViewer(ctx)
	if err != nil {
		return nil, err
	}
	if h.shares == nil {
		return nil, notWired()
	}
	f := share.ListFilters{Limit: pageLimit(req.Params.Limit)}
	if req.Params.Cursor != nil {
		f.Cursor = *req.Params.Cursor
	}
	page, err := h.shares.ListItems(ctx, v, f)
	if err != nil {
		return nil, err
	}
	return generated.ListSharedItineraryItems200JSONResponse{Items: page.Items, NextCursor: nextCursor(page.NextCursor)}, nil
}

func (h *Handler) GetSharedRoutes(ctx context.Context, req generated.GetSharedRoutesRequestObject) (generated.GetSharedRoutesResponseObject, error) {
	v, err := mustViewer(ctx)
	if err != nil {
		return nil, err
	}
	if h.shares == nil {
		return nil, notWired()
	}
	routes, err := h.shares.Routes(ctx, v, geo.Mode(req.Params.Mode))
	if err != nil {
		return nil, err
	}
	return generated.GetSharedRoutes200JSONResponse{Data: routes}, nil
}
