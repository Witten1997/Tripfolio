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
