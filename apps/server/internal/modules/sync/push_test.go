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
