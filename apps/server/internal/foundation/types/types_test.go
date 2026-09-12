package types_test

import (
	"encoding/json"
	"testing"
	"time"

	"tripfolio/server/internal/foundation/types"
)

func TestParseDateStrict(t *testing.T) {
	for _, ok := range []string{"2026-09-12", "2024-02-29"} {
		if _, err := types.ParseDate(ok); err != nil {
			t.Errorf("%s should parse: %v", ok, err)
		}
	}
	for _, bad := range []string{"2026-9-12", "2026/09/12", "2023-02-29", "", "2026-09-12T00:00:00Z"} {
		if _, err := types.ParseDate(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestTodayInTimezone(t *testing.T) {
	now := time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)
	d, err := types.TodayIn(now, "Asia/Shanghai")
	if err != nil || d != "2026-09-13" {
		t.Fatalf("TodayIn = %s, %v", d, err)
	}
	if _, err := types.TodayIn(now, "Mars/Olympus"); err == nil {
		t.Fatal("unknown timezone should fail")
	}
}

func TestLocalDateTime(t *testing.T) {
	l, err := types.ParseLocalDateTime("2026-10-02T14:30:00")
	if err != nil {
		t.Fatal(err)
	}
	if l.Date() != "2026-10-02" {
		t.Fatalf("Date = %s", l.Date())
	}
	for _, bad := range []string{"2026-10-02T14:30:00Z", "2026-10-02 14:30:00", "2026-10-02T14:30"} {
		if _, err := types.ParseLocalDateTime(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestVersionJSON(t *testing.T) {
	b, _ := json.Marshal(struct {
		V types.Version `json:"v"`
	}{7})
	if string(b) != `{"v":"7"}` {
		t.Fatalf("marshal = %s", b)
	}
	var out struct {
		V types.Version `json:"v"`
	}
	if err := json.Unmarshal([]byte(`{"v":"12"}`), &out); err != nil || out.V != 12 {
		t.Fatalf("unmarshal: %v, %d", err, out.V)
	}
	for _, bad := range []string{`{"v":7}`, `{"v":"0"}`, `{"v":"07"}`, `{"v":"-1"}`, `{"v":"x"}`} {
		if err := json.Unmarshal([]byte(bad), &out); err == nil {
			t.Errorf("%s should be rejected", bad)
		}
	}
}
