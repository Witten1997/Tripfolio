package share_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/geo"
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/modules/travel/share"
	"tripfolio/server/internal/modules/travel/trip"
)

type tripKey struct{ account, trip uuid.UUID }

// fakeTrips 只按 (account, trip) 命中，跨账号读取自然落空。
type fakeTrips struct{ trips map[tripKey]trip.Resource }

func (f *fakeTrips) Get(_ context.Context, accountID, id uuid.UUID) (trip.Resource, bool, error) {
	t, ok := f.trips[tripKey{accountID, id}]
	return t, ok, nil
}

// fakeItinerary 记录被查询的账号与旅行，供断言访客读取用的是主人的 account_id。
type fakeItinerary struct {
	items       []itinerary.Resource
	lastAccount uuid.UUID
	lastTrip    uuid.UUID
}

func (f *fakeItinerary) List(_ context.Context, accountID, tripID uuid.UUID, q itinerary.ListQuery) ([]itinerary.Resource, error) {
	f.lastAccount, f.lastTrip = accountID, tripID
	var out []itinerary.Resource
	for _, it := range f.items {
		if it.TripID != tripID {
			continue
		}
		if q.After != nil && !(it.ScheduledOn.After(q.After.ScheduledOn) || (it.ScheduledOn == q.After.ScheduledOn && it.SortOrder > q.After.SortOrder)) {
			continue
		}
		out = append(out, it)
		if len(out) == q.Limit {
			break
		}
	}
	return out, nil
}

// fakeRoutes 按 legKey 返回预设错误，否则返回一条 1 公里 / 10 分钟的直连路线；记录调用次数与限流键。
type fakeRoutes struct {
	errs    map[string]error
	calls   int
	lastKey string
}

func legKey(o, d geo.Coordinate) string {
	return fmt.Sprintf("%.6f,%.6f>%.6f,%.6f", o.Latitude, o.Longitude, d.Latitude, d.Longitude)
}

func (f *fakeRoutes) RouteAs(_ context.Context, limitKey string, origin, destination geo.Coordinate, mode geo.Mode) (geo.Route, error) {
	f.calls++
	f.lastKey = limitKey
	if err, ok := f.errs[legKey(origin, destination)]; ok {
		return geo.Route{}, err
	}
	return geo.Route{Mode: mode, DistanceMeters: 1000, DurationSeconds: 600, Path: []geo.Coordinate{origin, destination}, Provider: "amap"}, nil
}

// RoutesAs 逐段复用 RouteAs：任一段失败即整批失败，与服务端批量算路的「全有或全无」一致，
// 让调用方走回逐段路径（既有用例的逐段降级断言因此保持成立）。
func (f *fakeRoutes) RoutesAs(ctx context.Context, limitKey string, points []geo.Coordinate, mode geo.Mode) ([]geo.Route, error) {
	if len(points) < 2 {
		return nil, errors.New("至少两个坐标")
	}
	routes := make([]geo.Route, 0, len(points)-1)
	for i := 0; i+1 < len(points); i++ {
		route, err := f.RouteAs(ctx, limitKey, points[i], points[i+1], mode)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, nil
}

// fakeLimiter 是不看时间的固定计数器。
type fakeLimiter struct{ counts map[string]int }

func (l *fakeLimiter) Allow(key string, limit int, _ time.Duration, _ time.Time) (bool, time.Duration) {
	l.counts[key]++
	if l.counts[key] > limit {
		return false, 30 * time.Second
	}
	return true, 0
}

type fixture struct {
	store   *share.MemoryStore
	trips   *fakeTrips
	items   *fakeItinerary
	routes  *fakeRoutes
	limiter *fakeLimiter
	clock   *clock.Fake
	svc     *share.Service
	actor   actor.Actor
	tripID  uuid.UUID
	issued  int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		store: share.NewMemoryStore(), trips: &fakeTrips{trips: map[tripKey]trip.Resource{}}, items: &fakeItinerary{},
		routes: &fakeRoutes{errs: map[string]error{}}, limiter: &fakeLimiter{counts: map[string]int{}},
		clock:  clock.NewFake(time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)),
		actor:  actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"},
		tripID: uuid.New(),
	}
	f.trips.trips[tripKey{f.actor.AccountID, f.tripID}] = trip.Resource{
		ID: f.tripID, Name: "东京", Destination: "日本东京", StartDate: "2026-10-01", EndDate: "2026-10-07", Timezone: "Asia/Tokyo",
		CurrencyCode: "JPY", Notes: "旅行私密备注", Version: 1,
	}
	f.svc = share.NewService(share.Deps{
		Store: f.store, Trips: f.trips, Itinerary: f.items, Routes: f.routes, Limiter: f.limiter,
		Cursors: paging.InsecureCodec{}, Clock: f.clock, WebBaseURL: "https://trip.example.com",
		// 确定性令牌 t000…0001、t000…0002 …，长度恒为 22 且符合令牌正则。
		Tokens: func() (string, error) {
			f.issued++
			return fmt.Sprintf("t%021d", f.issued), nil
		},
	})
	return f
}

