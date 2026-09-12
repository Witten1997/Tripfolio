package money_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tripfolio/server/internal/foundation/money"
)

type fixtureCase struct {
	ID         string `json:"id"`
	Input      string `json:"input"`
	MinorUnits int    `json:"minor_units"`
	Canonical  string `json:"canonical"`
	Error      string `json:"error"`
}

type fixtureFile struct {
	Cases []fixtureCase `json:"cases"`
}

var errorByCode = map[string]error{
	"INVALID_FORMAT": money.ErrInvalidFormat,
	"SCALE_EXCEEDED": money.ErrScaleExceeded,
	"NEGATIVE":       money.ErrNegative,
	"TOO_LARGE":      money.ErrTooLarge,
}

func loadFixture(t *testing.T) fixtureFile {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "..", "packages", "contracts", "fixtures", "money.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取共享样例 %s: %v", path, err)
	}
	var f fixtureFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("解析共享样例: %v", err)
	}
	if len(f.Cases) == 0 {
		t.Fatal("共享样例为空")
	}
	return f
}

func TestCanonicalizeAgainstSharedFixture(t *testing.T) {
	for _, c := range loadFixture(t).Cases {
		t.Run(c.ID, func(t *testing.T) {
			got, err := money.Canonicalize(c.Input, c.MinorUnits)
			if c.Error != "" {
				want, ok := errorByCode[c.Error]
				if !ok {
					t.Fatalf("样例使用了未知错误代码 %q", c.Error)
				}
				if !errors.Is(err, want) {
					t.Fatalf("Canonicalize(%q, %d) error = %v, want %v", c.Input, c.MinorUnits, err, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("Canonicalize(%q, %d) unexpected error: %v", c.Input, c.MinorUnits, err)
			}
			if got != c.Canonical {
				t.Fatalf("Canonicalize(%q, %d) = %q, want %q", c.Input, c.MinorUnits, got, c.Canonical)
			}
		})
	}
}

func TestCanonicalizeRejectsUnsupportedMinorUnits(t *testing.T) {
	if _, err := money.Canonicalize("1", 5); err == nil {
		t.Fatal("want error for minor units 5, got nil")
	}
}

func TestCompactAndFromStorage(t *testing.T) {
	compact := []struct{ in, want string }{
		{"0128.500", "128.5"}, {"5.00", "5"}, {"0.0", "0"}, {"000", "0"}, {"12.3400", "12.34"}, {"7", "7"},
	}
	for _, c := range compact {
		got, err := money.Compact(c.in)
		if err != nil || got != c.want {
			t.Errorf("Compact(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := money.Compact("1.23456"); !errors.Is(err, money.ErrScaleExceeded) {
		t.Errorf("Compact over 4 decimals: %v", err)
	}
	if _, err := money.Compact("-3"); !errors.Is(err, money.ErrNegative) {
		t.Errorf("Compact negative: %v", err)
	}
	if _, err := money.Compact("1e3"); !errors.Is(err, money.ErrInvalidFormat) {
		t.Errorf("Compact scientific: %v", err)
	}

	stored := []struct {
		in    string
		units int
		want  string
	}{
		{"128.5000", 2, "128.50"}, {"1000.0000", 0, "1000"}, {"0.0000", 2, "0.00"}, {"3.1230", 3, "3.123"},
	}
	for _, c := range stored {
		got, err := money.FromStorage(c.in, c.units)
		if err != nil || got != c.want {
			t.Errorf("FromStorage(%q, %d) = %q, %v; want %q", c.in, c.units, got, err, c.want)
		}
	}
	if _, err := money.FromStorage("1.2340", 2); !errors.Is(err, money.ErrScaleExceeded) {
		t.Errorf("FromStorage with non-zero excess digits: %v", err)
	}
}
