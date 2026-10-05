package write_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/write"
)

type fakeSource struct {
	fields   []string
	complete bool
	calls    int
}

func TestTripRelatedFieldsConflictBothDirections(t *testing.T) {
	for _, pair := range [][2]string{{"start_date", "end_date"}, {"end_date", "timezone"}, {"currency_code", "budget_amount"}, {"budget_amount", "currency_locked_at"}} {
		for _, reverse := range []bool{false, true} {
			a, b := pair[0], pair[1]
			if reverse {
				a, b = b, a
			}
			d, err := write.ResolvePatch(context.Background(), &fakeSource{fields: []string{a}, complete: true}, uuid.New(), "trip", uuid.New(), 1, 2, []string{b})
			if err != nil || d.Merge {
				t.Fatalf("related fields merged: %s/%s %+v %v", a, b, d, err)
			}
		}
	}
	d, err := write.ResolvePatch(context.Background(), &fakeSource{fields: []string{"start_date"}, complete: true}, uuid.New(), "trip", uuid.New(), 1, 2, []string{"notes"})
	if err != nil || !d.Merge {
		t.Fatalf("independent notes rejected: %+v %v", d, err)
	}
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
		if d.Merge || strings.Join(d.Conflicting, ",") != "amount,currency_code,notes,participant_member_ids,payer_member_id,personal_amount,split_count,split_mode,splits" {
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
func TestReservationFieldGroupsAreBidirectional(t *testing.T) {
	for _, pair := range [][2]string{{"kind", "origin"}, {"kind", "destination"}, {"kind", "transport_number"}, {"start_local", "end_local"}} {
		for i := range 2 {
			src := &fakeSource{fields: []string{pair[i]}, complete: true}
			d, err := write.ResolvePatch(context.Background(), src, uuid.New(), "reservation", uuid.New(), 1, 2, []string{pair[1-i]})
			if err != nil || d.Merge || len(d.Conflicting) == 0 {
				t.Fatalf("pair %v reversed=%d decision=%+v %v", pair, i, d, err)
			}
		}
	}
}

func TestLedgerFinancialFieldsConflictBothDirections(t *testing.T) {
	fields := []string{"amount", "currency_code", "payer_member_id", "split_mode", "participant_member_ids", "splits", "personal_amount", "split_count"}
	for _, a := range fields {
		for _, b := range fields {
			d, err := write.ResolvePatch(context.Background(), &fakeSource{fields: []string{a}, complete: true}, uuid.New(), "ledger_entry", uuid.New(), 1, 2, []string{b})
			if err != nil || d.Merge {
				t.Fatalf("merged %s/%s: %+v %v", a, b, d, err)
			}
		}
	}
	for _, pair := range [][2]string{{"kind", "refunded_entry_id"}, {"refunded_entry_id", "kind"}} {
		d, err := write.ResolvePatch(context.Background(), &fakeSource{fields: []string{pair[0]}, complete: true}, uuid.New(), "ledger_entry", uuid.New(), 1, 2, []string{pair[1]})
		if err != nil || d.Merge {
			t.Fatalf("refund group %+v %v", d, err)
		}
	}
}
