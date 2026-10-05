package sync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/finance"
)

type memoryView struct {
	state  State
	events []Event
	reads  int
}

func (v *memoryView) Read(ctx context.Context, _ actor.Actor, fn func(View) error) error {
	v.reads++
	return fn(v)
}
func (v *memoryView) State(context.Context) (State, error) { return v.state, nil }
func (v *memoryView) Changes(_ context.Context, after, upper int64, limit int) ([]Event, error) {
	var result []Event
	for _, e := range v.events {
		if e.Seq > after && e.Seq <= upper && len(result) < limit {
			result = append(result, e)
		}
	}
	return result, nil
}

func syncFixture(t *testing.T) (*Service, *memoryView, actor.Actor) {
	t.Helper()
	keys, err := security.ParseKeyring("test=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32))))
	if err != nil {
		t.Fatal(err)
	}
	v := &memoryView{state: State{Epoch: uuid.New(), AccountStatus: "active", SessionActive: true}}
	s := NewService(v, security.NewCursorCodec(keys), clock.NewFake(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)))
	return s, v, actor.Actor{AccountID: uuid.New(), SessionID: uuid.New(), ClientKind: actor.ClientHarmony, AccountStatus: "active"}
}

func categoryEvent(t *testing.T, seq, end, version int64, batch uuid.UUID) Event {
	t.Helper()
	id := uuid.New()
	raw, err := json.Marshal(finance.CategoryResource{ID: id, Name: "交通", Version: types.Version(version)})
	if err != nil {
		t.Fatal(err)
	}
	return Event{Seq: seq, BatchEndSeq: end, Version: version, BatchID: batch, EntityID: id, EntityType: "expense_category", Kind: "upsert", SchemaVersion: 1, Data: raw, ChangedFields: []string{"name"}}
}

