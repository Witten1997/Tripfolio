package geo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	geodata "tripfolio/server/internal/modules/geo"
)

type routeTransportFunc func(*http.Request) (*http.Response, error)

func (f routeTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func routeResponse() *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(routeJSON(`"cost":{"duration":"886"}`)))}
}

func TestRouteQueuePacesAllAccountsAndModes(t *testing.T) {
	const interval = 25 * time.Millisecond
	var mu sync.Mutex
	var starts []time.Time
	client, err := NewAmapClient(Config{Key: "test", RouteInterval: interval, HTTPClient: &http.Client{Transport: routeTransportFunc(func(*http.Request) (*http.Response, error) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		return routeResponse(), nil
	})}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 5)
	for i := range 5 {
		go func(index int) {
			point := to
			point.Latitude += float64(index) / 10000
			mode := geodata.Driving
			if index%2 == 0 {
				mode = geodata.Walking
			}
			_, err := client.CalculateRoute(context.Background(), string(rune('a'+index)), from, point, mode)
			results <- err
		}(i)
	}
	for range 5 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if len(starts) != 5 {
		t.Fatalf("actual upstream calls: %d", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if elapsed := starts[i].Sub(starts[i-1]); elapsed < interval-3*time.Millisecond {
			t.Fatalf("request burst: gap %s, expected >= %s", elapsed, interval)
		}
	}
}

func TestQueuedSameRouteReusesResultWithoutConsumingQuota(t *testing.T) {
	var calls atomic.Int32
	client, _ := NewAmapClient(Config{Key: "test", RouteInterval: time.Nanosecond, GlobalDailyLimit: 1, HTTPClient: &http.Client{Transport: routeTransportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return routeResponse(), nil
	})}}, nil)
	results := make(chan error, 8)
	for range 8 {
		go func() {
			route, err := client.CalculateRoute(context.Background(), "a", from, to, geodata.Walking)
			if err == nil && route.DistanceMeters != 1108 {
				err = errors.New("route not preserved")
			}
			if err == nil {
				route.Path[0].Latitude = 0
			}
			results <- err
		}()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("identical requests were not coalesced: %d", calls.Load())
	}
	got, err := client.CalculateRoute(context.Background(), "b", from, to, geodata.Walking)
	if err != nil || got.Path[0] != from {
		t.Fatal("cached path was mutated by a caller")
	}
}

func TestCancelledRouteQueueDoesNotUseQuotaOrReserveFutureSlots(t *testing.T) {
	var calls atomic.Int32
	client, _ := NewAmapClient(Config{Key: "test", RouteInterval: time.Nanosecond, GlobalDailyLimit: 1, HTTPClient: &http.Client{Transport: routeTransportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return routeResponse(), nil
	})}}, nil)
	client.routeGate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := client.CalculateRoute(ctx, "a", from, to, geodata.Walking)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queue was not cancelled: %v", err)
	}
	<-client.routeGate
	client.routeNext = time.Now().Add(time.Hour)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel2()
	_, err = client.CalculateRoute(ctx2, "a", from, to, geodata.Walking)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pacing wait was not cancelled: %v", err)
	}
	if calls.Load() != 0 || client.dailyCount != 0 {
		t.Fatal("cancelled requests consumed quota")
	}
	client.routeNext = time.Time{}
	if _, err := client.CalculateRoute(context.Background(), "a", from, to, geodata.Walking); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("queue was not released")
	}
}

func TestProviderErrorsDistinguishRecoveryAndPermanentFailures(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
		retry  string
	}{
		{"10014", 429, "1"}, {"10015", 429, "1"}, {"10019", 429, "1"}, {"10020", 429, "1"}, {"10021", 429, "1"},
		{"10004", 429, "60"}, {"10016", 503, "1"}, {"20003", 503, "1"},
		{"10001", 503, ""}, {"10009", 503, ""}, {"10012", 503, ""},
		{"10003", 503, ""}, {"10044", 503, ""}, {"40000", 503, ""},
		{"20801", 404, ""}, {"20802", 404, ""}, {"20803", 404, ""},
	} {
		t.Run(tc.code, func(t *testing.T) {
			client, _ := NewAmapClient(Config{Key: "private-key", HTTPClient: &http.Client{Transport: routeTransportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"status":"0","infocode":"` + tc.code + `","info":"private-key"}`))}, nil
			})}}, nil)
			_, err := geodata.NewService(client).Route(context.Background(), actor.Actor{}, from, to, geodata.Walking)
			problem, ok := apperr.As(err)
			if !ok || problem.Status != tc.status || problem.Headers["Retry-After"] != tc.retry {
				t.Fatalf("wrong recovery policy: %+v", problem)
			}
			if strings.Contains(err.Error(), "private-key") {
				t.Fatal("provider info leaked credentials")
			}
			if cause := errors.Unwrap(problem); cause != nil && strings.Contains(cause.Error(), "private-key") {
				t.Fatal("provider info leaked credentials in logs")
			}
		})
	}
}

func TestUpstreamRetryAfterIsPreserved(t *testing.T) {
	for _, status := range []int{429, 503} {
		client, _ := NewAmapClient(Config{Key: "test", HTTPClient: &http.Client{Transport: routeTransportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"7"}}, Body: io.NopCloser(strings.NewReader("busy"))}, nil
		})}}, nil)
		_, err := geodata.NewService(client).Route(context.Background(), actor.Actor{}, from, to, geodata.Walking)
		problem, ok := apperr.As(err)
		if !ok || problem.Status != status || problem.Headers["Retry-After"] != "7" {
			t.Fatalf("lost retry window: %+v", problem)
		}
	}
}
