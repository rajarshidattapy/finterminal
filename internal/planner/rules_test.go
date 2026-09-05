package planner

import (
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.Local) }

func planQuery(t *testing.T, utterance string) *QueryPlan {
	t.Helper()
	p := NewRules(fixedNow).Plan(utterance, false)
	if p.Kind != KindQuery || p.Query == nil {
		t.Fatalf("%q: expected a query plan, got %s (%s)", utterance, p.Kind, p.Reason)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("%q: plan failed validation: %v", utterance, err)
	}
	return p.Query
}

// A bare "l" suffix used to swallow the "l" of "last", turning a ₹10,000
// threshold into ₹10,00,000 and silently returning nothing.
func TestAmountThresholdParsing(t *testing.T) {
	cases := []struct {
		utterance string
		wantPaise int64
	}{
		{"payments above 50,000 this month", 5_000_000},
		{"list payments over 10000 last 30 days", 1_000_000},
		{"payments above 2 lakh last 30 days", 20_000_000},
		{"payments over 5k today", 500_000},
		{"payments above ₹1,25,000 this week", 12_500_000},
		{"show me payments from yesterday", 0},
	}
	for _, c := range cases {
		if got := planQuery(t, c.utterance).Filters.MinAmountPaise; got != c.wantPaise {
			t.Errorf("%q: min_amount_paise = %d, want %d", c.utterance, got, c.wantPaise)
		}
	}
}

// "last 30 days" is a window; "last 30 payments" is a row limit.
func TestLimitIsNotATimeWindow(t *testing.T) {
	if got := planQuery(t, "list payments over 10000 last 30 days").Limit; got != 0 {
		t.Errorf("limit = %d, want 0 — 30 days is a window, not a row count", got)
	}
	if got := planQuery(t, "last 20 payments").Limit; got != 20 {
		t.Errorf("limit = %d, want 20", got)
	}
	q := planQuery(t, "list payments over 10000 last 30 days")
	if q.Window.From != "2026-08-07" || q.Window.To != "2026-09-05" {
		t.Errorf("window = %s..%s, want 2026-08-07..2026-09-05", q.Window.From, q.Window.To)
	}
}

// "top 5" means the five largest, not the five most recent.
func TestTopMeansLargest(t *testing.T) {
	if got := planQuery(t, "top 5 payments this week").SortBy; got != "amount" {
		t.Errorf("sort_by = %q, want %q", got, "amount")
	}
	if got := planQuery(t, "biggest payments this month").SortBy; got != "amount" {
		t.Errorf("sort_by = %q, want %q", got, "amount")
	}
	if got := planQuery(t, "show me payments from yesterday").SortBy; got != "" {
		t.Errorf("sort_by = %q, want the default (most recent)", got)
	}
}

func TestWindowPhrases(t *testing.T) {
	cases := []struct{ utterance, from, to string }{
		{"revenue today", "2026-09-05", "2026-09-05"},
		{"revenue yesterday", "2026-09-04", "2026-09-04"},
		{"revenue this week", "2026-08-31", "2026-09-05"},
		{"revenue this month", "2026-09-01", "2026-09-05"},
		{"revenue last month", "2026-08-01", "2026-08-31"},
		{"revenue for the last 14 days", "2026-08-23", "2026-09-05"},
		{"kitna kamaya iss hafte", "2026-08-31", "2026-09-05"},
	}
	for _, c := range cases {
		q := planQuery(t, c.utterance)
		if q.Window.From != c.from || q.Window.To != c.to {
			t.Errorf("%q: window = %s..%s, want %s..%s", c.utterance, q.Window.From, q.Window.To, c.from, c.to)
		}
	}
}
