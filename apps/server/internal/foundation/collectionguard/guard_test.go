package collectionguard

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
)

var account = uuid.MustParse("11111111-1111-4111-8111-111111111111")
var trip = "22222222-2222-4222-8222-222222222222"
var revision = "sha256:" + strings.Repeat("a", 64)

func assertCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Status != status || e.Code != code {
		t.Fatalf("got %v; want %d %s", err, status, code)
	}
}

func TestCheckPolicies(t *testing.T) {
	s := Scope{"members", trip}
	g := Guard{s.Kind, s.ScopeID, revision}
	for _, tc := range []struct {
		name    string
		enabled bool
		guards  []Guard
		status  int
		code    string
		calls   int
	}{
		{"legacy", false, nil, 0, "", 0},
		{"missing", true, nil, 428, "COLLECTION_BASE_REQUIRED", 0},
		{"explicit empty", false, []Guard{}, 428, "COLLECTION_BASE_REQUIRED", 0},
		{"explicit legacy", false, []Guard{g}, 0, "", 1},
		{"enabled", true, []Guard{g}, 0, "", 1},
		{"duplicate", true, []Guard{g, g}, 422, "INVALID_REFERENCE", 0},
		{"stale", true, []Guard{{s.Kind, s.ScopeID, "sha256:" + strings.Repeat("b", 64)}}, 412, "COLLECTION_CONFLICT", 1},
		{"unrelated", true, []Guard{{"todo_order", trip, revision}}, 422, "INVALID_REFERENCE", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := Check(context.Background(), tc.enabled, account, tc.guards, []Scope{s}, func(_ context.Context, got Scope) (string, error) {
				calls++
				if got != s {
					t.Fatal(got)
				}
				return revision, nil
			})
			if tc.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				assertCode(t, err, tc.status, tc.code)
			}
			if calls != tc.calls {
				t.Fatalf("resolver calls %d, want %d", calls, tc.calls)
			}
		})
	}
}

func TestGuardSyntax(t *testing.T) {
	for _, kind := range []string{"categories", "members", "packing_order", "todo_order", "itinerary_day", "photo_day"} {
		scope := trip
		if strings.HasSuffix(kind, "_day") {
			scope += "/2028-02-29"
		}
		if err := Validate(Guard{kind, scope, revision}); err != nil {
			t.Fatal(kind, err)
		}
	}
	for _, g := range []Guard{
		{"unknown", trip, revision}, {"members", uuid.Nil.String(), revision}, {"members", trip + "/2026-10-05", revision},
		{"photo_day", trip + "/2026-02-29", revision}, {"itinerary_day", trip + "/0000-01-01", revision},
		{"members", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", revision}, {"members", trip, "sha256:" + strings.Repeat("A", 64)},
		{"members", trip, revision + "\n"}, {"members", trip, "sha256:a"},
	} {
		assertCode(t, Validate(g), 422, "INVALID_REFERENCE")
	}
}

func TestCheckOwnershipAndFailures(t *testing.T) {
	ctx := context.Background()
	s := Scope{"categories", trip}
	assertCode(t, Check(ctx, true, account, []Guard{{s.Kind, s.ScopeID, revision}}, []Scope{s}, nil), 422, "INVALID_REFERENCE")
	s = Scope{"members", trip}
	guards := []Guard{{s.Kind, s.ScopeID, revision}}
	denied := apperr.NotFound()
	err := Check(ctx, true, account, guards, []Scope{s}, func(context.Context, Scope) (string, error) { return "", denied })
	if !errors.Is(err, denied) {
		t.Fatal(err)
	}
	assertCode(t, Check(ctx, true, account, guards, []Scope{s}, nil), 500, "INTERNAL_ERROR")
	assertCode(t, Check(ctx, true, account, guards, []Scope{s}, func(context.Context, Scope) (string, error) { return "", nil }), 500, "INTERNAL_ERROR")
	assertCode(t, Check(ctx, true, account, guards, []Scope{s, s}, nil), 422, "INVALID_REFERENCE")
	assertCode(t, Check(ctx, false, uuid.Nil, nil, nil, nil), 422, "INVALID_REFERENCE")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := Check(canceled, true, account, guards, []Scope{s}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCheckAllDatesBeforeCallerWrites(t *testing.T) {
	scopes := []Scope{{"photo_day", trip + "/2026-10-05"}, {"photo_day", trip + "/2026-10-06"}}
	guards := []Guard{{scopes[0].Kind, scopes[0].ScopeID, revision}, {scopes[1].Kind, scopes[1].ScopeID, revision}}
	calls := 0
	err := Check(context.Background(), true, account, guards, scopes, func(_ context.Context, s Scope) (string, error) {
		calls++
		if s == scopes[1] {
			return "sha256:" + strings.Repeat("b", 64), nil
		}
		return revision, nil
	})
	assertCode(t, err, 412, "COLLECTION_CONFLICT")
	if calls != 2 {
		t.Fatal(calls)
	}
	assertCode(t, Check(context.Background(), true, account, guards[:1], scopes, nil), 428, "COLLECTION_BASE_REQUIRED")
}
