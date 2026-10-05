package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/modules/account"
	"tripfolio/server/internal/modules/travel/share"
	"tripfolio/server/internal/transport/httpapi/middleware"
)

func TestParseCollectionGuards(t *testing.T) {
	revision := "sha256:" + strings.Repeat("a", 64)
	object := fmt.Sprintf(`{"kind":"members","scope_id":"22222222-2222-4222-8222-222222222222","revision":%q}`, revision)
	for _, tc := range []struct {
		name, raw string
		status    int
	}{
		{"empty array", "[]", 0}, {"valid", "[" + object + "]", 0},
		{"null", "null", 400}, {"object", object, 400}, {"empty", "", 400},
		{"trailing", "[] true", 400}, {"trailing comma", "[" + object + ",]", 400},
		{"duplicate field", "[" + strings.Replace(object, `"kind":"members"`, `"kind":"members","kind":"members"`, 1) + "]", 400},
		{"escaped duplicate", "[" + strings.Replace(object, `"kind":"members"`, `"kind":"members","\u006bind":"members"`, 1) + "]", 400},
		{"unknown field", "[" + strings.Replace(object, `"kind":"members"`, `"kind":"members","enabled":true`, 1) + "]", 400},
		{"wrong case", "[" + strings.Replace(object, `"kind"`, `"Kind"`, 1) + "]", 400},
		{"null value", "[" + strings.Replace(object, `"members"`, `null`, 1) + "]", 400},
		{"missing", "[{}]", 400}, {"nested", "[[" + object + "]]", 400},
		{"duplicate scope", "[" + object + "," + object + "]", 422},
		{"bad revision", "[" + strings.Replace(object, revision, "bad", 1) + "]", 422},
		{"boundary", "[]" + strings.Repeat(" ", collectionGuardsMaxBytes-2), 0},
		{"oversized", "[]" + strings.Repeat(" ", collectionGuardsMaxBytes-1), 400},
		{"invalid utf8", "[\"" + string([]byte{0xff}) + "\"]", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCollectionGuards(http.Header{collectionGuardsHeader: []string{tc.raw}})
			if tc.status == 0 {
				if err != nil || got == nil {
					t.Fatalf("%v %v", got, err)
				}
				return
			}
			e, ok := apperr.As(err)
			if !ok || e.Status != tc.status {
				t.Fatalf("got %v want %d", err, tc.status)
			}
		})
	}
	got, err := parseCollectionGuards(nil)
	if err != nil || got != nil {
		t.Fatalf("%v %v", got, err)
	}
	for _, h := range []http.Header{
		{collectionGuardsHeader: []string{"[]", "[]"}},
		{collectionGuardsHeader: []string{"[]"}, "x-collection-guards": []string{"[]"}},
		{collectionGuardsHeader: []string{}},
	} {
		if _, err := parseCollectionGuards(h); err == nil {
			t.Fatal("accepted duplicate/empty header")
		}
	}
	got, err = parseCollectionGuards(http.Header{"x-collection-guards": []string{"[" + object + "]"}})
	if err != nil || len(got) != 1 || got[0].Revision != revision {
		t.Fatalf("%v %v", got, err)
	}
}

func collectionGuardRoutes() [][2]string {
	return [][2]string{
		{http.MethodPut, "/api/v1/trips/22222222-2222-4222-8222-222222222222/members"},
		{http.MethodPost, "/api/v1/trips/22222222-2222-4222-8222-222222222222/ledger-entries"},
		{http.MethodPatch, "/api/v1/trips/22222222-2222-4222-8222-222222222222/ledger-entries/33333333-3333-4333-8333-333333333333"},
		{http.MethodPost, "/api/v1/trips/22222222-2222-4222-8222-222222222222/ledger-import"},
		{http.MethodPatch, "/api/v1/expense-categories/33333333-3333-4333-8333-333333333333"},
		{http.MethodPatch, "/api/v1/trips/22222222-2222-4222-8222-222222222222/photos/33333333-3333-4333-8333-333333333333"},
		{http.MethodPost, "/api/v1/trips/22222222-2222-4222-8222-222222222222/itinerary-items/reorder"},
	}
}

func collectionGuardJSON() string {
	return `[{"kind":"members","scope_id":"22222222-2222-4222-8222-222222222222","revision":"sha256:` + strings.Repeat("a", 64) + `"}]`
}

