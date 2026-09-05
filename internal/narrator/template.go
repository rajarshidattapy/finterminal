package narrator

import (
	"fmt"
	"strings"

	"github.com/rajarshidattapy/finterminal/internal/analytics"
	"github.com/rajarshidattapy/finterminal/internal/money"
)

func moneyFormat(p int64) string { return money.FormatShort(p) }

// Template renders a FactSet without a model at all. It is the default
// narration, the fallback when the model misbehaves, and — because it can only
// print facts that exist — the reason the product is safe with no model.
func Template(fs *analytics.FactSet) string {
	var b strings.Builder
	switch fs.Capability {
	case "revenue_breakdown":
		templateRevenue(&b, fs)
	case "payment_success_rate":
		fmt.Fprintf(&b, "  Success rate %s across %s attempted payments (%s captured).\n\n",
			fs.Get("overall_success_rate"), fs.Get("attempted"), fs.Get("captured"))
		table(&b, "  ", fs.Tables["by_method"], func(r analytics.Row) string {
			return fmt.Sprintf("%-12s %6d attempted   %8s", r.Label, r.Count, r.Extra)
		})
	case "failure_analysis":
		fmt.Fprintf(&b, "  %s failed payments in this window.\n\n", fs.Get("failed_total"))
		i := 0
		for _, r := range fs.Tables["patterns"] {
			i++
			if i > 6 {
				break
			}
			fmt.Fprintf(&b, "    %d. %-52s %4d  (%s)\n", i, r.Label, r.Count, r.Extra)
		}
	case "list_payments":
		fmt.Fprintf(&b, "  %s payments, %s in total.\n\n", fs.Get("result_count"), fs.Get("result_total"))
		table(&b, "    ", fs.Tables["payments"], func(r analytics.Row) string {
			return fmt.Sprintf("%-24s %12s  %s", r.Label, moneyFormat(r.Amount), r.Extra)
		})
	case "unpaid_invoices":
		fmt.Fprintf(&b, "  %s unpaid invoices, %s outstanding (%s past due).\n\n",
			fs.Get("unpaid_count"), fs.Get("outstanding_total"), fs.Get("overdue_count"))
		table(&b, "    ", fs.Tables["invoices"], func(r analytics.Row) string {
			return fmt.Sprintf("%-16s %12s  %s", r.Label, moneyFormat(r.Amount), r.Extra)
		})
	case "settlement_summary":
		fmt.Fprintf(&b, "  %s settled across %s settlements.\n  Fees %s · tax %s.\n\n",
			fs.Get("settled_total"), fs.Get("settlement_count"), fs.Get("fees_total"), fs.Get("tax_total"))
		table(&b, "    ", fs.Tables["by_status"], func(r analytics.Row) string {
			return fmt.Sprintf("%-12s %4d  %12s", r.Label, r.Count, moneyFormat(r.Amount))
		})
	case "duplicate_detection":
		fmt.Fprintf(&b, "  %s suspected duplicate pairs (same contact, same amount, within %s minutes).\n  Exposure %s.\n\n",
			fs.Get("duplicate_pairs"), fs.Get("window_minutes"), fs.Get("duplicate_exposure"))
		table(&b, "    ", fs.Tables["pairs"], func(r analytics.Row) string {
			return fmt.Sprintf("%-48s %12s  %s", r.Label, moneyFormat(r.Amount), r.Extra)
		})
	case "entity_lookup":
		fmt.Fprintf(&b, "  %s\n\n    Status     %s\n    Method     %s\n    Amount     %s\n    Refunded   %s\n    Created    %s\n",
			fs.Get("entity_id"), fs.Get("status"), fs.Get("method"),
			fs.Get("amount"), fs.Get("refunded"), fs.Get("created"))
		if c, ok := fs.Meta["contact"]; ok && c != "" {
			fmt.Fprintf(&b, "    Contact    %s\n", c)
		}
	default:
		for _, f := range fs.Facts {
			fmt.Fprintf(&b, "    %-24s %s\n", f.Key, f.Display)
		}
	}
	for _, note := range fs.Notes {
		fmt.Fprintf(&b, "\n  %s\n", note)
	}
	return strings.TrimRight(b.String(), "\n")
}

func templateRevenue(b *strings.Builder, fs *analytics.FactSet) {
	if fs.CompareTo == "previous_period" {
		fmt.Fprintf(b, "  Captured revenue %s %s period over period.\n\n",
			fs.Get("delta_direction"), fs.Get("delta_pct"))
		fmt.Fprintf(b, "    This period  (%-14s) %14s  ·  %s payments\n",
			fs.Meta["current_label"], fs.Get("current_revenue"), fs.Get("current_count"))
		fmt.Fprintf(b, "    Previous     (%-14s) %14s  ·  %s payments\n",
			fs.Meta["previous_label"], fs.Get("previous_revenue"), fs.Get("previous_count"))
		fmt.Fprintf(b, "    Difference   %31s\n\n", fs.Get("delta_revenue"))
		fmt.Fprintf(b, "    Success rate       %s → %s  (%s)\n",
			fs.Get("previous_success_rate"), fs.Get("current_success_rate"), fs.Get("success_rate_delta_pp"))
		fmt.Fprintf(b, "    Failed payments    %s → %s\n",
			fs.Get("previous_failed"), fs.Get("current_failed"))
	} else {
		fmt.Fprintf(b, "  Captured revenue %s across %s payments (success rate %s).\n\n",
			fs.Get("current_revenue"), fs.Get("current_count"), fs.Get("current_success_rate"))
	}
	fmt.Fprintf(b, "    Unpaid high-value  %s orders   %s outstanding\n\n",
		fs.Get("unpaid_highvalue_count"), fs.Get("unpaid_highvalue_amount"))
	table(b, "    ", fs.Tables["by_method"], func(r analytics.Row) string {
		return fmt.Sprintf("%-12s %14s  ·  %4d captured · %s", r.Label, moneyFormat(r.Amount), r.Count, r.Extra)
	})
}

func table(b *strings.Builder, indent string, rows []analytics.Row, render func(analytics.Row) string) {
	if len(rows) == 0 {
		fmt.Fprintf(b, "%s(nothing matched)\n", indent)
		return
	}
	for i, r := range rows {
		if i >= 25 {
			fmt.Fprintf(b, "%s… %d more\n", indent, len(rows)-i)
			break
		}
		fmt.Fprintf(b, "%s%s\n", indent, render(r))
	}
}
