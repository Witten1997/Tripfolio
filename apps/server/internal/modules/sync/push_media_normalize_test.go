package sync

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMediaPayloadNormalization(t *testing.T) {
	trip, asset := uuid.New(), uuid.New()
	op := Operation{Type: "photo.create", TripID: &trip, Payload: json.RawMessage(`{"asset_id":"` + strings.ToUpper(asset.String()) + `","recorded_on":"2026-10-05"}`)}
	a, err := normalizeMediaPayload(op)
	if err != nil {
		t.Fatal(err)
	}
	op.Payload = json.RawMessage(`{"asset_id":"` + asset.String() + `","recorded_on":"2026-10-05","sort_order":0,"caption":"","place_name":"","address":"","taken_at_local":null,"latitude":null,"longitude":null}`)
	b, err := normalizeMediaPayload(op)
	if err != nil || string(a) != string(b) {
		t.Fatalf("defaults differ: %s %s %v", a, b, err)
	}
	op.Type = "asset.register"
	op.Payload = json.RawMessage(`{"scope":"trip","trip_id":"` + trip.String() + `","original_name":" photo.png ","expected_size":7,"declared_media_type":"IMAGE/PNG; charset=x"}`)
	a, err = normalizeMediaPayload(op)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(a, &got)
	if got["original_name"] != "photo.png" || got["declared_media_type"] != "image/png" || got["trip_id"] != trip.String() {
		t.Fatalf("canonical asset %s", a)
	}
	for _, payload := range []string{`{"taken_at_local":null}`, `{"caption":"x"}`} {
		op.Type = "photo.update"
		op.Payload = json.RawMessage(payload)
		got, err := normalizeMediaPayload(op)
		if err != nil || string(got) != payload {
			t.Fatalf("presence lost %s %v", got, err)
		}
	}
}

func TestMediaPayloadRejectsInvalidAndInternalFields(t *testing.T) {
	trip := uuid.New()
	tests := []struct{ kind, payload string }{
		{"photo.create", `{"asset_id":"` + uuid.NewString() + `"}`},
		{"photo.create", `{"recorded_on":"2026-10-05","asset":{}}`},
		{"photo.update", `{}`},
		{"photo.update", `{"sort_order":1}`},
		{"photo.update", `{"caption":null}`},
		{"photo.update", `{"taken_at_local_set":true}`},
		{"photo.update", `{"latitude_set":true}`},
		{"photo.update", `{"recorded_on":"2026-02-30"}`},
		{"photo.update", `{"latitude":91}`},
		{"photo.update", `{"asset_id":null}`},
		{"photo.update", `{"caption":"a","caption":"b"}`},
		{"photo.delete", `{"version":"1"}`},
		{"photo.reorder", `{"recorded_on":"2026-10-05","ordered_ids":null}`},
		{"photo.reorder", `{"recorded_on":"2026-10-05","ordered_ids":["` + trip.String() + `","` + trip.String() + `"]}`},
		{"asset.register", `{"scope":"avatar","trip_id":"` + trip.String() + `","original_name":"a","expected_size":1,"declared_media_type":"image/png"}`},
		{"asset.register", `{"scope":"trip","trip_id":"` + uuid.NewString() + `","original_name":"a","expected_size":1,"declared_media_type":"image/png"}`},
		{"asset.register", `{"scope":"trip","trip_id":"` + trip.String() + `","original_name":"a","expected_size":1.5,"declared_media_type":"image/png"}`},
		{"asset.register", `{"scope":"trip","trip_id":"` + trip.String() + `","original_name":"a","expected_size":1,"declared_media_type":"image/png","id":"` + uuid.NewString() + `"}`},
	}
	for _, tc := range tests {
		t.Run(tc.kind+tc.payload, func(t *testing.T) {
			if _, err := normalizeMediaPayload(Operation{Type: tc.kind, TripID: &trip, Payload: json.RawMessage(tc.payload)}); err == nil {
				t.Fatal("accepted invalid payload")
			}
		})
	}
}

func TestMediaCoordinatesUseStoredPrecision(t *testing.T) {
	var previous string
	for _, payload := range []string{`{"latitude":12.1234564,"longitude":-0.0000001}`, `{"latitude":12.123456,"longitude":0}`} {
		normalized, err := normalizeMediaPayload(Operation{Type: "photo.update", Payload: json.RawMessage(payload)})
		if err != nil {
			t.Fatal(err)
		}
		if previous != "" && previous != string(normalized) {
			t.Fatalf("different precision: %s %s", previous, normalized)
		}
		previous = string(normalized)
	}
}
