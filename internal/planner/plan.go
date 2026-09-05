// Package planner turns an utterance into a typed, validated plan. It emits
// exactly one of three shapes — a QueryPlan, a WriteIntent, or a refusal — and
// nothing else. There is no field here that carries SQL, a URL, a shell string
// or a free-form value that reaches an API.
package planner

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Kind string

const (
	KindQuery  Kind = "query"
	KindWrite  Kind = "write"
	KindRefuse Kind = "refuse"
)

// Capability is a closed enum. An unrecognised value is a hard failure.
type Capability string

const (
	CapRevenueBreakdown  Capability = "revenue_breakdown"
	CapSuccessRate       Capability = "payment_success_rate"
	CapFailureAnalysis   Capability = "failure_analysis"
	CapListPayments      Capability = "list_payments"
	CapUnpaidInvoices    Capability = "unpaid_invoices"
	CapSettlementSummary Capability = "settlement_summary"
	CapDuplicateDetect   Capability = "duplicate_detection"
	CapEntityLookup      Capability = "entity_lookup"

	CapCreatePaymentLink Capability = "create_payment_link"
	CapCreateRefund      Capability = "create_refund"
	CapCapturePayment    Capability = "capture_payment"
	CapSendPaymentLink   Capability = "send_payment_link"
)

var readCapabilities = map[Capability]string{
	CapRevenueBreakdown:  "revenue and period-over-period movement",
	CapSuccessRate:       "payment success rate, overall or by method",
	CapFailureAnalysis:   "why payments are failing, grouped by error",
	CapListPayments:      "list payments with filters",
	CapUnpaidInvoices:    "invoices with an outstanding balance",
	CapSettlementSummary: "settlement totals for a period",
	CapDuplicateDetect:   "suspiciously repeated charges",
	CapEntityLookup:      "look up one payment or order by id",
}

var writeCapabilities = map[Capability]string{
	CapCreatePaymentLink: "create a payment link",
	CapCreateRefund:      "refund a payment",
	CapCapturePayment:    "capture an authorized payment",
	CapSendPaymentLink:   "send an existing payment link",
}

// IsRead reports whether c is a registered read capability.
func IsRead(c Capability) bool { _, ok := readCapabilities[c]; return ok }

// IsWrite reports whether c is a registered write capability.
func IsWrite(c Capability) bool { _, ok := writeCapabilities[c]; return ok }

// Known reports whether c is in the enum at all.
func Known(c Capability) bool { return IsRead(c) || IsWrite(c) }

// Capabilities returns every registered capability with its description.
func Capabilities() (read, write map[Capability]string) {
	return readCapabilities, writeCapabilities
}

// Filters are the only query narrowing the planner may express.
type Filters struct {
	Method         string `json:"method,omitempty"`
	Status         string `json:"status,omitempty"`
	MinAmountPaise int64  `json:"min_amount_paise,omitempty"`
}

type Window struct {
	From string `json:"from"` // YYYY-MM-DD, inclusive
	To   string `json:"to"`   // YYYY-MM-DD, inclusive
}

type QueryPlan struct {
	Capability Capability `json:"capability"`
	Window     Window     `json:"window"`
	CompareTo  string     `json:"compare_to,omitempty"` // "" or "previous_period"
	Filters    Filters    `json:"filters"`
	GroupBy    []string   `json:"group_by,omitempty"`
	SortBy     string     `json:"sort_by,omitempty"` // "" (most recent) or "amount"
	EntityID   string     `json:"entity_id,omitempty"`
	Limit      int        `json:"limit,omitempty"`
}

type WriteIntent struct {
	Capability   Capability `json:"capability"`
	PaymentID    string     `json:"payment_id,omitempty"`
	AmountPaise  int64      `json:"amount_paise,omitempty"`
	Speed        string     `json:"speed,omitempty"`
	Customer     string     `json:"customer,omitempty"`
	StatedReason string     `json:"stated_reason,omitempty"`
}

type Plan struct {
	Kind      Kind         `json:"kind"`
	Query     *QueryPlan   `json:"query,omitempty"`
	Write     *WriteIntent `json:"write,omitempty"`
	Reason    string       `json:"reason,omitempty"` // populated on refusal
	Source    string       `json:"source"`           // "rules" or "llm"
	LatencyMS int64        `json:"latency_ms"`
}

