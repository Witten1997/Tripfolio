package share_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/modules/geo"
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/modules/travel/share"
)

func threeStops(f *fixture) {
	f.items.items = []itinerary.Resource{
		item(f.tripID, "2026-10-01", 0, "A", 35.1, 139.1),
		item(f.tripID, "2026-10-01", 1, "B", 35.2, 139.2),
		item(f.tripID, "2026-10-01", 2, "C", 35.3, 139.3),
	}
}

var (
	legAB = legKey(geo.Coordinate{Latitude: 35.1, Longitude: 139.1}, geo.Coordinate{Latitude: 35.2, Longitude: 139.2})
)

func TestRoutesSkipsUnlocatedItemsAndKeepsAdjacency(t *testing.T) {
	f := newFixture(t)
	f.items.items = []itinerary.Resource{
		item(f.tripID, "2026-10-01", 0, "机场", 35.5494, 139.7798),
		item(f.tripID, "2026-10-01", 1, "酒店", 0, 0),
		item(f.tripID, "2026-10-02", 0, "浅草寺", 35.7148, 139.7967),
		item(f.tripID, "2026-10-02", 1, "晴空塔", 35.7101, 139.8107),
	}
	v := f.viewer(t)
	out, err := f.svc.Routes(context.Background(), v, geo.Driving)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Legs) != 2 || out.UnlocatedItemCount != 1 || out.FailedLegCount != 0 || out.Mode != geo.Driving {
		t.Fatalf("routes: %+v", out)
	}
	if out.Legs[0].FromItemID != f.items.items[0].ID || out.Legs[0].ToItemID != f.items.items[2].ID || out.Legs[1].ToItemID != f.items.items[3].ID {
		t.Fatalf("相邻关系应跳过无坐标项目并跨日接续: %+v", out.Legs)
	}
	if out.Legs[0].DistanceMeters != 1000 || len(out.Legs[0].Path) != 2 {
		t.Fatalf("leg: %+v", out.Legs[0])
	}
	if f.routes.lastKey != "share|"+v.ShareID.String() {
		t.Fatalf("访客算路应以分享 ID 作限流键: %q", f.routes.lastKey)
	}
}

func TestRoutesCachesWholeTripUntilItineraryChangesOrExpires(t *testing.T) {
	f := newFixture(t)
	threeStops(f)
	v := f.viewer(t)
	run := func(mode geo.Mode) {
		if _, err := f.svc.Routes(context.Background(), v, mode); err != nil {
			t.Fatal(err)
		}
	}
	run(geo.Walking)
	run(geo.Walking)
	if f.routes.calls != 2 {
		t.Fatalf("第二次应命中整趟缓存，实际上游调用 %d 次", f.routes.calls)
	}
	run(geo.Cycling)
	if f.routes.calls != 4 {
		t.Fatalf("不同出行方式不共用缓存: %d", f.routes.calls)
	}
	moved := 35.25
	f.items.items[1].Latitude = &moved
	run(geo.Walking)
	if f.routes.calls != 6 {
		t.Fatalf("坐标变化后指纹应失效并重算: %d", f.routes.calls)
	}
	f.clock.Advance(6 * time.Minute)
	run(geo.Walking)
	if f.routes.calls != 8 {
		t.Fatalf("5 分钟后缓存过期应重算: %d", f.routes.calls)
	}
}

func TestRoutesSingleLegFailureDoesNotStopOthersAndIsNotCached(t *testing.T) {
	f := newFixture(t)
	threeStops(f)
	f.routes.errs[legAB] = apperr.NotFound()
	v := f.viewer(t)
	out, err := f.svc.Routes(context.Background(), v, geo.Driving)
	if err != nil || len(out.Legs) != 1 || out.FailedLegCount != 1 {
		t.Fatalf("单段无路线应继续算后续路段: %v %+v", err, out)
	}
	busy := apperr.Dependency(errors.New("busy"))
	busy.Headers = map[string]string{"Retry-After": "1"}
	f.routes.errs[legAB] = busy
	out, err = f.svc.Routes(context.Background(), v, geo.Driving)
	if err != nil || len(out.Legs) != 1 || out.FailedLegCount != 1 {
		t.Fatalf("带 Retry-After 的暂时性 503 只算单段失败: %v %+v", err, out)
	}
	delete(f.routes.errs, legAB)
	out, err = f.svc.Routes(context.Background(), v, geo.Driving)
	if err != nil || len(out.Legs) != 2 || out.FailedLegCount != 0 {
		t.Fatalf("失败结果不应被缓存，恢复后应重算: %v %+v", err, out)
	}
}

func TestRoutesQuotaOrConfigurationErrorFailsWhole(t *testing.T) {
	f := newFixture(t)
	threeStops(f)
	f.routes.errs[legAB] = apperr.New(503, "DEPENDENCY_UNAVAILABLE", "地图服务配额已用尽，请检查配额或稍后再试")
	v := f.viewer(t)
	_, err := f.svc.Routes(context.Background(), v, geo.Driving)
	code(t, err, 503, "DEPENDENCY_UNAVAILABLE")
	if f.routes.calls != 1 {
		t.Fatalf("配额耗尽应立即停止，不再请求后续路段: %d", f.routes.calls)
	}
}

func TestRoutesValidatesModeAndLimitsPerShareAndIP(t *testing.T) {
	f := newFixture(t)
	threeStops(f)
	v := f.viewer(t)
	_, err := f.svc.Routes(context.Background(), v, geo.Mode("flying"))
	code(t, err, 422, "VALIDATION_FAILED")
	for i := 0; i < 30; i++ {
		if _, err := f.svc.Routes(context.Background(), v, geo.Driving); err != nil {
			t.Fatalf("第 %d 次不应限流: %v", i+1, err)
		}
	}
	_, err = f.svc.Routes(context.Background(), v, geo.Driving)
	code(t, err, 429, "RATE_LIMITED")
	other := v
	other.ClientIP = "198.51.100.7"
	if _, err := f.svc.Routes(context.Background(), other, geo.Driving); err != nil {
		t.Fatalf("其他 IP 不受影响: %v", err)
	}
}

func TestRoutesWithoutRouteSourceIsUnavailable(t *testing.T) {
	f := newFixture(t)
	threeStops(f)
	v := f.viewer(t)
	svc := share.NewService(share.Deps{Store: f.store, Trips: f.trips, Itinerary: f.items, Limiter: f.limiter, Cursors: paging.InsecureCodec{}, Clock: f.clock, WebBaseURL: "https://trip.example.com"})
	_, err := svc.Routes(context.Background(), v, geo.Driving)
	code(t, err, 503, "DEPENDENCY_UNAVAILABLE")
}

func TestRoutesWithNoLocatedItemsReturnsEmpty(t *testing.T) {
	f := newFixture(t)
	f.items.items = []itinerary.Resource{item(f.tripID, "2026-10-01", 0, "酒店", 0, 0)}
	v := f.viewer(t)
	out, err := f.svc.Routes(context.Background(), v, geo.Driving)
	if err != nil || len(out.Legs) != 0 || out.UnlocatedItemCount != 1 || f.routes.calls != 0 {
		t.Fatalf("无坐标点不应调用上游: %v %+v calls=%d", err, out, f.routes.calls)
	}
}
