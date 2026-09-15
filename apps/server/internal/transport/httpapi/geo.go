package httpapi

import (
	"context"

	"tripfolio/server/internal/modules/geo"
	"tripfolio/server/internal/transport/httpapi/generated"
)

func (h *Handler) ReverseGeocode(ctx context.Context, req generated.ReverseGeocodeRequestObject) (generated.ReverseGeocodeResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.geo == nil {
		return nil, notWired()
	}
	place, err := h.geo.Reverse(ctx, a, geo.Coordinate{Latitude: req.Params.Latitude, Longitude: req.Params.Longitude})
	if err != nil {
		return nil, err
	}
	return generated.ReverseGeocode200JSONResponse{Data: place}, nil
}

func (h *Handler) SearchPlaces(ctx context.Context, req generated.SearchPlacesRequestObject) (generated.SearchPlacesResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.geo == nil {
		return nil, notWired()
	}
	city := ""
	if req.Params.City != nil {
		city = *req.Params.City
	}
	places, err := h.geo.Search(ctx, a, req.Params.Q, city, req.Params.Latitude, req.Params.Longitude)
	if err != nil {
		return nil, err
	}
	return generated.SearchPlaces200JSONResponse{Data: places}, nil
}

func (h *Handler) CalculateRoute(ctx context.Context, req generated.CalculateRouteRequestObject) (generated.CalculateRouteResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.geo == nil {
		return nil, notWired()
	}
	p := req.Params
	route, err := h.geo.Route(ctx, a,
		geo.Coordinate{Latitude: p.OriginLatitude, Longitude: p.OriginLongitude},
		geo.Coordinate{Latitude: p.DestinationLatitude, Longitude: p.DestinationLongitude}, geo.Mode(p.Mode))
	if err != nil {
		return nil, err
	}
	return generated.CalculateRoute200JSONResponse{Data: route}, nil
}