func TestCollectionGuardRequestsCapture(t *testing.T) {
	for _, route := range collectionGuardRoutes() {
		for _, kind := range []actor.ClientKind{actor.ClientWeb, actor.ClientAndroid, actor.ClientHarmony} {
			t.Run(route[0]+route[1]+string(kind), func(t *testing.T) {
				for _, raw := range []string{"", "[]", collectionGuardJSON()} {
					called := false
					request := httptest.NewRequest(route[0], route[1]+"?untouched=true", strings.NewReader("original-body"))
					a := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), AccountStatus: "active", ClientKind: kind}
					request = request.WithContext(actor.WithActor(request.Context(), a))
					request.Header.Set("Idempotency-Key", "original-operation")
					request.Header.Set("If-Match", `"7"`)
					if raw != "" {
						request.Header.Set(collectionGuardsHeader, raw)
					}
					next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						called = true
						guards := collectionguard.Request(r.Context())
						if (guards == nil) != (raw == "") {
							t.Fatalf("absent/empty distinction lost: %q %v", raw, guards)
						}
						if raw == collectionGuardJSON() {
							if len(guards) != 1 || guards[0].Revision != "sha256:"+strings.Repeat("a", 64) {
								t.Fatalf("guards: %v", guards)
							}
							guards[0].Revision = "modified"
							if collectionguard.Request(r.Context())[0].Revision == "modified" {
								t.Fatal("mutable request context")
							}
						}
						body, err := io.ReadAll(r.Body)
						if err != nil || string(body) != "original-body" || r.URL.RawQuery != "untouched=true" || r.Header.Get("Idempotency-Key") != "original-operation" || r.Header.Get("If-Match") != `"7"` {
							t.Fatal("middleware changed the original request")
						}
						gotActor, _ := actor.FromContext(r.Context())
						if gotActor != a {
							t.Fatal("actor changed")
						}
						original := [32]byte{1, 2, 3}
						if (collectionguard.Fingerprint(r.Context(), original) == original) != (raw == "") {
							t.Fatal("REST fingerprint did not distinguish explicit preconditions")
						}
						w.WriteHeader(204)
					})
					w := httptest.NewRecorder()
					collectionGuardRequests(next).ServeHTTP(w, request)
					if !called || w.Code != 204 {
						t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
					}
				}
			})
		}
	}
}

func TestCollectionGuardRequestsIsolation(t *testing.T) {
	routes := [][2]string{
		{http.MethodPost, "/api/v1/sync/push"},
		{http.MethodGet, "/api/v1/sync/snapshot"},
		{http.MethodGet, "/api/v1/public/trips/22222222-2222-4222-8222-222222222222"},
		{http.MethodGet, "/api/v1/metadata"},
		{http.MethodPost, "/api/v1/auth/logout"},
		{http.MethodGet, "/api/v1/trips/22222222-2222-4222-8222-222222222222/members"},
		{http.MethodPost, "/api/v1/trips/22222222-2222-4222-8222-222222222222/ledger-import-preview"},
		{http.MethodPost, "/api/v1/expense-categories"},
		{http.MethodPost, "/api/v1/trips/22222222-2222-4222-8222-222222222222/photos"},
		{http.MethodDelete, collectionGuardRoutes()[2][1]},
		{http.MethodPatch, "/api/v1/trips/22222222-2222-4222-8222-222222222222/itinerary-items/33333333-3333-4333-8333-333333333333"},
		{http.MethodPut, collectionGuardRoutes()[0][1] + "/"},
		{http.MethodPut, "/api/v1/trips//members"},
		{http.MethodPut, "/other/api/v1/trips/22222222-2222-4222-8222-222222222222/members"},
	}
	for _, route := range routes {
		t.Run(route[0]+route[1], func(t *testing.T) {
			for _, raw := range []string{"[]", collectionGuardJSON(), "malformed"} {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(route[0], route[1], nil)
				r.Header.Set(collectionGuardsHeader, raw)
				r = r.WithContext(actor.WithActor(r.Context(), actor.Actor{AccountID: uuid.New(), ClientKind: actor.ClientHarmony}))
				collectionGuardRequests(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("inapplicable header reached handler") })).ServeHTTP(w, r)
				if w.Code != 422 || !strings.Contains(w.Body.String(), "INVALID_REFERENCE") {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
			}
			// No header preserves the original route, but cannot inherit REST data.
			guards, _ := parseCollectionGuards(http.Header{collectionGuardsHeader: {collectionGuardJSON()}})
			ctx, _ := collectionguard.WithRequest(context.Background(), guards)
			r := httptest.NewRequest(route[0], route[1], nil).WithContext(ctx)
			called := false
			collectionGuardRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if collectionguard.Request(r.Context()) != nil {
					t.Fatal("inherited REST guards escaped isolation")
				}
			})).ServeHTTP(httptest.NewRecorder(), r)
			if !called || len(collectionguard.Request(ctx)) != 1 {
				t.Fatal("no-header compatibility or original context changed")
			}
		})
	}
}

type collectionAuthenticator struct{ calls int }

