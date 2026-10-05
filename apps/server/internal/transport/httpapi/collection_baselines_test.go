package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/write"
	baseline "tripfolio/server/internal/modules/collectionbaseline"
)

type baselineHTTPReader struct {
	actor        actor.Actor
	limit, reads int
	count        int
	revision     string
}

func (r *baselineHTTPReader) Read(_ context.Context, a actor.Actor, fn func(baseline.View) error) error {
	r.actor = a
	r.reads++
	return fn(r)
}
func (*baselineHTTPReader) State(context.Context) (baseline.State, error) {
	return baseline.State{Epoch: uuid.MustParse("33333333-3333-4333-8333-333333333333"), AccountStatus: "active", SessionActive: true}, nil
}
func (r *baselineHTTPReader) Revision(_ context.Context, _ uuid.UUID, s collectionguard.Scope) (write.ScopeRevision, error) {
	return write.ScopeRevision{Kind: s.Kind, ScopeID: s.ScopeID, Revision: r.revision}, nil
}
func (r *baselineHTTPReader) Entries(_ context.Context, s collectionguard.Scope, after uuid.UUID, limit int) ([]baseline.Entry, error) {
	r.limit = limit
	entries := []baseline.Entry{}
	for i := 1; i <= r.count && len(entries) < limit; i++ {
		id := uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
		if id.String() <= after.String() {
			continue
		}
		data := map[string]any{"id": id.String(), "version": "9007199254740993", "deleted_at": nil}
		if s.Kind != "categories" {
			parts := strings.Split(s.ScopeID, "/")
			data["trip_id"], data["scheduled_on"], data["recorded_on"] = parts[0], parts[1], parts[1]
		}
		raw, _ := json.Marshal(data)
		entries = append(entries, baseline.Entry{ID: id, Version: 9007199254740993, Data: raw})
	}
	return entries, nil
}

func baselineHTTPRequest(router http.Handler, a actor.Actor, query string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/collection-baselines?"+query, nil)
	if a.AccountID != uuid.Nil {
		r = r.WithContext(actor.WithActor(r.Context(), a))
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}
func baselineHTTPFixture() (http.Handler, *baselineHTTPReader, *clock.Fake, actor.Actor) {
	a := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb}
	r := &baselineHTTPReader{revision: "sha256:" + strings.Repeat("a", 64)}
	clk := clock.NewFake(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
	h := NewRouter(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), CollectionBaselines: baseline.NewService(r, paging.InsecureCodec{}, clk)})
	return h, r, clk, a
}
func assertBaselineHTTPError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if w.Code != status || problem.Code != code {
		t.Fatalf("want %d %s, got %d %s", status, code, w.Code, w.Body)
	}
}

func TestCollectionBaselineHTTPPagesAndActor(t *testing.T) {
	for _, kind := range []string{"categories", "itinerary_day", "photo_day"} {
		t.Run(kind, func(t *testing.T) {
			h, reader, _, a := baselineHTTPFixture()
			scope := a.AccountID.String()
			if kind != "categories" {
				scope = uuid.NewString() + "/2028-02-29"
			}
			query := "kind=" + kind + "&scope_id=" + url.QueryEscape(scope)
			w := baselineHTTPRequest(h, a, query, nil)
			if w.Code != 200 || reader.limit != 51 || reader.actor != a {
				t.Fatalf("default/actor: %d %s", w.Code, w.Body)
			}
			var empty map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &empty); err != nil {
				t.Fatal(err)
			}
			if _, ok := empty["data"]; ok {
				t.Fatal("unexpected data wrapper")
			}
			if items, ok := empty["items"].([]any); !ok || len(items) != 0 || empty["next_cursor"] != nil {
				t.Fatal("invalid empty page")
			}
			reader.count = 3
			w = baselineHTTPRequest(h, a, query+"&limit=2", nil)
			var first, last baseline.Page
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &first) != nil || len(first.Items) != 2 || first.NextCursor == nil {
				t.Fatalf("first page %s", w.Body)
			}
			if !strings.Contains(string(first.Items[0]), `"version":"9007199254740993"`) {
				t.Fatal("version lost precision")
			}
			w = baselineHTTPRequest(h, a, query+"&limit=2&cursor="+url.QueryEscape(*first.NextCursor), nil)
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &last) != nil || len(last.Items) != 1 || last.NextCursor != nil || !first.ExpiresAt.Equal(last.ExpiresAt) || first.Revision != last.Revision {
				t.Fatalf("last page %s", w.Body)
			}
		})
	}
}

