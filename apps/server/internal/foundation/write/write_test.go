package write_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/write"
)

type fakeSource struct {
	fields   []string
	complete bool
	calls    int
}

func (f *fakeSource) ChangedFieldsSince(context.Context, uuid.UUID, string, uuid.UUID, int64, int64) ([]string, bool, error) {
	f.calls++
	return f.fields, f.complete, nil
}

func TestResolvePatch(t *testing.T) {
	ctx := context.Background()
	acc, id := uuid.New(), uuid.New()

	t.Run("same version merges without consulting the log", func(t *testing.T) {
		src := &fakeSource{}
		d, err := write.ResolvePatch(ctx, src, acc, "ledger_entry", id, 3, 3, []string{"notes"})
		if err != nil || !d.Merge || d.Merged || src.calls != 0 {
			t.Fatalf("decision = %+v, calls = %d, err = %v", d, src.calls, err)
		}
	})
	t.Run("disjoint fields merge with warning", func(t *testing.T) {
		src := &fakeSource{fields: []string{"amount", "category_id"}, complete: true}
		d, err := write.ResolvePatch(ctx, src, acc, "ledger_entry", id, 2, 4, []string{"notes"})
		if err != nil || !d.Merge || !d.Merged {
			t.Fatalf("decision = %+v, err = %v", d, err)
		}
	})
	t.Run("intersecting fields conflict and list them sorted", func(t *testing.T) {
		src := &fakeSource{fields: []string{"notes", "amount"}, complete: true}
		d, _ := write.ResolvePatch(ctx, src, acc, "ledger_entry", id, 2, 4, []string{"notes", "amount", "occurred_on"})
		if d.Merge || len(d.Conflicting) != 2 || d.Conflicting[0] != "amount" || d.Conflicting[1] != "notes" {
			t.Fatalf("decision = %+v", d)
		}
	})
	t.Run("expired log cannot decide", func(t *testing.T) {
		src := &fakeSource{fields: nil, complete: false}
		d, _ := write.ResolvePatch(ctx, src, acc, "ledger_entry", id, 2, 4, []string{"notes"})
		if d.Merge || d.Conflicting != nil {
			t.Fatalf("decision = %+v", d)
		}
	})
	t.Run("base ahead of current conflicts", func(t *testing.T) {
		src := &fakeSource{complete: true}
		d, _ := write.ResolvePatch(ctx, src, acc, "ledger_entry", id, 5, 4, []string{"notes"})
		if d.Merge || src.calls != 0 {
			t.Fatalf("decision = %+v", d)
		}
	})
}

func TestFingerprintIsDeterministicAndSensitive(t *testing.T) {
	type cmd struct {
		Amount string  `json:"amount"`
		Notes  *string `json:"notes"`
	}
	base := int64(3)
	a := write.Fingerprint("ledger_entry.update", "x", &base, cmd{Amount: "128.50"})
	b := write.Fingerprint("ledger_entry.update", "x", &base, cmd{Amount: "128.50"})
	if a != b {
		t.Fatal("same command must produce same fingerprint")
	}
	empty := ""
	c := write.Fingerprint("ledger_entry.update", "x", &base, cmd{Amount: "128.50", Notes: &empty})
	if a == c {
		t.Fatal("explicit empty string must differ from absent field")
	}
	d := write.Fingerprint("ledger_entry.update", "x", nil, cmd{Amount: "128.50"})
	if a == d {
		t.Fatal("base version must be part of the fingerprint")
	}
}
