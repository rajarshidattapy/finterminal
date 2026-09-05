// Package money is the single conversion boundary between the API's smallest
// currency unit (paise) and anything a human or a model ever sees.
package money

import (
	"fmt"
	"strconv"
	"strings"
)

// Format renders paise as Indian-grouped rupees, e.g. 48230000 -> "₹4,82,300.00".
func Format(paise int64) string {
	neg := paise < 0
	if neg {
		paise = -paise
	}
	rupees := paise / 100
	frac := paise % 100
	s := group(strconv.FormatInt(rupees, 10))
	out := fmt.Sprintf("₹%s.%02d", s, frac)
	if neg {
		return "−" + out
	}
	return out
}

// FormatShort drops the paise fraction when it is zero.
func FormatShort(paise int64) string {
	if paise%100 == 0 {
		s := Format(paise)
		return strings.TrimSuffix(s, ".00")
	}
	return Format(paise)
}

// group applies the Indian digit grouping (last 3, then pairs).
func group(d string) string {
	if len(d) <= 3 {
		return d
	}
	head, tail := d[:len(d)-3], d[len(d)-3:]
	var parts []string
	for len(head) > 2 {
		parts = append([]string{head[len(head)-2:]}, parts...)
		head = head[:len(head)-2]
	}
	if head != "" {
		parts = append([]string{head}, parts...)
	}
	return strings.Join(parts, ",") + "," + tail
}

// ParseRupees converts a rupee string ("2500", "2,500.50", "₹2500") to paise.
func ParseRupees(s string) (int64, error) {
	s = strings.NewReplacer("₹", "", ",", "", " ", "", "rs.", "", "Rs.", "", "INR", "").Replace(strings.TrimSpace(s))
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad amount %q", s)
	}
	var f int64
	if frac != "" {
		if len(frac) > 2 {
			frac = frac[:2]
		}
		for len(frac) < 2 {
			frac += "0"
		}
		if f, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return 0, fmt.Errorf("bad amount %q", s)
		}
	}
	p := w*100 + f
	if neg {
		p = -p
	}
	return p, nil
}

// Pct returns a percentage with one decimal, guarding division by zero.
func Pct(num, den int64) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) * 100 / float64(den)
}
