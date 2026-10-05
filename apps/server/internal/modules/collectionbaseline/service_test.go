package collectionbaseline

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/write"
)

type testReader struct {
	state           State
	entries         []Entry
	reads           int
	afterRead       func()
	overrideEntries func([]Entry) []Entry
}

func (r *testReader) Read(_ context.Context, _ actor.Actor, fn func(View) error) error {
	r.reads++
	err := fn(r)
	if r.afterRead != nil {
		r.afterRead()
	}
	return err
}
func (r *testReader) State(context.Context) (State, error) { return r.state, nil }
func (r *testReader) Revision(_ context.Context, epoch uuid.UUID, scope collectionguard.Scope) (write.ScopeRevision, error) {
	var source strings.Builder
	fmt.Fprintf(&source, "%s\n%s\n%s\n", epoch, scope.Kind, scope.ScopeID)
	for _, e := range r.entries {
		fmt.Fprintf(&source, "%s:%d\n", e.ID, e.Version)
	}
	return write.ScopeRevision{Kind: scope.Kind, ScopeID: scope.ScopeID,
		Revision: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(source.String())))}, nil
}
func (r *testReader) Entries(_ context.Context, _ collectionguard.Scope, after uuid.UUID, limit int) ([]Entry, error) {
	out := []Entry{}
	for _, e := range r.entries {
		if e.ID.String() > after.String() {
			out = append(out, e)
		}
		if len(out) == limit {
			break
		}
	}
	if r.overrideEntries != nil {
		out = r.overrideEntries(out)
	}
	return out, nil
}
func fixture(t *testing.T, kind string) (*Service, *testReader, actor.Actor, Query) {
	t.Helper()
	a := actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientWeb}
	scope := a.AccountID.String()
	if kind != "categories" {
		scope = uuid.NewString() + "/2028-02-29"
	}
	r := &testReader{state: State{Epoch: uuid.New(), AccountStatus: "active", SessionActive: true}}
	for i := 1; i <= 3; i++ {
		id := uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
		data := map[string]any{"id": id.String(), "version": "9007199254740993", "deleted_at": nil}
		if kind != "categories" {
			parts := strings.Split(scope, "/")
			data["trip_id"], data["scheduled_on"], data["recorded_on"] = parts[0], parts[1], parts[1]
		}
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		r.entries = append(r.entries, Entry{ID: id, Version: 9007199254740993, Data: raw})
	}
	keys, err := security.ParseKeyring("test=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(r, security.NewCursorCodec(keys), clock.NewFake(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)))
	return s, r, a, Query{Kind: kind, ScopeID: scope, Limit: 2}
}
func requireCode(t *testing.T, err error, code string, status int) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code || e.Status != status {
		t.Fatalf("want %s/%d, got %v", code, status, err)
	}
}
func TestPagesAllKindsAndEmpty(t *testing.T) {
	for _, kind := range []string{"categories", "itinerary_day", "photo_day"} {
		t.Run(kind, func(t *testing.T) {
			s, r, a, q := fixture(t, kind)
			first, err := s.List(context.Background(), a, q)
			if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
				t.Fatalf("first: %+v %v", first, err)
			}
			q.Cursor = *first.NextCursor
			s.clock.(*clock.Fake).Advance(time.Minute)
			last, err := s.List(context.Background(), a, q)
			if err != nil || len(last.Items) != 1 || last.NextCursor != nil || last.Revision != first.Revision || last.ExpiresAt != first.ExpiresAt {
				t.Fatalf("last: %+v %v", last, err)
			}
			if !strings.Contains(string(first.Items[0]), `"version":"9007199254740993"`) {
				t.Fatal("version lost precision")
			}
			q.Cursor = ""
			r.entries = nil
			empty, err := s.List(context.Background(), a, q)
			if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil {
				t.Fatalf("empty: %+v %v", empty, err)
			}
		})
	}
}
func TestCursorBindingAndSignature(t *testing.T) {
	s, r, a, q := fixture(t, "itinerary_day")
	first, err := s.List(context.Background(), a, q)
	if err != nil {
		t.Fatal(err)
	}
	original := *first.NextCursor
	for _, name := range []string{"signature", "account", "kind", "scope", "limit", "purpose", "version", "after", "epoch", "revision"} {
		t.Run(name, func(t *testing.T) {
			a2, q2 := a, q
			q2.Cursor = original
			var c cursor
			if err := s.codec.Decode(a.AccountID, cursorScope, original, &c); err != nil {
				t.Fatal(err)
			}
			signScope := cursorScope
			switch name {
			case "signature":
				parts := strings.Split(original, ".")
				parts[1] = "A" + parts[1][1:]
				if strings.Join(parts, ".") == original {
					parts[1] = "B" + parts[1][1:]
				}
				q2.Cursor = strings.Join(parts, ".")
			case "account":
				a2.AccountID = uuid.New()
			case "kind":
				q2.Kind = "photo_day"
			case "scope":
				q2.ScopeID = uuid.NewString() + "/2028-02-29"
			case "limit":
				q2.Limit = 1
			case "purpose":
				signScope = "sync/v2"
			case "version":
				c.Version = 2
			case "after":
				c.After = uuid.Nil
			case "epoch":
				c.Epoch = uuid.Nil
			case "revision":
				c.Revision = "sha256:bad"
			}
			if name == "purpose" || name == "version" || name == "after" || name == "epoch" || name == "revision" {
				q2.Cursor, err = s.codec.Encode(a.AccountID, signScope, c)
				if err != nil {
					t.Fatal(err)
				}
			}
			before := r.reads
			_, err := s.List(context.Background(), a2, q2)
			requireCode(t, err, "INVALID_CURSOR", 400)
			if r.reads != before {
				t.Fatal("invalid cursor reached repository")
			}
		})
	}
}
func TestChangesExpiryAndNoPartialResult(t *testing.T) {
	for _, name := range []string{"version", "insert", "delete", "epoch", "expired", "exit-expired", "inactive", "disabled", "missing-state", "corrupt-projection", "unordered"} {
		t.Run(name, func(t *testing.T) {
			s, r, a, q := fixture(t, "categories")
			first, err := s.List(context.Background(), a, q)
			if err != nil {
				t.Fatal(err)
			}
			q.Cursor = *first.NextCursor
			code, status := "COLLECTION_BASELINE_CHANGED", 412
			switch name {
			case "version":
				r.entries[0].Version++
			case "insert":
				r.entries = append(r.entries, Entry{ID: uuid.New(), Version: 1})
			case "delete":
				r.entries = r.entries[1:]
			case "epoch":
				r.state.Epoch = uuid.New()
			case "expired":
				s.clock.(*clock.Fake).Advance(lifetime)
			case "exit-expired":
				r.afterRead = func() { s.clock.(*clock.Fake).Advance(lifetime) }
			case "inactive":
				r.state.SessionActive = false
				code, status = "SESSION_EXPIRED", 401
			case "disabled":
				r.state.AccountStatus = "banned"
				code, status = "ACCOUNT_BANNED", 403
			case "missing-state":
				r.state.Epoch = uuid.Nil
				code, status = "DEPENDENCY_UNAVAILABLE", 503
			case "corrupt-projection":
				r.entries[2].Data = json.RawMessage(`{"id":"wrong"}`)
				code, status = "DEPENDENCY_UNAVAILABLE", 503
			case "unordered":
				q.Cursor = ""
				r.overrideEntries = func(e []Entry) []Entry { e[0], e[1] = e[1], e[0]; return e }
				code, status = "DEPENDENCY_UNAVAILABLE", 503
			}
			page, err := s.List(context.Background(), a, q)
			requireCode(t, err, code, status)
			if page.Items != nil || page.NextCursor != nil || page.Revision != "" {
				t.Fatal("failure leaked partial result")
			}
		})
	}
}
func TestAdmissionValidationAndDependencies(t *testing.T) {
	s, r, a, q := fixture(t, "categories")
	for _, kind := range []actor.ClientKind{actor.ClientWeb, actor.ClientAndroid, actor.ClientHarmony} {
		a.ClientKind = kind
		if _, err := s.List(context.Background(), a, q); err != nil {
			t.Fatal(err)
		}
	}
	a.ClientKind = "share"
	_, err := s.List(context.Background(), a, q)
	requireCode(t, err, "SESSION_EXPIRED", 401)
	a.ClientKind = actor.ClientWeb
	for _, bad := range []Query{{Kind: "members", ScopeID: a.AccountID.String()}, {Kind: "photo_day", ScopeID: a.AccountID.String() + "/2026-02-29"}, {Kind: "itinerary_day", ScopeID: a.AccountID.String() + "/0000-01-01"}} {
		_, err := s.List(context.Background(), a, bad)
		requireCode(t, err, "INVALID_REFERENCE", 422)
	}
	for _, limit := range []int{-1, 101} {
		bad := q
		bad.Limit = limit
		_, err := s.List(context.Background(), a, bad)
		requireCode(t, err, "VALIDATION_FAILED", 422)
	}
	bad := q
	bad.ScopeID = uuid.NewString()
	_, err = s.List(context.Background(), a, bad)
	requireCode(t, err, "RESOURCE_NOT_FOUND", 404)
	s.codec = nil
	_, err = s.List(context.Background(), a, q)
	requireCode(t, err, "DEPENDENCY_UNAVAILABLE", 503)
	if r.reads != 3 {
		t.Fatal("invalid input reached repository")
	}
}
