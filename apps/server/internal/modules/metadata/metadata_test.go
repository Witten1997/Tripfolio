package metadata_test

import (
	"testing"

	"tripfolio/server/internal/modules/metadata"
)

func TestCurrentHasTwentyOneCurrenciesWithDocumentedMinorUnits(t *testing.T) {
	m := metadata.Current()
	if len(m.Currencies) != 21 {
		t.Fatalf("len(Currencies) = %d, want 21", len(m.Currencies))
	}
	if m.DefaultCurrencyCode != "CNY" {
		t.Errorf("DefaultCurrencyCode = %q, want CNY", m.DefaultCurrencyCode)
	}
	zero := map[string]bool{"JPY": true, "KRW": true, "VND": true}
	three := map[string]bool{"KWD": true, "BHD": true}
	seen := map[string]bool{}
	for _, c := range m.Currencies {
		if seen[c.Code] {
			t.Errorf("币种 %s 重复", c.Code)
		}
		seen[c.Code] = true
		want := 2
		if zero[c.Code] {
			want = 0
		}
		if three[c.Code] {
			want = 3
		}
		if c.MinorUnits != want {
			t.Errorf("%s minor units = %d, want %d", c.Code, c.MinorUnits, want)
		}
	}
}

func TestMinorUnitsLookup(t *testing.T) {
	if u, ok := metadata.MinorUnits("JPY"); !ok || u != 0 {
		t.Errorf("MinorUnits(JPY) = %d, %v", u, ok)
	}
	if _, ok := metadata.MinorUnits("XXX"); ok {
		t.Error("MinorUnits(XXX) should be unsupported")
	}
}

func TestCurrentReturnsIndependentCopies(t *testing.T) {
	a := metadata.Current()
	a.Currencies[0].Code = "ZZZ"
	if b := metadata.Current(); b.Currencies[0].Code != "CNY" {
		t.Fatal("Current() must not share the currencies slice between calls")
	}
}