func (a *collectionAuthenticator) Authenticate(_ context.Context, token string) (actor.Actor, error) {
	a.calls++
	if token != "valid" {
		return actor.Actor{}, apperr.Unauthorized("SESSION_EXPIRED", "会话失效")
	}
	return actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb, AccountStatus: "active"}, nil
}

func TestCollectionGuardAuthenticationPrecedesParsing(t *testing.T) {
	auth := &collectionAuthenticator{}
	h := middleware.Auth(auth, middleware.AuthPolicy{})(collectionGuardRequests(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid request reached handler") })))
	for _, tc := range []struct {
		token, code string
		status      int
	}{
		{"", "AUTH_REQUIRED", 401}, {"expired", "SESSION_EXPIRED", 401}, {"valid", "MALFORMED_REQUEST", 400},
	} {
		r := httptest.NewRequest(http.MethodPut, collectionGuardRoutes()[0][1], nil)
		r.Header.Set(collectionGuardsHeader, "invalid-json")
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if auth.calls != 2 {
		t.Fatalf("auth calls=%d", auth.calls)
	}
}

func TestCollectionGuardRouterWiring(t *testing.T) {
	router := NewRouter(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	for _, route := range collectionGuardRoutes() {
		for _, raw := range []string{"invalid-json", collectionGuardJSON(), ""} {
			r := httptest.NewRequest(route[0], route[1], strings.NewReader("{}"))
			r = r.WithContext(actor.WithActor(r.Context(), actor.Actor{AccountID: uuid.New(), AccountStatus: "active"}))
			r.Header.Set("Content-Type", "application/json")
			if strings.HasSuffix(route[1], "/ledger-import") {
				r.URL.RawQuery = "preview_digest=" + strings.Repeat("a", 64)
				r.Header.Set("Content-Type", "application/octet-stream")
			}
			r.Header.Set("Idempotency-Key", uuid.NewString())
			r.Header.Set("If-Match", `"1"`)
			if raw != "" {
				r.Header.Set(collectionGuardsHeader, raw)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			want := 503 // Parsed and reached the deliberately unwired service.
			if strings.HasPrefix(route[1], "/api/v1/expense-categories/") {
				want = 422 // This existing handler rejects an empty patch before dispatch.
			}
			if raw == "invalid-json" {
				want = 400
			}
			if w.Code != want {
				t.Fatalf("%s %s guard=%q got %d: %s", route[0], route[1], raw, w.Code, w.Body.String())
			}
			if want == 422 && !strings.Contains(w.Body.String(), "EMPTY_PATCH") {
				t.Fatalf("expected original field validation, got %s", w.Body.String())
			}
		}
	}
	for _, path := range []string{"/api/v1/sync/push", "/api/v1/metadata"} {
		r := httptest.NewRequest(http.MethodPost, path, nil)
		r.Header.Set(collectionGuardsHeader, "[]")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 422 {
			t.Fatalf("REST header reached other route: %d %s", w.Code, w.Body.String())
		}
	}
	// Real router order: authentication must reject before even malformed guards.
	authRouter := NewRouter(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Sessions: &account.SessionService{}})
	r := httptest.NewRequest(http.MethodPut, collectionGuardRoutes()[0][1], nil)
	r.Header.Set(collectionGuardsHeader, "invalid-json")
	w := httptest.NewRecorder()
	authRouter.ServeHTTP(w, r)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "AUTH_REQUIRED") {
		t.Fatalf("guard parser ran before account authentication: %d %s", w.Code, w.Body.String())
	}
}

type collectionShareResolver struct{ calls int }

func (s *collectionShareResolver) Resolve(context.Context, string, string) (share.Viewer, error) {
	s.calls++
	return share.Viewer{}, nil
}

func TestCollectionGuardShareBoundary(t *testing.T) {
	resolver := &collectionShareResolver{}
	called := false
	h := middleware.Share(resolver, isSharePath)(collectionGuardRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, ok := actor.FromContext(r.Context()); ok {
			t.Fatal("share actor was not stripped")
		}
		if collectionguard.Request(r.Context()) != nil {
			t.Fatal("share inherited REST guard")
		}
	})))
	for _, withHeader := range []bool{false, true} {
		called = false
		r := httptest.NewRequest(http.MethodGet, "/api/v1/public/trips/fixture", nil)
		r = r.WithContext(actor.WithActor(r.Context(), actor.Actor{AccountID: uuid.New()}))
		r.Header.Set("Authorization", "Share fixture-token")
		if withHeader {
			r.Header.Set(collectionGuardsHeader, "[]")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if withHeader {
			if called || w.Code != 422 {
				t.Fatalf("guard injected through valid share: %d", w.Code)
			}
		} else if !called {
			t.Fatal("unguarded share compatibility lost")
		}
	}
	if resolver.calls != 2 {
		t.Fatal("share authorization must precede guards")
	}
}
