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
