package httpapi

import (
	"context"
	"strconv"
	"strings"

	"tripfolio/server/internal/foundation/apperr"
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

func (h *Handler) CalculateTripRoutes(ctx context.Context, req generated.CalculateTripRoutesRequestObject) (generated.CalculateTripRoutesResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.geo == nil {
		return nil, notWired()
	}
	points, err := parseRoutePoints(req.Params.Points)
	if err != nil {
		return nil, err
	}
	routes, err := h.geo.Routes(ctx, a, points, geo.Mode(req.Params.Mode))
	if err != nil {
		return nil, err
	}
	return generated.CalculateTripRoutes200JSONResponse{Data: routes}, nil
}

// parseRoutePoints 解析契约里的 points：`经度,纬度;经度,纬度`。数量与格式错误按 422 返回，
// 由调用方退回逐段调用（单段接口的坐标校验在模块内）。
func parseRoutePoints(raw string) ([]geo.Coordinate, error) {
	invalid := apperr.Validation(apperr.Field("points", "INVALID", "坐标格式应为 经度,纬度;经度,纬度"))
	parts := strings.Split(raw, ";")
	if len(parts) < 2 || len(parts) > geo.MaxTripRoutePoints {
		return nil, invalid
	}
	points := make([]geo.Coordinate, 0, len(parts))
	for _, part := range parts {
		longitudeRaw, latitudeRaw, found := strings.Cut(strings.TrimSpace(part), ",")
		if !found {
			return nil, invalid
		}
		longitude, err := strconv.ParseFloat(strings.TrimSpace(longitudeRaw), 64)
		if err != nil {
			return nil, invalid
		}
		latitude, err := strconv.ParseFloat(strings.TrimSpace(latitudeRaw), 64)
		if err != nil {
			return nil, invalid
		}
		point := geo.Coordinate{Latitude: latitude, Longitude: longitude}
		if !point.Valid() {
			return nil, invalid
		}
		points = append(points, point)
	}
	return points, nil
}