func expectCode(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestSignedCursorIsolationAndProtocol(t *testing.T) {
	s, v, a := syncFixture(t)
	ctx := context.Background()
	token, _ := s.BaselineCursor(a.AccountID, v.state.Epoch, 0)
	_, err := s.Changes(ctx, a, "", token, 1)
	expectCode(t, err, "SYNC_PROTOCOL_UNSUPPORTED")
	if v.reads != 0 {
		t.Fatal("invalid protocol reached database")
	}
	other := a
	other.AccountID = uuid.New()
	_, err = s.Changes(ctx, other, "2", token, 1)
	expectCode(t, err, "INVALID_CURSOR")
	parts := strings.Split(token, ".")
	parts[1] = "A" + parts[1][1:]
	if strings.Join(parts, ".") == token {
		parts[1] = "B" + parts[1][1:]
	}
	_, err = s.Changes(ctx, a, "2", strings.Join(parts, "."), 1)
	expectCode(t, err, "INVALID_CURSOR")
	for _, scope := range []string{"sync:commit:v2", "sync:snapshot:v2", "trips"} {
		wrong, _ := s.codec.Encode(a.AccountID, scope, cursor{Protocol: 2, Epoch: v.state.Epoch, Purpose: "checkpoint", After: "0"})
		_, err = s.Changes(ctx, a, "2", wrong, 1)
		expectCode(t, err, "INVALID_CURSOR")
	}
	for _, after := range []string{"01", "+1", "1e3", "9223372036854775808"} {
		wrong, _ := s.codec.Encode(a.AccountID, changesScope, cursor{Protocol: 2, Epoch: v.state.Epoch, Purpose: "checkpoint", After: after})
		_, err = s.Changes(ctx, a, "2", wrong, 1)
		expectCode(t, err, "INVALID_CURSOR")
	}
	v.state.Epoch = uuid.New()
	_, err = s.Changes(ctx, a, "2", token, 1)
	expectCode(t, err, "SYNC_EPOCH_MISMATCH")
	a.ClientKind = actor.ClientWeb
	_, err = s.Status(ctx, a)
	expectCode(t, err, "NATIVE_SESSION_REQUIRED")
}

func TestFixedHighWaterAcrossBatchPagesAndLargeIntegers(t *testing.T) {
	s, v, a := syncFixture(t)
	ctx := context.Background()
	const base int64 = 9007199254740993
	batch := uuid.New()
	v.state.LastSeq = base + 2
	v.events = []Event{categoryEvent(t, base+1, base+2, base, batch), categoryEvent(t, base+2, base+2, base+1, batch)}
	token, _ := s.BaselineCursor(a.AccountID, v.state.Epoch, base)
	first, err := s.Changes(ctx, a, "2", token, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || first.Changes[0].Version != "9007199254740993" || first.Changes[0].BatchEndSeq != "9007199254740995" {
		t.Fatalf("first page: %+v", first)
	}
	v.state.LastSeq++
	v.events = append(v.events, categoryEvent(t, base+3, base+3, 1, uuid.New()))
	second, err := s.Changes(ctx, a, "2", first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || len(second.Changes) != 1 || second.Changes[0].BatchID != batch {
		t.Fatalf("unstable H: %+v", second)
	}
	third, err := s.Changes(ctx, a, "2", second.NextCursor, 1)
	if err != nil || third.HasMore || len(third.Changes) != 1 || third.Changes[0].Seq != "9007199254740996" {
		t.Fatalf("new round: %+v %v", third, err)
	}
	empty, err := s.Changes(ctx, a, "2", third.NextCursor, 1)
	if err != nil || empty.HasMore || len(empty.Changes) != 0 || empty.NextCursor == "" {
		t.Fatalf("empty checkpoint: %+v %v", empty, err)
	}
}

func TestRetentionHolesAndInvalidPayloadDoNotReturnProgress(t *testing.T) {
	s, v, a := syncFixture(t)
	ctx := context.Background()
	v.state.LastSeq = 2
	v.state.RetainedAfterSeq = 1
	token, _ := s.BaselineCursor(a.AccountID, v.state.Epoch, 0)
	p, err := s.Changes(ctx, a, "2", token, 1)
	expectCode(t, err, "CURSOR_EXPIRED")
	if p.NextCursor != "" {
		t.Fatal("expired response advanced")
	}
	token, _ = s.BaselineCursor(a.AccountID, v.state.Epoch, 1)
	v.events = []Event{categoryEvent(t, 2, 2, 1, uuid.New())}
	if _, err = s.Changes(ctx, a, "2", token, 1); err != nil {
		t.Fatal("retained equality must work", err)
	}
	v.events = nil
	p, err = s.Changes(ctx, a, "2", token, 1)
	expectCode(t, err, "CURSOR_EXPIRED")
	if p.NextCursor != "" {
		t.Fatal("hole advanced")
	}
	v.events = []Event{categoryEvent(t, 2, 2, 1, uuid.New())}
	v.events[0].SchemaVersion = 3
	p, err = s.Changes(ctx, a, "2", token, 1)
	expectCode(t, err, "DEPENDENCY_UNAVAILABLE")
	if p.NextCursor != "" {
		t.Fatal("unknown schema advanced")
	}
	v.state.SessionActive = false
	_, err = s.Status(ctx, a)
	expectCode(t, err, "SESSION_EXPIRED")
}

func TestProjectionDropsPrivateFieldsAndRejectsMissingHistory(t *testing.T) {
	e := categoryEvent(t, 1, 1, 1, uuid.New())
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(e.Data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["storage_key"] = json.RawMessage(`"private-key"`)
	e.Data, _ = json.Marshal(fields)
	p, err := project(e)
	if err != nil || strings.Contains(string(p.Data), "private-key") {
		t.Fatalf("projection leak: %s %v", p.Data, err)
	}
	delete(fields, "sort_order")
	e.Data, _ = json.Marshal(fields)
	_, err = project(e)
	expectCode(t, err, "DEPENDENCY_UNAVAILABLE")
	e.Kind = "purge"
	p, err = project(e)
	if err != nil || p.Data != nil || p.ChangedFields != nil {
		t.Fatalf("purge leaked content: %+v %v", p, err)
	}
}
