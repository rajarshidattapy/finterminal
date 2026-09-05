package planner

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RulesPlanner is the deterministic planner. It runs first, costs nothing, and
// makes the whole product usable with no model configured. When an LLM is
// available it is used only for utterances the rules decline (see Chain).
type RulesPlanner struct {
	Now func() time.Time
}

func NewRules(now func() time.Time) *RulesPlanner {
	if now == nil {
		now = time.Now
	}
	return &RulesPlanner{Now: now}
}

func (r *RulesPlanner) Name() string { return "rules" }

var (
	reLastNDays = regexp.MustCompile(`(?:last|past|pichle)\s+(\d{1,3})\s*(?:days?|din)`)
	reAmount    = regexp.MustCompile(`(?:above|over|more than|greater than|>|se zyada|upar)\s*₹?\s*([\d,]+(?:\.\d+)?)\s*(k|l|lakh|cr)?`)
	reRupees    = regexp.MustCompile(`₹\s*([\d,]+(?:\.\d+)?)\s*(k|l|lakh)?|(?:rs\.?|inr)\s*([\d,]+(?:\.\d+)?)\s*(k|l|lakh)?`)
	reLimit     = regexp.MustCompile(`\b(?:top|first|last)\s+(\d{1,3})\b`)
)

// Plan implements the deterministic mapping from utterance to typed plan.
func (r *RulesPlanner) Plan(utterance string, writeMode bool) *Plan {
	t0 := time.Now()
	u := normalise(utterance)
	now := r.Now()

	if p := r.planWrite(u, utterance, now); p != nil {
		p.Source = "rules"
		p.LatencyMS = time.Since(t0).Milliseconds()
		return p
	}
	p := r.planQuery(u, utterance, now)
	if p == nil {
		p = Refusal("I couldn't map that to a supported capability.")
	}
	p.Source = "rules"
	p.LatencyMS = time.Since(t0).Milliseconds()
	return p
}

func (r *RulesPlanner) planWrite(u, raw string, now time.Time) *Plan {
	switch {
	case hasAny(u, "refund", "wapas", "return the money", "paisa wapas"):
		w := &WriteIntent{Capability: CapCreateRefund, Speed: "normal"}
		w.PaymentID = firstMatch(idPay, raw)
		if amt, ok := amountPaise(u); ok {
			w.AmountPaise = amt
		}
		w.StatedReason = extractReason(u)
		return &Plan{Kind: KindWrite, Write: w}
	case hasAny(u, "capture") && !hasAny(u, "capture rate"):
		return &Plan{Kind: KindWrite, Write: &WriteIntent{
			Capability: CapCapturePayment, PaymentID: firstMatch(idPay, raw)}}
	case hasAny(u, "send the link", "send payment link", "resend the link", "send link"):
		return &Plan{Kind: KindWrite, Write: &WriteIntent{
			Capability: CapSendPaymentLink, PaymentID: firstMatch(idLink, raw)}}
	case hasAny(u, "payment link", "create a link", "link banao", "payment ka link"):
		w := &WriteIntent{Capability: CapCreatePaymentLink, Customer: extractCustomer(u)}
		if amt, ok := amountPaise(u); ok {
			w.AmountPaise = amt
		}
		return &Plan{Kind: KindWrite, Write: w}
	}
	return nil
}

func (r *RulesPlanner) planQuery(u, raw string, now time.Time) *Plan {
	win, compare := parseWindow(u, now)
	q := &QueryPlan{Window: win, CompareTo: compare}
	q.Filters.Method = parseMethod(u)
	q.Filters.Status = parseStatus(u)
	if m := reLimit.FindStringSubmatch(u); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 500 {
			q.Limit = n
		}
	}

	if id := firstMatch(idPay, raw); id != "" && !hasAny(u, "refund", "capture") {
		q.Capability = CapEntityLookup
		q.EntityID = id
		return &Plan{Kind: KindQuery, Query: q}
	}
	if id := firstMatch(idOrder, raw); id != "" {
		q.Capability = CapEntityLookup
		q.EntityID = id
		return &Plan{Kind: KindQuery, Query: q}
	}

	switch {
	case hasAny(u, "duplicate", "repeated", "charged twice", "double charge", "do baar"):
		q.Capability = CapDuplicateDetect

	case hasAny(u, "unpaid invoice", "outstanding", "who hasn't paid", "who has not paid",
		"haven't paid", "have not paid", "pending invoice", "invoice", "bakaya", "paisa nahi diya"):
		q.Capability = CapUnpaidInvoices

	case hasAny(u, "settlement", "settled", "payout to my bank", "bank account credit", "settle"):
		q.Capability = CapSettlementSummary

	// A failure question needs both a failure word and an explanatory cue.
	// "failed payments this week" is a listing; "why did payments fail" is not.
	case hasAny(u, "fail", "decline", "error") &&
		hasAny(u, "why", "kyun", "reason", "investigate", "analysis", "analyse", "analyze",
			"what errors", "error codes", "failing", "fail ho rahe", "declin"):
		if hasAny(u, "revenue", "revenu", "sales", "gmv", "income", "kamai") && hasAny(u, "drop", "down", "fall", "fell", "decline", "kam") {
			q.Capability = CapRevenueBreakdown
			q.CompareTo = "previous_period"
		} else {
			q.Capability = CapFailureAnalysis
		}

	case hasAny(u, "success rate", "sucess rate", "succes rate", "conversion rate",
		"success ratio", "sucess", "kitne percent success"):
		q.Capability = CapSuccessRate

	case hasAny(u, "revenue", "revenu", "revanue", "sales", "gmv", "turnover", "income",
		"how much did i make", "kitna kamaya", "kamai", "total collected", "collections"):
		q.Capability = CapRevenueBreakdown
		if hasAny(u, "drop", "down", "fall", "fell", "decline", "change", "compare", "vs", "kam") {
			q.CompareTo = "previous_period"
		}

	case hasAny(u, "list", "show me payments", "payments above", "which payments",
		"payments over", "payments today", "transactions", "payments", "dikhao"):
		q.Capability = CapListPayments
		if amt, ok := amountPaise(u); ok {
			q.Filters.MinAmountPaise = amt
		}

	default:
		return nil
	}

	if q.Capability == CapListPayments {
		if amt, ok := amountPaise(u); ok {
			q.Filters.MinAmountPaise = amt
		}
	}
	return &Plan{Kind: KindQuery, Query: q}
}

