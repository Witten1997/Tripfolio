package sync

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/types"
)

// normalizeItineraryPayload leaves envelope canonicalization and fingerprints to prepare.
func normalizeItineraryPayload(op Operation) (json.RawMessage, error) {
	invalid := func(field string) error {
		return apperr.Validation(apperr.Field(field, "INVALID", "行程字段格式无效"))
	}
	allowed := []string{"title", "kind", "planned_start_local", "planned_end_local", "planned_duration_minutes", "poi_id", "place_name", "address", "latitude", "longitude", "estimated_amount", "currency_code", "notes", "status", "actual_start_local", "actual_end_local", "actual_notes"}
	var required []string
	switch op.Type {
	case "itinerary_item.create":
		allowed = append(allowed, "scheduled_on")
		required = []string{"title", "kind", "scheduled_on"}
	case "itinerary_item.update":
		allowed = append(allowed, "footprint_excluded")
	case "itinerary_item.delete":
		allowed = nil
	case "itinerary_item.reorder":
		allowed, required = []string{"days"}, []string{"days"}
	default:
		return nil, invalid("type")
	}
	m, err := object(op.Payload, required, allowed)
	if err != nil {
		return nil, invalid("payload")
	}
	if op.Type == "itinerary_item.update" && len(m) == 0 {
		return nil, invalid("payload")
	}
	nullable := map[string]bool{"planned_start_local": true, "planned_end_local": true, "planned_duration_minutes": true, "latitude": true, "longitude": true, "estimated_amount": true, "actual_start_local": true, "actual_end_local": true}
	for k, raw := range m {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if !nullable[k] {
				return nil, invalid(k)
			}
			m[k] = json.RawMessage("null")
			continue
		}
		switch k {
		case "days":
			var days []json.RawMessage
			if json.Unmarshal(raw, &days) != nil || len(days) == 0 {
				return nil, invalid(k)
			}
			type day struct {
				Date string      `json:"date"`
				IDs  []uuid.UUID `json:"ordered_ids"`
			}
			out := make([]day, 0, len(days))
			seenDays, seenIDs := map[string]bool{}, map[uuid.UUID]bool{}
			for _, rawDay := range days {
				if _, err := object(rawDay, []string{"date", "ordered_ids"}, []string{"date", "ordered_ids"}); err != nil {
					return nil, invalid(k)
				}
				var d day
				if json.Unmarshal(rawDay, &d) != nil || d.IDs == nil {
					return nil, invalid(k)
				}
				if _, err := types.ParseDate(d.Date); err != nil || len(d.Date) != 10 || seenDays[d.Date] {
					return nil, invalid(k)
				}
				seenDays[d.Date] = true
				for _, id := range d.IDs {
					if id == uuid.Nil || seenIDs[id] {
						return nil, invalid(k)
					}
					seenIDs[id] = true
				}
				out = append(out, d)
			}
			m[k], _ = json.Marshal(out)
		case "footprint_excluded":
			var v bool
			if json.Unmarshal(raw, &v) != nil {
				return nil, invalid(k)
			}
			m[k], _ = json.Marshal(v)
		case "planned_duration_minutes":
			var v int32
			if json.Unmarshal(raw, &v) != nil {
				return nil, invalid(k)
			}
			m[k], _ = json.Marshal(v)
		case "latitude", "longitude":
			var v float64
			if json.Unmarshal(raw, &v) != nil {
				return nil, invalid(k)
			}
			m[k], _ = json.Marshal(v)
		default:
			var v string
			if json.Unmarshal(raw, &v) != nil || strings.ContainsRune(v, '\x00') {
				return nil, invalid(k)
			}
			if k == "title" {
				v = strings.TrimSpace(v)
			}
			if k == "estimated_amount" {
				v, err = money.Compact(v)
				if err != nil {
					return nil, invalid(k)
				}
			}
			if k == "scheduled_on" {
				if _, err := types.ParseDate(v); err != nil || len(v) != 10 {
					return nil, invalid(k)
				}
			}
			m[k], _ = json.Marshal(v)
		}
	}
	if op.Type == "itinerary_item.create" {
		for _, k := range []string{"poi_id", "place_name", "address", "notes", "actual_notes"} {
			if _, ok := m[k]; !ok {
				m[k] = json.RawMessage(`""`)
			}
		}
		if _, ok := m["status"]; !ok {
			m["status"] = json.RawMessage(`"pending"`)
		}
		for k := range nullable {
			if _, ok := m[k]; !ok {
				m[k] = json.RawMessage("null")
			}
		}
	}
	return json.Marshal(m)
}