func (f *fixture) enable(t *testing.T) share.Resource {
	t.Helper()
	r, err := f.svc.Enable(context.Background(), f.actor, f.tripID)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	return r
}

func (f *fixture) viewer(t *testing.T) share.Viewer {
	t.Helper()
	r := f.enable(t)
	v, err := f.svc.Resolve(context.Background(), r.Token, "203.0.113.9")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return v
}

func code(t *testing.T, err error, status int, want string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Status != status || e.Code != want {
		t.Fatalf("err = %v, want %d %s", err, status, want)
	}
}

func item(tripID uuid.UUID, on string, order int32, title string, lat, lng float64) itinerary.Resource {
	r := itinerary.Resource{ID: uuid.New(), TripID: tripID, Title: title, Kind: itinerary.KindAttraction, ScheduledOn: types.Date(on), SortOrder: order, Notes: "私密", Status: itinerary.StatusPending}
	if lat != 0 || lng != 0 {
		r.Latitude, r.Longitude = &lat, &lng
	}
	return r
}

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

func TestGenerateTokenIs22UrlSafeChars(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		tok, err := share.GenerateToken()
		if err != nil || !tokenRe.MatchString(tok) || seen[tok] {
			t.Fatalf("token #%d = %q, err = %v", i, tok, err)
		}
		seen[tok] = true
	}
}

func TestEnableIsIdempotentAndBuildsURL(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Get(context.Background(), f.actor, f.tripID)
	code(t, err, 404, "SHARE_NOT_FOUND")
	first := f.enable(t)
	if !tokenRe.MatchString(first.Token) || first.URL != "https://trip.example.com/s/"+first.Token || first.ViewCount != 0 || first.RotatedAt != nil {
		t.Fatalf("resource: %+v", first)
	}
	if again := f.enable(t); again.Token != first.Token || again.ID != first.ID {
		t.Fatalf("重复开启应返回同一条: %+v vs %+v", again, first)
	}
	got, err := f.svc.Get(context.Background(), f.actor, f.tripID)
	if err != nil || got.Token != first.Token {
		t.Fatalf("get: %v %+v", err, got)
	}
}

func TestOwnerOperationsRequireOwnTripOutsideRecycleBin(t *testing.T) {
	f := newFixture(t)
	stranger := actor.Actor{AccountID: uuid.New(), AccountStatus: "active"}
	_, err := f.svc.Enable(context.Background(), stranger, f.tripID)
	code(t, err, 404, "RESOURCE_NOT_FOUND")
	f.enable(t)
	deleted := f.clock.Now()
	tr := f.trips.trips[tripKey{f.actor.AccountID, f.tripID}]
	tr.DeletedAt = &deleted
	f.trips.trips[tripKey{f.actor.AccountID, f.tripID}] = tr
	_, err = f.svc.Get(context.Background(), f.actor, f.tripID)
	code(t, err, 410, "TRIP_DELETED")
	_, err = f.svc.Rotate(context.Background(), f.actor, f.tripID)
	code(t, err, 410, "TRIP_DELETED")
	code(t, f.svc.Disable(context.Background(), f.actor, f.tripID), 410, "TRIP_DELETED")
}

func TestRotateInvalidatesOldTokenImmediately(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Rotate(context.Background(), f.actor, f.tripID)
	code(t, err, 404, "SHARE_NOT_FOUND")
	old := f.enable(t)
	f.clock.Advance(time.Minute)
	rotated, err := f.svc.Rotate(context.Background(), f.actor, f.tripID)
	if err != nil || rotated.Token == old.Token || rotated.RotatedAt == nil || !rotated.RotatedAt.Equal(f.clock.Now()) || rotated.ID != old.ID {
		t.Fatalf("rotate: %v %+v", err, rotated)
	}
	_, err = f.svc.Resolve(context.Background(), old.Token, "203.0.113.9")
	code(t, err, 404, "SHARE_NOT_FOUND")
	if _, err := f.svc.Resolve(context.Background(), rotated.Token, "203.0.113.9"); err != nil {
		t.Fatalf("新令牌应可解析: %v", err)
	}
}

func TestDisableRemovesShareAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	r := f.enable(t)
	for i := 0; i < 2; i++ {
		if err := f.svc.Disable(context.Background(), f.actor, f.tripID); err != nil {
			t.Fatalf("disable #%d: %v", i+1, err)
		}
	}
	_, err := f.svc.Get(context.Background(), f.actor, f.tripID)
	code(t, err, 404, "SHARE_NOT_FOUND")
	_, err = f.svc.Resolve(context.Background(), r.Token, "203.0.113.9")
	code(t, err, 404, "SHARE_NOT_FOUND")
}