// parseWindow maps a time phrase to a concrete date range. Unstated windows
// default to the last 7 days, which is stated in --explain rather than assumed
// silently.
func parseWindow(u string, now time.Time) (Window, string) {
	day := func(t time.Time) string { return t.Format("2006-01-02") }
	today := now.Truncate(24 * time.Hour)

	switch {
	case hasAny(u, "today", "aaj"):
		return Window{From: day(today), To: day(today)}, ""
	case hasAny(u, "yesterday", "kal"):
		y := today.AddDate(0, 0, -1)
		return Window{From: day(y), To: day(y)}, ""
	case hasAny(u, "this week", "iss hafte", "is hafte", "hafte ka", "week to date"):
		start := startOfWeek(today)
		return Window{From: day(start), To: day(today)}, "previous_period"
	case hasAny(u, "last week", "pichle hafte", "previous week"):
		start := startOfWeek(today).AddDate(0, 0, -7)
		return Window{From: day(start), To: day(start.AddDate(0, 0, 6))}, "previous_period"
	case hasAny(u, "this month", "is mahine", "iss mahine", "month to date"):
		start := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
		return Window{From: day(start), To: day(today)}, ""
	case hasAny(u, "last month", "pichle mahine"):
		first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
		start := first.AddDate(0, -1, 0)
		return Window{From: day(start), To: day(first.AddDate(0, 0, -1))}, ""
	case hasAny(u, "this quarter", "last quarter"):
		return Window{From: day(today.AddDate(0, -3, 0)), To: day(today)}, ""
	}
	if m := reLastNDays.FindStringSubmatch(u); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 365 {
			return Window{From: day(today.AddDate(0, 0, -n+1)), To: day(today)}, ""
		}
	}
	// Default: trailing 7 days, compared with the 7 before it.
	return Window{From: day(today.AddDate(0, 0, -6)), To: day(today)}, "previous_period"
}

func startOfWeek(t time.Time) time.Time {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7 // ISO: week starts Monday
	}
	return t.AddDate(0, 0, -(wd - 1))
}

func parseMethod(u string) string {
	switch {
	case hasAny(u, "upi", "gpay", "google pay", "phonepe", "paytm upi", "vpa"):
		return "upi"
	case hasAny(u, "card", "credit card", "debit card", "visa", "mastercard", "rupay"):
		return "card"
	case hasAny(u, "netbanking", "net banking", "bank transfer"):
		return "netbanking"
	case hasAny(u, "wallet"):
		return "wallet"
	}
	return ""
}

func parseStatus(u string) string {
	switch {
	case hasAny(u, "failed payment", "failed payments", "failures only"):
		return "failed"
	case hasAny(u, "captured payment", "successful payment", "successful payments", "paid payments"):
		return "captured"
	case hasAny(u, "authorized", "authorised", "uncaptured"):
		return "authorized"
	}
	return ""
}

// amountPaise extracts a rupee figure from an utterance and converts it once,
// here, to paise.
func amountPaise(u string) (int64, bool) {
	if m := reAmount.FindStringSubmatch(u); m != nil {
		if v, ok := toPaise(m[1], m[2]); ok {
			return v, true
		}
	}
	if m := reRupees.FindStringSubmatch(u); m != nil {
		num, suffix := m[1], m[2]
		if num == "" {
			num, suffix = m[3], m[4]
		}
		if v, ok := toPaise(num, suffix); ok {
			return v, true
		}
	}
	return 0, false
}

func toPaise(num, suffix string) (int64, bool) {
	num = strings.ReplaceAll(num, ",", "")
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	switch suffix {
	case "k":
		f *= 1_000
	case "l", "lakh":
		f *= 100_000
	case "cr":
		f *= 10_000_000
	}
	return int64(f*100 + 0.5), true
}

func extractCustomer(u string) string {
	for _, kw := range []string{" for ", " to ", " ke liye "} {
		if i := strings.LastIndex(u, kw); i >= 0 {
			name := strings.TrimSpace(u[i+len(kw):])
			name = strings.Trim(name, ".?!,")
			if name != "" && len(name) < 40 {
				return name
			}
		}
	}
	return ""
}

func extractReason(u string) string {
	for _, kw := range []string{"because ", "since ", "reason: "} {
		if i := strings.Index(u, kw); i >= 0 {
			return strings.TrimSpace(strings.Trim(u[i+len(kw):], ".?!,"))
		}
	}
	return ""
}

func normalise(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("’", "'", "“", "\"", "”", "\"", "  ", " ").Replace(s)
	return s
}

func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func firstMatch(re *regexp.Regexp, s string) string {
	return re.FindString(s)
}
