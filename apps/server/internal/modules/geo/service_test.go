package geo_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/modules/geo"
)

type keyRecorder struct{ key string }

func (k *keyRecorder) ReverseGeocode(context.Context, string, float64, float64) (geo.Place, error) {
	return geo.Place{}, nil
}
func (k *keyRecorder) SearchPlaces(context.Context, string, string, *float64, *float64, string) ([]geo.Place, error) {
	return nil, nil
}
func (k *keyRecorder) CalculateRoute(_ context.Context, key string, o, d geo.Coordinate, m geo.Mode) (geo.Route, error) {
	k.key = key
	return geo.Route{Mode: m, DistanceMeters: 1, DurationSeconds: 1, Path: []geo.Coordinate{o, d}, Provider: "amap"}, nil
}
func (k *keyRecorder) CalculateTripRoutes(_ context.Context, key string, points []geo.Coordinate, m geo.Mode) ([]geo.Route, error) {
	k.key = key
	routes := make([]geo.Route, 0, len(points)-1)
	for i := 0; i+1 < len(points); i++ {
		routes = append(routes, geo.Route{Mode: m, DistanceMeters: 1, DurationSeconds: 1, Path: []geo.Coordinate{points[i], points[i+1]}, Provider: "amap"})
	}
	return routes, nil
}

func TestRoutesUsesAccountKeyAndCallerKeyAndValidatesPoints(t *testing.T) {
	rec := &keyRecorder{}
	svc := geo.NewService(rec)
	a := actor.Actor{AccountID: uuid.New()}
	points := []geo.Coordinate{{Latitude: 35.1, Longitude: 139.1}, {Latitude: 35.2, Longitude: 139.2}, {Latitude: 35.3, Longitude: 139.3}}
	routes, err := svc.Routes(context.Background(), a, points, geo.Driving)
	if err != nil || rec.key != a.AccountID.String() || len(routes) != 2 {
		t.Fatalf("Routes 应以账号 ID 作限流键并返回相邻路段: %v %q %d", err, rec.key, len(routes))
	}
	if _, err := svc.RoutesAs(context.Background(), "share|abc", points, geo.Walking); err != nil || rec.key != "share|abc" {
		t.Fatalf("RoutesAs 应以调用方键限流: %v %q", err, rec.key)
	}
	if _, err := svc.RoutesAs(context.Background(), "share|abc", points[:1], geo.Driving); err == nil {
		t.Fatal("只有一个坐标应拒绝")
	}
	if _, err := svc.RoutesAs(context.Background(), "share|abc", append(points, make([]geo.Coordinate, geo.MaxTripRoutePoints)...), geo.Driving); err == nil {
		t.Fatal("坐标过多应拒绝")
	}
	if _, err := svc.RoutesAs(context.Background(), "share|abc", points, geo.Mode("flying")); err == nil {
		t.Fatal("非法出行方式应拒绝")
	}
}

func TestRouteUsesAccountKeyAndRouteAsUsesCallerKey(t *testing.T) {
	rec := &keyRecorder{}
	svc := geo.NewService(rec)
	a := actor.Actor{AccountID: uuid.New()}
	o, d := geo.Coordinate{Latitude: 35.1, Longitude: 139.1}, geo.Coordinate{Latitude: 35.2, Longitude: 139.2}
	if _, err := svc.Route(context.Background(), a, o, d, geo.Driving); err != nil || rec.key != a.AccountID.String() {
		t.Fatalf("Route 应以账号 ID 作限流键: %v %q", err, rec.key)
	}
	if _, err := svc.RouteAs(context.Background(), "share|abc", o, d, geo.Walking); err != nil || rec.key != "share|abc" {
		t.Fatalf("RouteAs 应以调用方键限流: %v %q", err, rec.key)
	}
	if _, err := svc.RouteAs(context.Background(), "share|abc", o, d, geo.Mode("flying")); err == nil {
		t.Fatal("非法出行方式应拒绝")
	}
}