func TestCollectionBaselineHTTPRejectsParameters(t *testing.T) {
	h, reader, _, a := baselineHTTPFixture()
	base := "kind=categories&scope_id=" + a.AccountID.String()
	for _, tc := range []struct {
		query  string
		status int
		code   string
	}{
		{base + "&limit=0", 422, "VALIDATION_FAILED"}, {base + "&limit=-1", 422, "VALIDATION_FAILED"}, {base + "&limit=101", 422, "VALIDATION_FAILED"},
		{base + "&limit=1.5", 400, "MALFORMED_REQUEST"}, {base + "&limit=", 400, "MALFORMED_REQUEST"},
		{base + "&limit=1&limit=2", 400, "MALFORMED_REQUEST"}, {base + "&unknown=x", 400, "MALFORMED_REQUEST"},
		{base + "&cursor=", 400, "INVALID_CURSOR"}, {base + "&cursor=invalid", 400, "INVALID_CURSOR"},
		{base + "&cursor=%ZZ", 400, "MALFORMED_REQUEST"}, {base + "&kind=photo_day", 400, "MALFORMED_REQUEST"},
		{"kind=categories", 400, "MALFORMED_REQUEST"}, {"scope_id=" + a.AccountID.String(), 400, "MALFORMED_REQUEST"},
		{"kind=members&scope_id=" + a.AccountID.String(), 422, "INVALID_REFERENCE"},
		{"kind=categories&scope_id=bad", 422, "INVALID_REFERENCE"},
		{"kind=photo_day&scope_id=" + a.AccountID.String() + "/2026-02-30", 422, "INVALID_REFERENCE"},
		{"kind=categories&scope_id=" + uuid.NewString(), 404, "RESOURCE_NOT_FOUND"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			assertBaselineHTTPError(t, baselineHTTPRequest(h, a, tc.query, nil), tc.status, tc.code)
		})
	}
	if reader.reads != 0 {
		t.Fatal("invalid query reached reader")
	}
}

func TestCollectionBaselineHTTPChangedAndExpired(t *testing.T) {
	for _, expire := range []bool{false, true} {
		h, reader, clk, a := baselineHTTPFixture()
		reader.count = 2
		query := "kind=categories&scope_id=" + a.AccountID.String() + "&limit=1"
		w := baselineHTTPRequest(h, a, query, nil)
		var first baseline.Page
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &first) != nil || first.NextCursor == nil {
			t.Fatal(w.Body)
		}
		if expire {
			clk.Advance(10 * time.Minute)
		} else {
			reader.revision = "sha256:" + strings.Repeat("b", 64)
		}
		assertBaselineHTTPError(t, baselineHTTPRequest(h, a, query+"&cursor="+url.QueryEscape(*first.NextCursor), nil), 412, "COLLECTION_BASELINE_CHANGED")
	}
}

func TestCollectionBaselineHTTPIdentityGuardAndDependencies(t *testing.T) {
	h, reader, _, a := baselineHTTPFixture()
	query := "kind=categories&scope_id=" + a.AccountID.String()
	assertBaselineHTTPError(t, baselineHTTPRequest(h, actor.Actor{}, query, nil), 401, "AUTH_REQUIRED")
	assertBaselineHTTPError(t, baselineHTTPRequest(h, a, query, map[string]string{"X-Collection-Guards": "[]"}), 422, "INVALID_REFERENCE")
	if reader.reads != 0 {
		t.Fatal("rejected request reached reader")
	}
	for _, service := range []*baseline.Service{nil, baseline.NewService(nil, nil, nil)} {
		router := NewRouter(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), CollectionBaselines: service})
		assertBaselineHTTPError(t, baselineHTTPRequest(router, a, query, nil), 503, "DEPENDENCY_UNAVAILABLE")
	}
	for _, kind := range []actor.ClientKind{actor.ClientWeb, actor.ClientAndroid, actor.ClientHarmony} {
		a.ClientKind = kind
		w := baselineHTTPRequest(h, a, query, nil)
		if w.Code != 200 || reader.actor != a {
			t.Fatalf("client %s: %s", kind, w.Body)
		}
	}
}
