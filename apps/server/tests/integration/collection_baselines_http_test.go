package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	baseline "tripfolio/server/internal/modules/collectionbaseline"
	"tripfolio/server/internal/transport/httpapi"
)

func newCollectionHTTPFixture(t *testing.T) *collectionFixture {
	t.Helper()
	f := newCollectionFixture(t)
	s := f.services // BuildServices uses the real Store, configured paging codec and clock.
	if s.CollectionBaselines == nil {
		t.Fatal("bootstrap did not assemble collection baseline service")
	}
	f.server = httptest.NewServer(httpapi.NewRouter(httpapi.Deps{
		Logger: quietLogger(), CORSOrigins: []string{f.origin},
		Identity: s.Identity, Sessions: s.Sessions, Trips: s.Trips, Shares: s.Shares,
		CollectionBaselines: s.CollectionBaselines,
	}))
	t.Cleanup(f.server.Close)
	return f
}

func collectionHTTPPath(q baseline.Query) string {
	values := url.Values{"kind": {q.Kind}, "scope_id": {q.ScopeID}, "limit": {"2"}}
	if q.Cursor != "" {
		values.Set("cursor", q.Cursor)
	}
	return "/collection-baselines?" + values.Encode()
}
func collectionHTTPPage(t *testing.T, r apiResponse) baseline.Page {
	t.Helper()
	expectStatus(t, r, 200, "")
	if _, ok := r.Body["data"]; ok {
		t.Fatal("unexpected data wrapper")
	}
	var page baseline.Page
	if err := json.Unmarshal(r.Raw, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestCollectionBaselinesHTTPBootstrapPages(t *testing.T) {
	f := newCollectionHTTPFixture(t)
	spec, err := openapi3.NewLoader().LoadFromFile("../../../../packages/contracts/dist/openapi.v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	schema := spec.Components.Schemas["CollectionBaselinePage"].Value
	for _, kind := range []string{"categories", "itinerary_day", "photo_day"} {
		t.Run(kind, func(t *testing.T) {
			q := f.query(kind)
			emptyResponse := f.do(request{method: http.MethodGet, path: collectionHTTPPath(q), token: f.webToken})
			empty := collectionHTTPPage(t, emptyResponse)
			verifyCollectionDigest(t, []baseline.Page{empty}, 0)
			if empty.Items == nil {
				t.Fatal("empty items must be []")
			}
			if err := schema.VisitJSON(emptyResponse.Body); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				f.insert(kind)
			}
			var pages []baseline.Page
			for {
				response := f.do(request{method: http.MethodGet, path: collectionHTTPPath(q), token: f.webToken})
				page := collectionHTTPPage(t, response)
				if err := schema.VisitJSON(response.Body); err != nil {
					t.Fatal(err)
				}
				pages = append(pages, page)
				if page.NextCursor == nil {
					break
				}
				q.Cursor = *page.NextCursor
			}
			verifyCollectionDigest(t, pages, 3)
			// Native identity may read this account endpoint without enabling native sync.
			q.Cursor = ""
			native := collectionHTTPPage(t, f.do(request{method: http.MethodGet, path: collectionHTTPPath(q), token: f.token}))
			if native.Revision != pages[0].Revision {
				t.Fatal("native and web views differ")
			}
		})
	}
}

func TestCollectionBaselinesHTTPChangedAndInvalidQuery(t *testing.T) {
	f := newCollectionHTTPFixture(t)
	for _, kind := range []string{"categories", "itinerary_day", "photo_day"} {
		t.Run(kind, func(t *testing.T) {
			for range 3 {
				f.insert(kind)
			}
			q := f.query(kind)
			first := collectionHTTPPage(t, f.do(request{method: http.MethodGet, path: collectionHTTPPath(q), token: f.webToken}))
			if first.NextCursor == nil {
				t.Fatal("expected cursor")
			}
			q.Cursor = *first.NextCursor
			f.insert(kind)
			expectStatus(t, f.do(request{method: http.MethodGet, path: collectionHTTPPath(q), token: f.webToken}), 412, "COLLECTION_BASELINE_CHANGED")
		})
	}
	path := "/collection-baselines?kind=categories&scope_id=" + f.owner.String()
	page := collectionHTTPPage(t, f.do(request{method: http.MethodGet, path: path, token: f.webToken}))
	if len(page.Items) != 4 || page.NextCursor != nil {
		t.Fatal("default limit failed")
	}
	for _, tc := range []struct {
		suffix string
		status int
		code   string
	}{
		{"&limit=0", 422, "VALIDATION_FAILED"}, {"&limit=101", 422, "VALIDATION_FAILED"},
		{"&limit=abc", 400, "MALFORMED_REQUEST"}, {"&cursor=invalid", 400, "INVALID_CURSOR"},
		{"&limit=2&limit=3", 400, "MALFORMED_REQUEST"},
	} {
		expectStatus(t, f.do(request{method: http.MethodGet, path: path + tc.suffix, token: f.webToken}), tc.status, tc.code)
	}
	expectStatus(t, f.do(request{method: http.MethodGet, path: path, token: f.webToken, headers: map[string]string{"X-Collection-Guards": "[]"}}), 422, "INVALID_REFERENCE")
}

func TestCollectionBaselinesHTTPIdentityIsolation(t *testing.T) {
	f := newCollectionHTTPFixture(t)
	q := f.query("categories")
	path := collectionHTTPPath(q)
	expectStatus(t, f.do(request{method: http.MethodGet, path: path}), 401, "AUTH_REQUIRED")
	// Use a real enabled share, not merely a malformed token.
	share := shareToken(t, f.do(request{method: http.MethodPut, path: "/trips/" + f.trip.String() + "/share", token: f.webToken}))
	expectStatus(t, f.asGuest(share, path), 401, "AUTH_REQUIRED")
	foreign := f.registerWeb(uniqueEmail(), "correct horse battery")
	foreignToken := foreign.data()["access_token"].(string)
	for _, kind := range []string{"categories", "itinerary_day", "photo_day"} {
		expectStatus(t, f.do(request{method: http.MethodGet, path: collectionHTTPPath(f.query(kind)), token: foreignToken}), 404, "RESOURCE_NOT_FOUND")
	}
	q.Kind, q.ScopeID = "photo_day", uuid.NewString()+"/2026-02-30"
	expectStatus(t, f.do(request{method: http.MethodGet, path: collectionHTTPPath(q), token: f.webToken}), 422, "INVALID_REFERENCE")
	f.sql(`UPDATE account_sessions SET revoked_at=now() WHERE id=$1`, f.web.SessionID)
	expectStatus(t, f.do(request{method: http.MethodGet, path: path, token: f.webToken}), 401, "SESSION_EXPIRED")
}
