package money

import "testing"

func TestFormat(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "₹0.00"},
		{1, "₹0.01"},
		{100, "₹1.00"},
		{125000, "₹1,250.00"},
		{1250000, "₹12,500.00"},
		{48230000, "₹4,82,300.00"},
		{100000000, "₹10,00,000.00"},
		{-10875000, "−₹1,08,750.00"},
	}
	for _, c := range cases {
		if got := Format(c.in); got != c.want {
			t.Errorf("Format(%d) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, s := range []string{"2500", "₹2,500.50", "1,00,000", "0.01"} {
		p, err := ParseRupees(s)
		if err != nil {
			t.Fatalf("ParseRupees(%q): %v", s, err)
		}
		if got, err2 := ParseRupees(Format(p)); err2 != nil || got != p {
			t.Errorf("round trip %q: %d -> %d (%v)", s, p, got, err2)
		}
	}
}

func TestPctZeroDen(t *testing.T) {
	if Pct(5, 0) != 0 {
		t.Fatal("expected 0 for zero denominator")
	}
}
