package money_test

import (
	"testing"

	"tripfolio/server/internal/foundation/money"
)

func TestDecimalParseAndFormat(t *testing.T) {
	cases := []struct {
		in         string
		minorUnits int
		want       string
	}{
		{"128.50", 2, "128.50"},
		{"128.5000", 2, "128.50"},
		{"0", 2, "0.00"},
		{"-3.5", 2, "-3.50"},
		{"1000", 0, "1000"},
		{"12.5", 0, "12.5"},
		{"12.1234", 2, "12.1234"},
	}
	for _, c := range cases {
		d, err := money.ParseDecimal(c.in)
		if err != nil {
			t.Fatalf("parse %q: %v", c.in, err)
		}
		if got := d.Format(c.minorUnits); got != c.want {
			t.Errorf("format %q with %d units: got %q, want %q", c.in, c.minorUnits, got, c.want)
		}
	}
	for _, bad := range []string{"", "-", "1.", ".5", "1e3", "1,000", "1.23456", "abc"} {
		if _, err := money.ParseDecimal(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestDecimalArithmetic(t *testing.T) {
	a := money.MustDecimal("100.00")
	b := money.MustDecimal("30.25")
	if got := a.Sub(b).Format(2); got != "69.75" {
		t.Fatalf("sub: %s", got)
	}
	if got := b.Sub(a).Format(2); got != "-69.75" {
		t.Fatalf("negative sub: %s", got)
	}
	if got := a.Add(b).Format(2); got != "130.25" {
		t.Fatalf("add: %s", got)
	}
	var zero money.Decimal
	if !zero.IsZero() || zero.Format(2) != "0.00" {
		t.Fatalf("zero value should format as 0.00, got %s", zero.Format(2))
	}
	if a.Cmp(b) != 1 || b.Cmp(a) != -1 || a.Cmp(a) != 0 {
		t.Fatal("cmp")
	}
	if b.Sub(a).Sign() != -1 {
		t.Fatal("sign")
	}
	if r := money.MustDecimal("25").Ratio(a); r != 0.25 {
		t.Fatalf("ratio: %v", r)
	}
	if r := a.Ratio(money.Zero()); r != 0 {
		t.Fatalf("ratio by zero: %v", r)
	}
}