func TestResolveRejectsMalformedTokensWithoutStoreLookup(t *testing.T) {
	f := newFixture(t)
	for _, bad := range []string{"", "short", "t000000000000000000001x", "t0000000000000000000+1", "../../etc/passwd"} {
		_, err := f.svc.Resolve(context.Background(), bad, "203.0.113.9")
		code(t, err, 404, "SHARE_NOT_FOUND")
	}
	if f.store.Resolves != 0 {
		t.Fatalf("格式不符的令牌不应查库，实际查了 %d 次", f.store.Resolves)
	}
}

func TestResolveMapsOwnerAndTripState(t *testing.T) {
	f := newFixture(t)
	r := f.enable(t)
	v, err := f.svc.Resolve(context.Background(), r.Token, "203.0.113.9")
	if err != nil || v.AccountID != f.actor.AccountID || v.TripID != f.tripID || v.ShareID != r.ID || v.ClientIP != "203.0.113.9" {
		t.Fatalf("viewer: %v %+v", err, v)
	}
	deleted := f.clock.Now()
	f.store.SetTripDeleted(f.tripID, &deleted)
	_, err = f.svc.Resolve(context.Background(), r.Token, "203.0.113.9")
	code(t, err, 410, "TRIP_DELETED")
	f.store.SetTripDeleted(f.tripID, nil)
	f.store.SetOwnerStatus(f.actor.AccountID, "deleting")
	_, err = f.svc.Resolve(context.Background(), r.Token, "203.0.113.9")
	code(t, err, 403, "ACCOUNT_DELETING")
}

func TestResolveRateLimitsByClientIP(t *testing.T) {
	f := newFixture(t)
	r := f.enable(t)
	for i := 0; i < 120; i++ {
		if _, err := f.svc.Resolve(context.Background(), r.Token, "203.0.113.9"); err != nil {
			t.Fatalf("第 %d 次不应限流: %v", i+1, err)
		}
	}
	_, err := f.svc.Resolve(context.Background(), r.Token, "203.0.113.9")
	code(t, err, 429, "RATE_LIMITED")
	if _, err := f.svc.Resolve(context.Background(), r.Token, "198.51.100.7"); err != nil {
		t.Fatalf("其他 IP 不受影响: %v", err)
	}
}

func TestViewTripProjectsPublicFieldsAndCountsViews(t *testing.T) {
	f := newFixture(t)
	v := f.viewer(t)
	want := share.PublicTrip{Name: "东京", Destination: "日本东京", StartDate: "2026-10-01", EndDate: "2026-10-07", Timezone: "Asia/Tokyo"}
	pub, err := f.svc.ViewTrip(context.Background(), v)
	if err != nil || pub != want {
		t.Fatalf("view: %v %+v", err, pub)
	}
	if _, err := f.svc.ViewTrip(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.Get(context.Background(), f.actor, f.tripID)
	if got.ViewCount != 2 || got.LastViewedAt == nil {
		t.Fatalf("view_count: %+v", got)
	}
}

func TestListItemsUsesOwnerAccountAndPaginates(t *testing.T) {
	f := newFixture(t)
	f.items.items = []itinerary.Resource{
		item(f.tripID, "2026-10-01", 0, "机场", 35.5494, 139.7798),
		item(f.tripID, "2026-10-01", 1, "酒店", 0, 0),
		item(f.tripID, "2026-10-02", 0, "浅草寺", 35.7148, 139.7967),
	}
	v := f.viewer(t)
	page, err := f.svc.ListItems(context.Background(), v, share.ListFilters{Limit: 2})
	if err != nil || len(page.Items) != 2 || page.NextCursor == nil || page.Items[0].Title != "机场" || page.Items[1].Latitude != nil {
		t.Fatalf("page 1: %v %+v", err, page)
	}
	if f.items.lastAccount != f.actor.AccountID || f.items.lastTrip != f.tripID {
		t.Fatalf("访客读取应传主人的账号与旅行: %s %s", f.items.lastAccount, f.items.lastTrip)
	}
	rest, err := f.svc.ListItems(context.Background(), v, share.ListFilters{Limit: 2, Cursor: *page.NextCursor})
	if err != nil || len(rest.Items) != 1 || rest.NextCursor != nil || rest.Items[0].Title != "浅草寺" {
		t.Fatalf("page 2: %v %+v", err, rest)
	}
	_, err = f.svc.ListItems(context.Background(), v, share.ListFilters{Cursor: "not-a-cursor"})
	code(t, err, 400, "INVALID_CURSOR")
	_, err = f.svc.ListItems(context.Background(), v, share.ListFilters{Limit: 101})
	code(t, err, 422, "VALIDATION_FAILED")
}
