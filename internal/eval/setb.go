package eval

import (
	"fmt"
	"strings"
	"time"

	"github.com/rajarshidattapy/finterminal/internal/analytics"
	"github.com/rajarshidattapy/finterminal/internal/money"
	"github.com/rajarshidattapy/finterminal/internal/narrator"
	"github.com/rajarshidattapy/finterminal/internal/store"
)

// RunB is the regression test for Principle 1. Every figure the engine reports
// is recomputed here by an independent path — a plain loop over the raw rows,
// no SQL aggregation — and the narration is checked for numerals that exist in
// neither. A model that starts inventing figures fails the build.
func RunB(s *store.Store, now time.Time) *Report {
	t0 := time.Now()
	r := &Report{Set: "B · numeric fidelity"}
	e := analytics.New(s)

	week := analytics.Window{From: trunc(now).AddDate(0, 0, -6), To: trunc(now).AddDate(0, 0, 1)}
	prev := week.Previous()

	// --- independent recomputation, straight from the rows ---
	cur := rawTotals(s, week)
	pre := rawTotals(s, prev)

	fs, err := e.RevenueBreakdown(week, true)
	if err != nil {
		r.add("revenue_breakdown runs", false, "no error", err.Error(), "")
		r.Elapsed = time.Since(t0)
		return r
	}

	eq := func(name, want, got string) { r.add(name, want == got, want, got, "") }

	eq("current revenue equals row-by-row sum", money.FormatShort(cur.captured), fs.Get("current_revenue"))
	eq("current captured count", fmt.Sprint(cur.capturedN), fs.Get("current_count"))
	eq("previous revenue equals row-by-row sum", money.FormatShort(pre.captured), fs.Get("previous_revenue"))
	eq("previous captured count", fmt.Sprint(pre.capturedN), fs.Get("previous_count"))
	eq("delta is exactly current minus previous",
		money.FormatShort(cur.captured-pre.captured), fs.Get("delta_revenue"))
	eq("current success rate", fmt.Sprintf("%.1f%%", money.Pct(int64(cur.capturedN), int64(cur.total))),
		fs.Get("current_success_rate"))
	eq("previous success rate", fmt.Sprintf("%.1f%%", money.Pct(int64(pre.capturedN), int64(pre.total))),
		fs.Get("previous_success_rate"))
	eq("current failed count", fmt.Sprint(cur.failed), fs.Get("current_failed"))
	eq("previous failed count", fmt.Sprint(pre.failed), fs.Get("previous_failed"))

	// Method breakdown must reconcile to the headline figure, to the paise.
	var methodSum int64
	var methodCount int
	for _, row := range fs.Tables["by_method"] {
		methodSum += row.Amount
		methodCount += row.Count
	}
	eq("method breakdown reconciles to total revenue",
		money.FormatShort(cur.captured), money.FormatShort(methodSum))
	eq("method breakdown reconciles to payment count", fmt.Sprint(cur.capturedN), fmt.Sprint(methodCount))

	// Failure analysis.
	fa, err := e.FailureAnalysis(week, "")
	if err != nil {
		r.add("failure_analysis runs", false, "no error", err.Error(), "")
	} else {
		eq("failed total matches raw count", fmt.Sprint(cur.failed), fa.Get("failed_total"))
		sum := 0
		for _, row := range fa.Tables["patterns"] {
			sum += row.Count
		}
		eq("failure patterns sum to failed total", fmt.Sprint(cur.failed), fmt.Sprint(sum))
	}

	// Listing: the printed total is the sum of the printed rows, and the filter holds.
	const minP = 5_000_00
	lp, err := e.ListPayments(week, "", "", minP, 500)
	if err != nil {
		r.add("list_payments runs", false, "no error", err.Error(), "")
	} else {
		var sum int64
		below := 0
		for _, row := range lp.Tables["payments"] {
			sum += row.Amount
			if row.Amount < minP {
				below++
			}
		}
		eq("listed total equals sum of listed rows", money.FormatShort(sum), lp.Get("result_total"))
		eq("min-amount filter holds on every row", "0 below", fmt.Sprintf("%d below", below))
	}

	// Invoices: outstanding is amount minus paid, summed.
	inv, err := e.UnpaidInvoices(500)
	if err != nil {
		r.add("unpaid_invoices runs", false, "no error", err.Error(), "")
	} else {
		var sum int64
		for _, row := range inv.Tables["invoices"] {
			sum += row.Amount
		}
		eq("outstanding equals sum of balances", money.FormatShort(sum), inv.Get("outstanding_total"))
	}

	// Settlements.
	st, err := e.SettlementSummary(analytics.Window{From: trunc(now).AddDate(0, 0, -30), To: trunc(now).AddDate(0, 0, 1)})
	if err != nil {
		r.add("settlement_summary runs", false, "no error", err.Error(), "")
	} else {
		var processed int64
		rows, _ := s.DB.Query(`SELECT amount_paise FROM settlements WHERE status='processed' AND created_at >= ? AND created_at < ?`,
			trunc(now).AddDate(0, 0, -30).Unix(), trunc(now).AddDate(0, 0, 1).Unix())
		if rows != nil {
			for rows.Next() {
				var a int64
				_ = rows.Scan(&a)
				processed += a
			}
			rows.Close()
		}
		eq("settled total equals row-by-row sum", money.FormatShort(processed), st.Get("settled_total"))
	}

	// Duplicate pairs must genuinely be same-amount and inside the window.
	dup, err := e.DuplicateDetection(analytics.Window{From: trunc(now).AddDate(0, 0, -90), To: trunc(now).AddDate(0, 0, 1)}, 30*time.Minute)
	if err != nil {
		r.add("duplicate_detection runs", false, "no error", err.Error(), "")
	} else {
		bad := 0
		for _, row := range dup.Tables["pairs"] {
			if !strings.Contains(row.Extra, "apart") {
				bad++
			}
		}
		eq("every duplicate pair carries its gap", "0 malformed", fmt.Sprintf("%d malformed", bad))
	}

	// --- the guard itself ---
	tmpl := narrator.Template(fs)
	if bad, ok := narrator.Guard(fs, tmpl); ok {
		r.add("template narration contains no numeral outside the fact set", true, "clean", "clean", "")
	} else {
		r.add("template narration contains no numeral outside the fact set", false, "clean", bad, "")
	}

	invented := "Revenue fell 18.4% to ₹9,99,999 across 4242 payments."
	if bad, ok := narrator.Guard(fs, invented); !ok {
		r.add("guard rejects an invented figure", true, "rejected", "rejected "+bad, "")
	} else {
		r.add("guard rejects an invented figure", false, "rejected", "accepted", invented)
	}

	restated := fmt.Sprintf("Captured revenue was %s across %s payments.",
		fs.Get("current_revenue"), fs.Get("current_count"))
	if _, ok := narrator.Guard(fs, restated); ok {
		r.add("guard accepts a faithful restatement", true, "accepted", "accepted", "")
	} else {
		r.add("guard accepts a faithful restatement", false, "accepted", "rejected", restated)
	}

	// Money boundary: paise in, paise out, no float drift anywhere in between.
	back, err := money.ParseRupees(strings.TrimPrefix(fs.Get("current_revenue"), "₹"))
	eq("money round-trips through the format boundary",
		fmt.Sprint(cur.captured), fmt.Sprint(back))
	if err != nil {
		r.add("money parse of the headline figure", false, "no error", err.Error(), "")
	}

	r.Elapsed = time.Since(t0)
	return r
}

type rawT struct {
	captured  int64
	capturedN int
	total     int
	failed    int
}

// rawTotals walks the rows one at a time. No SUM, no GROUP BY — this is the
// independent path the engine is checked against.
func rawTotals(s *store.Store, w analytics.Window) rawT {
	var t rawT
	rows, err := s.DB.Query(`SELECT status, amount_paise FROM payments WHERE created_at >= ? AND created_at < ?`,
		w.From.Unix(), w.To.Unix())
	if err != nil {
		return t
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var amt int64
		if rows.Scan(&st, &amt) != nil {
			continue
		}
		t.total++
		switch st {
		case "captured":
			t.captured += amt
			t.capturedN++
		case "failed":
			t.failed++
		}
	}
	return t
}

func trunc(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
