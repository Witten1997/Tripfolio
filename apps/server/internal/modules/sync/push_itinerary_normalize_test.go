package sync

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestItineraryPayloadCanonicalization(t *testing.T) {
	first := Operation{Type: "itinerary_item.create", Payload: json.RawMessage(`{"title":"  walk  ","kind":"other","scheduled_on":"2026-10-02","estimated_amount":"12.00","currency_code":"CNY","latitude":31.00,"longitude":121.0}`)}
	a, err := normalizeItineraryPayload(first)
	if err != nil {
		t.Fatal(err)
	}
	first.Payload = json.RawMessage(`{"title":"walk","kind":"other","scheduled_on":"2026-10-02","estimated_amount":"12","currency_code":"CNY","latitude":31,"longitude":121,"status":"pending","notes":"","planned_start_local":null}`)
	b, err := normalizeItineraryPayload(first)
	if err != nil || string(a) != string(b) {
		t.Fatalf("canonical payloads differ: %s %s %v", a, b, err)
	}
	for _, input := range []string{`{"notes":""}`, `{"planned_start_local":null}`} {
		raw, err := normalizeItineraryPayload(Operation{Type: "itinerary_item.update", Payload: json.RawMessage(input)})
		if err != nil || string(raw) != input {
			t.Fatalf("patch presence lost: %s %v", raw, err)
		}
	}
}

func TestItineraryPayloadRejectsInvalidFields(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"scheduled_on":"2026-10-03"}`, `{"sort_order":1}`, `{"latitude_set":true}`,
		`{"notes":null}`, `{"title":null}`, `{"footprint_excluded":"true"}`, `{"latitude":"31"}`,
		`{"planned_duration_minutes":1.5}`, `{"planned_duration_minutes":2147483648}`, `{"estimated_amount":1}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := normalizeItineraryPayload(Operation{Type: "itinerary_item.update", Payload: json.RawMessage(raw)}); err == nil {
				t.Fatal("accepted invalid patch")
			}
		})
	}
	if _, err := normalizeItineraryPayload(Operation{Type: "itinerary_item.delete", Payload: json.RawMessage(`{"title":"bad"}`)}); err == nil {
		t.Fatal("delete accepted fields")
	}
}

func TestItineraryReorderPayload(t *testing.T) {
	id := uuid.New().String()
	valid := `{"days":[{"date":"2026-10-02","ordered_ids":["` + id + `"]},{"date":"2026-10-03","ordered_ids":[]}]}`
	if _, err := normalizeItineraryPayload(Operation{Type: "itinerary_item.reorder", Payload: json.RawMessage(valid)}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"days":[]}`, `{"days":null}`, `{"days":[{"date":"2026-1-2","ordered_ids":[]}]}`,
		`{"days":[{"date":"2026-10-02","ordered_ids":null}]}`,
		`{"days":[{"date":"2026-10-02","ordered_ids":[],"base_version":1}]}`,
		`{"days":[{"date":"2026-10-02","ordered_ids":[]},{"date":"2026-10-02","ordered_ids":[]}]}`,
		`{"days":[{"date":"2026-10-02","ordered_ids":["` + id + `"]},{"date":"2026-10-03","ordered_ids":["` + id + `"]}]}`,
	} {
		if _, err := normalizeItineraryPayload(Operation{Type: "itinerary_item.reorder", Payload: json.RawMessage(raw)}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
