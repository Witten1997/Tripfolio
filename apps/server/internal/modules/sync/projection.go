package sync

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/assets"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/modules/travel/album"
	"tripfolio/server/internal/modules/travel/document"
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/modules/travel/member"
	"tripfolio/server/internal/modules/travel/packing"
	"tripfolio/server/internal/modules/travel/reservation"
	"tripfolio/server/internal/modules/travel/todo"
	"tripfolio/server/internal/modules/travel/trip"
)

func unavailableProjection() error {
	return apperr.New(503, "DEPENDENCY_UNAVAILABLE", "历史资料不支持当前同步结构，不能推进同步进度")
}

func project(e Event) (Change, error) {
	var resource any
	switch e.EntityType {
	case "trip":
		resource = &trip.Resource{}
	case "trip_member":
		resource = &member.Resource{}
	case "expense_category":
		resource = &finance.CategoryResource{}
	case "ledger_entry":
		resource = &finance.LedgerResource{}
	case "itinerary_item":
		resource = &itinerary.Resource{}
	case "reservation":
		resource = &reservation.Resource{}
	case "document":
		resource = &document.Resource{}
	case "photo":
		resource = &album.Resource{}
	case "asset":
		resource = &assets.Resource{}
	case "packing_item":
		resource = &struct {
			packing.Resource
			SortOrder *int32 `json:"sort_order"`
		}{}
	case "todo":
		resource = &struct {
			todo.Resource
			SortOrder *int32 `json:"sort_order"`
		}{}
	default:
		return Change{}, unavailableProjection()
	}
	if e.Version <= 0 || e.EntityID == uuid.Nil || e.BatchID == uuid.Nil || (e.SchemaVersion != 1 && e.SchemaVersion != 2) {
		return Change{}, unavailableProjection()
	}
	if (e.EntityType == "expense_category") != (e.TripID == nil) || (e.TripID != nil && *e.TripID == uuid.Nil) {
		return Change{}, unavailableProjection()
	}
	if e.EntityType == "trip" && *e.TripID != e.EntityID {
		return Change{}, unavailableProjection()
	}
	c := Change{Seq: strconv.FormatInt(e.Seq, 10), BatchID: e.BatchID, BatchEndSeq: strconv.FormatInt(e.BatchEndSeq, 10),
		EntityType: e.EntityType, EntityID: e.EntityID, TripID: e.TripID, Version: strconv.FormatInt(e.Version, 10),
		Kind: e.Kind, SchemaVersion: ProtocolVersion, RequiresSnapshot: e.RequiresSnapshot}
	switch e.Kind {
	case "delete", "purge", "redacted":
		return c, nil
	case "upsert":
	default:
		return Change{}, unavailableProjection()
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(e.Data, &input) != nil || input == nil || json.Unmarshal(e.Data, resource) != nil || e.ChangedFields == nil {
		return Change{}, unavailableProjection()
	}
	var id, version string
	if json.Unmarshal(input["id"], &id) != nil || id != e.EntityID.String() ||
		json.Unmarshal(input["version"], &version) != nil || version != c.Version {
		return Change{}, unavailableProjection()
	}
	if e.EntityType != "expense_category" && e.EntityType != "trip" {
		var tripID string
		if json.Unmarshal(input["trip_id"], &tripID) != nil || tripID != e.TripID.String() {
			return Change{}, unavailableProjection()
		}
	}
	if e.EntityType == "asset" {
		var scope string
		if json.Unmarshal(input["scope"], &scope) != nil || scope != "trip" {
			return Change{}, unavailableProjection()
		}
	}
	if e.EntityType == "packing_item" || e.EntityType == "todo" {
		var order int32
		if bytes.Equal(input["sort_order"], []byte("null")) || json.Unmarshal(input["sort_order"], &order) != nil {
			return Change{}, unavailableProjection()
		}
	}
	// Only public resource types reach the wire, including their nested fields.
	// Missing historic fields cannot silently acquire today's defaults.
	encoded, err := json.Marshal(resource)
	if err != nil {
		return Change{}, unavailableProjection()
	}
	var allowed map[string]json.RawMessage
	if json.Unmarshal(encoded, &allowed) != nil {
		return Change{}, unavailableProjection()
	}
	for key := range allowed {
		if _, exists := input[key]; !exists {
			return Change{}, unavailableProjection()
		}
	}
	for _, field := range e.ChangedFields {
		if _, exists := allowed[field]; !exists {
			return Change{}, unavailableProjection()
		}
	}
	c.Data = encoded
	c.ChangedFields = append([]string{}, e.ChangedFields...)
	return c, nil
}
