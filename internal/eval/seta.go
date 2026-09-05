package eval

import (
	"fmt"
	"time"

	"github.com/rajarshidattapy/finterminal/internal/planner"
)

// planCase is one utterance with the plan it must produce. Windows are matched
// by day-count tolerance, not exact strings — the phrase "this week" is
// legitimately ambiguous by a day, the capability is not.
type planCase struct {
	utterance string
	cap       planner.Capability
	method    string // "" = don't care
	compare   string // "" = don't care, "-" = must be empty
	days      int    // expected window length in days, 0 = don't care
}

// SetA is 60 utterances: paraphrases, Hinglish, typos and ambiguous windows.
var SetA = []planCase{
	{"why did revenue drop this week?", planner.CapRevenueBreakdown, "", "previous_period", 0},
	{"revenue is down, what happened", planner.CapRevenueBreakdown, "", "previous_period", 0},
	{"how much did I make last month", planner.CapRevenueBreakdown, "", "", 0},
	{"total collected this month", planner.CapRevenueBreakdown, "", "", 0},
	{"sales for the last 30 days", planner.CapRevenueBreakdown, "", "", 30},
	{"what's my gmv this week", planner.CapRevenueBreakdown, "", "", 0},
	{"compare this week's revenue to last week", planner.CapRevenueBreakdown, "", "previous_period", 0},
	{"kitna kamaya iss hafte", planner.CapRevenueBreakdown, "", "", 0},
	{"pichle mahine ki kamai", planner.CapRevenueBreakdown, "", "", 0},
	{"revenu drop kyun hua", planner.CapRevenueBreakdown, "", "previous_period", 0},
	{"turnover for the past 7 days", planner.CapRevenueBreakdown, "", "", 7},
	{"income today", planner.CapRevenueBreakdown, "", "", 1},

	{"what's my success rate on UPI?", planner.CapSuccessRate, "upi", "", 0},
	{"payment success rate this month", planner.CapSuccessRate, "", "", 0},
	{"card success rate last 14 days", planner.CapSuccessRate, "card", "", 14},
	{"success ratio for netbanking", planner.CapSuccessRate, "netbanking", "", 0},
	{"conversion rate yesterday", planner.CapSuccessRate, "", "", 1},
	{"kitne percent success hue aaj", planner.CapSuccessRate, "", "", 1},
	{"whats my sucess rate", planner.CapSuccessRate, "", "", 0},
	{"success rate on gpay", planner.CapSuccessRate, "upi", "", 0},

	{"why are UPI payments failing?", planner.CapFailureAnalysis, "upi", "", 0},
	{"investigate the failures", planner.CapFailureAnalysis, "", "", 0},
	{"what errors are we seeing on cards", planner.CapFailureAnalysis, "card", "", 0},
	{"show me why payments declined last week", planner.CapFailureAnalysis, "", "", 7},
	{"upi fail ho rahe hain kyun", planner.CapFailureAnalysis, "upi", "", 0},
	{"failure reasons for the last 3 days", planner.CapFailureAnalysis, "", "", 3},
	{"why did payments fail today", planner.CapFailureAnalysis, "", "", 1},
	{"biggest error codes this month", planner.CapFailureAnalysis, "", "", 0},

	{"payments above ₹50,000 today", planner.CapListPayments, "", "", 1},
	{"list payments over 25000 this week", planner.CapListPayments, "", "", 0},
	{"show me payments from yesterday", planner.CapListPayments, "", "", 1},
	{"which payments were above 1 lakh last month", planner.CapListPayments, "", "", 0},
	{"transactions in the last 2 days", planner.CapListPayments, "", "", 2},
	{"top 10 payments this week", planner.CapListPayments, "", "", 0},
	{"upi payments today", planner.CapListPayments, "upi", "", 1},
	{"failed payments this week", planner.CapListPayments, "", "", 0},
	{"aaj ke payments dikhao", planner.CapListPayments, "", "", 1},
	{"list wallet transactions", planner.CapListPayments, "wallet", "", 0},

	{"which customers haven't paid?", planner.CapUnpaidInvoices, "", "", 0},
	{"unpaid invoices", planner.CapUnpaidInvoices, "", "", 0},
	{"what's outstanding right now", planner.CapUnpaidInvoices, "", "", 0},
	{"pending invoice list", planner.CapUnpaidInvoices, "", "", 0},
	{"kisne paisa nahi diya", planner.CapUnpaidInvoices, "", "", 0},
	{"who has not paid their invoice", planner.CapUnpaidInvoices, "", "", 0},

	{"settlement total this month?", planner.CapSettlementSummary, "", "", 0},
	{"how much was settled last week", planner.CapSettlementSummary, "", "", 7},
	{"iss hafte ka settlement kitna hai", planner.CapSettlementSummary, "", "", 0},
	{"settlements in the last 10 days", planner.CapSettlementSummary, "", "", 10},
	{"what settled yesterday", planner.CapSettlementSummary, "", "", 1},
	{"bank account credit this month", planner.CapSettlementSummary, "", "", 0},

	{"find suspiciously repeated payments", planner.CapDuplicateDetect, "", "", 0},
	{"any duplicate charges this week", planner.CapDuplicateDetect, "", "", 0},
	{"did anyone get charged twice", planner.CapDuplicateDetect, "", "", 0},
	{"double charge check for last 30 days", planner.CapDuplicateDetect, "", "", 30},
	{"customers charged do baar", planner.CapDuplicateDetect, "", "", 0},

	{"show me pay_29QQoUBi66xm2f", planner.CapEntityLookup, "", "", 0},
	{"what happened with pay_29QQoUBi66xm2f", planner.CapEntityLookup, "", "", 0},
	{"look up order_JKa9fL2mNq44Xz", planner.CapEntityLookup, "", "", 0},
	{"details for pay_JKa9fL2mNq44Xz please", planner.CapEntityLookup, "", "", 0},
	{"pay_29QQoUBi66xm2f status", planner.CapEntityLookup, "", "", 0},
}

// RunA scores planning accuracy. Exact match on capability, tolerance match on
// the window.
func RunA(p planner.Interface, now time.Time) *Report {
	t0 := time.Now()
	r := &Report{Set: "A · planning accuracy"}
	for _, c := range SetA {
		plan := p.Plan(c.utterance, false)
		if plan.Kind != planner.KindQuery || plan.Query == nil {
			r.add(c.utterance, false, string(c.cap), string(plan.Kind), plan.Reason)
			continue
		}
		q := plan.Query
		if q.Capability != c.cap {
			r.add(c.utterance, false, string(c.cap), string(q.Capability), "")
			continue
		}
		if c.method != "" && q.Filters.Method != c.method {
			r.add(c.utterance, false, "method="+c.method, "method="+q.Filters.Method, "")
			continue
		}
		if c.compare == "previous_period" && q.CompareTo != "previous_period" {
			r.add(c.utterance, false, "compare_to=previous_period", "compare_to="+q.CompareTo, "")
			continue
		}
		if c.days > 0 {
			got := windowDays(q.Window)
			if got != c.days {
				r.add(c.utterance, false, fmt.Sprintf("%dd window", c.days), fmt.Sprintf("%dd", got), "")
				continue
			}
		}
		r.add(c.utterance, true, string(c.cap), string(q.Capability), "")
	}
	r.Elapsed = time.Since(t0)
	return r
}

func windowDays(w planner.Window) int {
	from, err1 := time.Parse("2006-01-02", w.From)
	to, err2 := time.Parse("2006-01-02", w.To)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(to.Sub(from).Hours()/24) + 1
}