var (
	idPay   = regexp.MustCompile(`\bpay_[A-Za-z0-9]{4,}\b`)
	idOrder = regexp.MustCompile(`\border_[A-Za-z0-9]{4,}\b`)
	idLink  = regexp.MustCompile(`\bplink_[A-Za-z0-9]{4,}\b`)
)

// Validate enforces the typed contract before anything executes.
func (p *Plan) Validate() error {
	switch p.Kind {
	case KindRefuse:
		if p.Reason == "" {
			return fmt.Errorf("refusal without a reason")
		}
		return nil
	case KindQuery:
		if p.Query == nil {
			return fmt.Errorf("query plan missing")
		}
		q := p.Query
		if !IsRead(q.Capability) {
			return fmt.Errorf("unknown read capability %q", q.Capability)
		}
		if q.Capability == CapEntityLookup {
			if !idPay.MatchString(q.EntityID) && !idOrder.MatchString(q.EntityID) {
				return fmt.Errorf("entity_lookup needs a pay_ or order_ id, got %q", q.EntityID)
			}
		}
		if q.Window.From != "" {
			if _, err := time.Parse("2006-01-02", q.Window.From); err != nil {
				return fmt.Errorf("bad window.from %q", q.Window.From)
			}
		}
		if q.Window.To != "" {
			if _, err := time.Parse("2006-01-02", q.Window.To); err != nil {
				return fmt.Errorf("bad window.to %q", q.Window.To)
			}
		}
		if q.CompareTo != "" && q.CompareTo != "previous_period" {
			return fmt.Errorf("unsupported compare_to %q", q.CompareTo)
		}
		switch q.Filters.Method {
		case "", "upi", "card", "netbanking", "wallet":
		default:
			return fmt.Errorf("unsupported method filter %q", q.Filters.Method)
		}
		switch q.Filters.Status {
		case "", "created", "authorized", "captured", "failed", "refunded":
		default:
			return fmt.Errorf("unsupported status filter %q", q.Filters.Status)
		}
		if q.Filters.MinAmountPaise < 0 {
			return fmt.Errorf("negative min_amount_paise")
		}
		switch q.SortBy {
		case "", "recent", "amount":
		default:
			return fmt.Errorf("unsupported sort_by %q", q.SortBy)
		}
		if q.Limit < 0 || q.Limit > 500 {
			return fmt.Errorf("limit out of range: %d", q.Limit)
		}
		return nil
	case KindWrite:
		if p.Write == nil {
			return fmt.Errorf("write intent missing")
		}
		w := p.Write
		if !IsWrite(w.Capability) {
			return fmt.Errorf("unknown write capability %q", w.Capability)
		}
		switch w.Capability {
		case CapCreateRefund, CapCapturePayment:
			if !idPay.MatchString(w.PaymentID) {
				return fmt.Errorf("%s needs a pay_ id, got %q", w.Capability, w.PaymentID)
			}
		case CapSendPaymentLink:
			if !idLink.MatchString(w.PaymentID) {
				return fmt.Errorf("send_payment_link needs a plink_ id, got %q", w.PaymentID)
			}
		}
		if w.AmountPaise < 0 {
			return fmt.Errorf("negative amount")
		}
		switch w.Speed {
		case "", "normal", "optimum":
		default:
			return fmt.Errorf("unsupported refund speed %q", w.Speed)
		}
		return nil
	}
	return fmt.Errorf("unknown plan kind %q", p.Kind)
}

// Refusal builds the refusal a caller shows when nothing maps.
func Refusal(reason string) *Plan {
	return &Plan{Kind: KindRefuse, Reason: reason, Source: "rules"}
}

// CapabilityList renders the supported surface for a refusal message.
func CapabilityList() string {
	var b strings.Builder
	b.WriteString("I can answer:\n")
	for _, c := range []Capability{CapRevenueBreakdown, CapSuccessRate, CapFailureAnalysis,
		CapListPayments, CapUnpaidInvoices, CapSettlementSummary, CapDuplicateDetect, CapEntityLookup} {
		fmt.Fprintf(&b, "    · %-22s %s\n", c, readCapabilities[c])
	}
	b.WriteString("  And, in a --write session:\n")
	for _, c := range []Capability{CapCreatePaymentLink, CapCreateRefund, CapCapturePayment, CapSendPaymentLink} {
		fmt.Fprintf(&b, "    · %-22s %s\n", c, writeCapabilities[c])
	}
	return b.String()
}
