package sync

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func pushTestInput() PushInput {
	id := uuid.New()
	payload, _ := json.Marshal(map[string]any{"name": "trip", "start_date": "2026-10-01", "end_date": "2026-10-03", "timezone": "Asia/Shanghai", "currency_code": "CNY", "self_member_id": uuid.New()})
	return PushInput{SyncEpoch: uuid.New(), ClientID: uuid.New(), Operations: []Operation{{OperationID: uuid.New(), Type: "trip.create", EntityType: "trip", EntityID: &id, TripID: &id, Guards: []GuardReference{}, DependsOn: []uuid.UUID{}, Payload: payload}}}
}

func TestPushStrictEnvelopeAndLimits(t *testing.T) {
	in := pushTestInput()
	raw, _ := json.Marshal(in)
	if _, err := DecodePush(raw); err != nil {
		t.Fatal(err)
	}
	bad := []string{strings.Replace(string(raw), `"client_id":`, `"extra":true,"client_id":`, 1), strings.Replace(string(raw), `"name":"trip"`, `"name":"first","name":"trip"`, 1), strings.Replace(string(raw), `"guards":[]`, `"guards":null`, 1), strings.Replace(string(raw), `"base":null,`, ``, 1)}
	for _, raw := range bad {
		if _, err := DecodePush([]byte(raw)); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
	if _, err := DecodePush([]byte(strings.Repeat(" ", MaxPushBytes+1))); err == nil {
		t.Fatal("oversize body accepted")
	}
	other := in.Operations[0]
	other.OperationID = uuid.New()
	other.DependsOn = []uuid.UUID{in.Operations[0].OperationID}
	in.Operations[0].DependsOn = []uuid.UUID{other.OperationID}
	in.Operations = append(in.Operations, other)
	if err := validateBatch(in); err == nil {
		t.Fatal("forward dependency accepted")
	}
}

func TestPushCanonicalFingerprintAndCommitPurpose(t *testing.T) {
	op := pushTestInput().Operations[0]
	a, err := prepare(op)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(op.Payload, &m)
	m["name"] = " trip "
	m["notes"] = ""
	m["destination"] = ""
	m["budget_amount"] = nil
	op.Payload, _ = json.Marshal(m)
	b, err := prepare(op)
	if err != nil || a.Fingerprint != b.Fingerprint {
		t.Fatal("equivalent create changed fingerprint")
	}
	s, v, actor := syncFixture(t)
	token, err := s.commitCursor(actor.AccountID, v.state.Epoch, op.OperationID, "9007199254740993")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.decode(actor.AccountID, token)
	expectCode(t, err, "INVALID_CURSOR")
}
func TestPushContentNullableAndCanonicalFields(t *testing.T) {
	for _, entity := range []string{"reservation", "document"} {
		op := pushTestInput().Operations[0]
		op.EntityType = entity
		op.Type = entity + ".create"
		asset := uuid.New()
		payload := map[string]any{"kind": "transport", "title": " title "}
		if entity == "document" {
			payload = map[string]any{"title": " title ", "asset_id": asset}
		}
		op.Payload, _ = json.Marshal(payload)
		first, err := prepare(op)
		if err != nil {
			t.Fatal(err)
		}
		payload["notes"] = ""
		if entity == "reservation" {
			payload["start_local"] = nil
			payload["booking_reference"] = ""
		} else {
			payload["reservation_id"] = nil
			payload["asset_id"] = strings.ToUpper(asset.String())
		}
		op.Payload, _ = json.Marshal(payload)
		second, err := prepare(op)
		if err != nil || first.Fingerprint != second.Fingerprint {
			t.Fatalf("canonical %s: %v", entity, err)
		}
		var canonical map[string]any
		_ = json.Unmarshal(second.Payload, &canonical)
		if canonical["title"] != " title " {
			t.Fatal("changed REST text semantics")
		}
		for _, bad := range []map[string]any{{"title": nil}, {"reservation_id_set": true}, {"start_local_set": true}, {"account_id": uuid.NewString()}} {
			op.Type = entity + ".update"
			op.Payload, _ = json.Marshal(bad)
			if _, err := prepare(op); err == nil {
				t.Fatalf("accepted internal/null fields %v", bad)
			}
		}
	}
}

func TestPushFinanceStrictFields(t *testing.T) {
	op := pushTestInput().Operations[0]
	op.EntityType = "ledger_entry"
	op.Type = "ledger_entry.create"
	memberID, categoryID := uuid.New(), uuid.New()
	payload := map[string]any{"kind": "expense", "amount": "100.00", "currency_code": "CNY", "category_id": categoryID, "occurred_on": "2026-10-01", "payer_member_id": memberID, "split_mode": "even", "participant_member_ids": []uuid.UUID{memberID}}
	op.Payload, _ = json.Marshal(payload)
	first, err := prepare(op)
	if err != nil {
		t.Fatal(err)
	}
	payload["amount"] = "100"
	payload["refunded_entry_id"] = nil
	payload["attachment_asset_ids"] = []uuid.UUID{}
	payload["notes"] = ""
	op.Payload, _ = json.Marshal(payload)
	second, err := prepare(op)
	if err != nil || first.Fingerprint != second.Fingerprint {
		t.Fatal("ledger canonical defaults", err)
	}
	for _, key := range []string{"payer_member_id", "split_mode", "participant_member_ids", "occurred_on", "currency_code"} {
		copy := map[string]any{}
		for k, v := range payload {
			if k != key {
				copy[k] = v
			}
		}
		op.Payload, _ = json.Marshal(copy)
		if _, err = prepare(op); err == nil {
			t.Fatal("accepted missing", key)
		}
	}
	op.Type = "ledger_entry.update"
	for _, bad := range []map[string]any{{"split_count": 2}, {"personal_amount": "1"}, {"splits": []any{}}, {"refunded_set": true}, {"amount": nil}, {"participant_member_ids": nil}, {"attachment_asset_ids": nil}, {"kind": "refund"}} {
		op.Payload, _ = json.Marshal(bad)
		if _, err = prepare(op); err == nil {
			t.Fatal("accepted bad ledger", bad)
		}
	}
	op.EntityType = "expense_category"
	op.Type = "expense_category.update"
	op.Payload = []byte(`{"icon":null}`)
	if _, err = prepare(op); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"sort_order":1}`, `{"icon_set":true}`, `{"name":null}`} {
		op.Payload = []byte(raw)
		if _, err = prepare(op); err == nil {
			t.Fatal("category bypass", raw)
		}
	}
	op.EntityType = "trip_member"
	op.Type = "trip_member.replace"
	for _, bad := range []map[string]any{{"members": nil}, {"members": []any{map[string]any{"id": memberID, "name": "A", "share_percent": "100", "is_self": true}}}, {"members": []any{map[string]any{"id": memberID, "name": nil, "share_percent": "100"}}}} {
		op.Payload, _ = json.Marshal(bad)
		if _, err = prepare(op); err == nil {
			t.Fatal("member field accepted", bad)
		}
	}
}

func TestPublicModuleCommandsAndFingerprint(t *testing.T) {
	trip, id, dep1, dep2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	rev, base := "revision", "1"
	cases := []struct{ kind, payload string }{
		{"itinerary_item.create", `{"title":"walk","kind":"other","scheduled_on":"2026-10-02"}`},
		{"itinerary_item.update", `{"notes":"note"}`}, {"itinerary_item.delete", `{}`},
		{"itinerary_item.reorder", `{"days":[{"date":"2026-10-02","ordered_ids":[]}]}`},
		{"photo.create", `{"asset_id":"` + id.String() + `","recorded_on":"2026-10-02"}`},
		{"photo.update", `{"caption":"note"}`}, {"photo.delete", `{}`},
		{"photo.reorder", `{"recorded_on":"2026-10-02","ordered_ids":[]}`},
		{"asset.register", `{"scope":"trip","trip_id":"` + trip.String() + `","original_name":"a.png","expected_size":1,"declared_media_type":"image/png"}`},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			op := Operation{OperationID: uuid.New(), Type: tc.kind, EntityType: strings.Split(tc.kind, ".")[0], EntityID: &id, TripID: &trip, Payload: json.RawMessage(tc.payload), DependsOn: []uuid.UUID{dep1, dep2}, Guards: []GuardReference{{Kind: "photo_day", ScopeID: trip.String() + "/2026-10-02", Revision: &rev}, {Kind: "itinerary_day", ScopeID: trip.String() + "/2026-10-02", Revision: &rev}}}
			a, err := prepare(op)
			if err != nil {
				t.Fatal(err)
			}
			op.Payload = a.Payload
			op.DependsOn = []uuid.UUID{dep2, dep1}
			op.Guards = []GuardReference{op.Guards[1], op.Guards[0]}
			b, err := prepare(op)
			if err != nil || a.Fingerprint != b.Fingerprint {
				t.Fatalf("unstable canonical fingerprint: %v", err)
			}
			for _, mutate := range []func(*Operation){func(o *Operation) { o.OperationID = uuid.New() }, func(o *Operation) { o.Base = &BaseReference{Version: &base} }, func(o *Operation) { o.DependsOn = []uuid.UUID{dep1} }, func(o *Operation) { o.Guards = nil }} {
				changed := op
				mutate(&changed)
				c, err := prepare(changed)
				if err != nil || c.Fingerprint == a.Fingerprint {
					t.Fatalf("facts omitted from fingerprint: %v", err)
				}
			}
		})
	}
}
