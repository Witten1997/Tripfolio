package share_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/travel/itinerary"
	"tripfolio/server/internal/modules/travel/share"
	"tripfolio/server/internal/modules/travel/trip"
)

func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(m))
}

// fullItem 把每个私密字段都填上可辨认的值，便于断言它们没有出现在公开模型里。
// ID 固定，避免随机 UUID 的十六进制碰巧包含被检查的子串。
func fullItem() itinerary.Resource {
	coord := 35.714800
	minutes := int32(30)
	amount := "1500.00"
	currency := "JPY"
	local := types.LocalDateTime("2026-10-02T09:00:00")
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	return itinerary.Resource{
		ID: uuid.MustParse("019ed632-51c0-7000-8000-00000000abcd"), TripID: uuid.MustParse("019ed632-51c0-7000-8000-00000000ef01"),
		Title: "浅草寺", Kind: itinerary.KindAttraction, ScheduledOn: "2026-10-02", SortOrder: 2,
		PlannedStartLocal: &local, PlannedEndLocal: &local, PlannedDurationMinutes: &minutes,
		PlaceName: "浅草寺", Address: "东京都台东区", Latitude: &coord, Longitude: &coord,
		EstimatedAmount: &amount, CurrencyCode: &currency, Notes: "私密备注", Status: itinerary.StatusCompleted,
		ActualStartLocal: &local, ActualEndLocal: &local, ActualNotes: "实际私密",
		Version: 3, CreatedAt: now, UpdatedAt: now,
	}
}

func TestPublicItineraryItemExposesExactlyTheWhitelist(t *testing.T) {
	want := []string{
		"address", "id", "kind", "latitude", "longitude", "place_name",
		"planned_duration_minutes", "planned_end_local", "planned_start_local", "scheduled_on", "sort_order", "title",
	}
	if got := jsonKeys(t, share.ToPublicItem(fullItem())); !slices.Equal(got, want) {
		t.Fatalf("公开行程字段 = %v，白名单 = %v；新增字段前先更新分享设计 4.3", got, want)
	}
}

func TestPublicTripExposesExactlyTheWhitelist(t *testing.T) {
	budget := "1000.00"
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	full := trip.Resource{
		ID: uuid.New(), Name: "东京", StartDate: "2026-10-01", EndDate: "2026-10-07", Destination: "东京",
		Notes: "旅行私密备注", Timezone: "Asia/Tokyo", CurrencyCode: "JPY", BudgetAmount: &budget,
		Version: 2, CreatedAt: now, UpdatedAt: now, ArchivedAt: &now,
	}
	want := []string{"destination", "end_date", "name", "start_date", "timezone"}
	if got := jsonKeys(t, share.ToPublicTrip(full)); !slices.Equal(got, want) {
		t.Fatalf("公开旅行字段 = %v，白名单 = %v；新增字段前先更新分享设计 4.3", got, want)
	}
}

func TestPublicModelsDoNotLeakPrivateValues(t *testing.T) {
	item, err := json.Marshal(share.ToPublicItem(fullItem()))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"私密备注", "实际私密", "1500", "JPY", "completed", "estimated", "notes", "status", "version", "trip_id", "ef01"} {
		if bytes.Contains(item, []byte(secret)) {
			t.Errorf("公开行程泄露 %q: %s", secret, item)
		}
	}
	if !bytes.Contains(item, []byte(`"planned_start_local":"2026-10-02T09:00:00"`)) || !bytes.Contains(item, []byte(`"latitude":35.7148`)) {
		t.Errorf("公开行程应保留计划时间与坐标: %s", item)
	}
}
