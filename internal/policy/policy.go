// Package policy is the write plane. Nothing reaches a money-moving call
// without passing every check here, and every check is enforced in Go — the
// prompt is never the only line of defence.
package policy

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
	"time"

	"github.com/rajarshidattapy/finterminal/internal/analytics"
	"github.com/rajarshidattapy/finterminal/internal/money"
	"github.com/rajarshidattapy/finterminal/internal/planner"
)

// Config holds the ceilings. Defaults are conservative; ~/.razorpay/ai.yaml
// may lower them, and lowering is the only direction that needs no thought.
type Config struct {
	RefundCeilingPaise      int64
	PaymentLinkCeilingPaise int64
	SendLinkPerSession      int
	AllowRefundInLive       bool // v1: false, always
}

func Defaults() Config {
	return Config{
		RefundCeilingPaise:      2_500_000,  // ₹25,000
		PaymentLinkCeilingPaise: 10_000_000, // ₹1,00,000
		SendLinkPerSession:      5,
		AllowRefundInLive:       false,
	}
}

// Session carries the runtime facts a policy decision depends on.
type Session struct {
	WriteEnabled bool
	LiveMode     bool
	Interactive  bool
	LinksSent    int
}

// Check is one named gate, recorded in the audit log whether it passed or not.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// Decision is the result of running every gate. Allowed means "may show a
// confirmation prompt", never "has been executed".
type Decision struct {
	Allowed        bool    `json:"allowed"`
	Checks         []Check `json:"checks"`
	Refusal        string  `json:"refusal,omitempty"`
	IdempotencyKey string  `json:"idempotency_key,omitempty"`
}

func (d *Decision) add(name string, pass bool, detail string) {
	d.Checks = append(d.Checks, Check{Name: name, Pass: pass, Detail: detail})
	if !pass && d.Refusal == "" {
		d.Refusal = detail
	}
}

// Evaluate runs the gates in order. `fresh` must be a view read from the API
// (or mirror, in test mode) after planning — never a value carried over from
// the conversation.
func Evaluate(cfg Config, s Session, w *planner.WriteIntent, fresh *analytics.PaymentView) *Decision {
	d := &Decision{}

	d.add("capability_allowlist", planner.IsWrite(w.Capability),
		fmt.Sprintf("%q is not a registered write capability", w.Capability))
	if !d.Checks[0].Pass {
		return d
	}

	d.add("write_session", s.WriteEnabled,
		"Write capability requires an elevated session. Restart with:  finterminal --write")
	d.add("interactive_tty", s.Interactive,
		"Writes are unavailable without a TTY — there is no confirmation path in CI or cron.")

	switch w.Capability {
	case planner.CapCreateRefund:
		d.add("live_mode_refund_block", !s.LiveMode || cfg.AllowRefundInLive,
			"Refunds are blocked outright in live mode in v1. This is deliberate, not an omission.")
		d.add("amount_ceiling", w.AmountPaise <= cfg.RefundCeilingPaise,
			fmt.Sprintf("Refund of %s exceeds the configured ceiling of %s.",
				money.FormatShort(w.AmountPaise), money.FormatShort(cfg.RefundCeilingPaise)))
		if fresh == nil {
			d.add("fresh_reread", false, "Could not re-read the payment; refusing to render a confirmation from context.")
		} else {
			d.add("fresh_reread", true, fmt.Sprintf("re-read %s at %s",
				fresh.ID, fresh.FetchedAt.Format(time.RFC3339)))
			d.add("entity_status", fresh.Status == "captured",
				fmt.Sprintf("Payment %s is %s, not captured — nothing to refund.", fresh.ID, fresh.Status))
			remaining := fresh.AmountPaise - fresh.Refunded
			d.add("not_already_refunded", remaining > 0,
				fmt.Sprintf("Payment %s is already fully refunded.", fresh.ID))
			d.add("amount_within_entity", w.AmountPaise <= remaining,
				fmt.Sprintf("Refund of %s exceeds the %s still refundable on %s.",
					money.FormatShort(w.AmountPaise), money.FormatShort(remaining), fresh.ID))
			d.add("amount_positive", w.AmountPaise > 0, "Refund amount must be greater than zero.")
		}

	case planner.CapCapturePayment:
		if fresh == nil {
			d.add("fresh_reread", false, "Could not re-read the payment; refusing to render a confirmation from context.")
		} else {
			d.add("fresh_reread", true, fmt.Sprintf("re-read %s at %s", fresh.ID, fresh.FetchedAt.Format(time.RFC3339)))
			d.add("entity_status", fresh.Status == "authorized",
				fmt.Sprintf("Payment %s is %s; only an authorized payment can be captured.", fresh.ID, fresh.Status))
		}

	case planner.CapCreatePaymentLink:
		d.add("amount_ceiling", w.AmountPaise <= cfg.PaymentLinkCeilingPaise,
			fmt.Sprintf("Payment link of %s exceeds the ceiling of %s.",
				money.FormatShort(w.AmountPaise), money.FormatShort(cfg.PaymentLinkCeilingPaise)))
		d.add("amount_positive", w.AmountPaise > 0, "A payment link needs an amount. Say how much.")

	case planner.CapSendPaymentLink:
		d.add("rate_limit", s.LinksSent < cfg.SendLinkPerSession,
			fmt.Sprintf("Session limit of %d sent links reached.", cfg.SendLinkPerSession))
	}

	for _, c := range d.Checks {
		if !c.Pass {
			return d
		}
	}
	d.Allowed = true
	d.IdempotencyKey = NewIdempotencyKey()
	return d
}

// NewIdempotencyKey returns a sortable, unique key. It is persisted before the
// call is made, so a retry after a network failure reuses it.
func NewIdempotencyKey() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	enc := base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)
	return "rzpai_" + strings.ToUpper(enc.EncodeToString(b[:]))[:14]
}

// ConfirmationCard renders the prompt from `fresh` and nothing else.
func ConfirmationCard(w *planner.WriteIntent, fresh *analytics.PaymentView, d *Decision, live bool) string {
	mode := "test mode"
	if live {
		mode = "LIVE"
	}
	var b strings.Builder
	switch w.Capability {
	case planner.CapCreateRefund:
		remaining := fresh.AmountPaise - fresh.Refunded - w.AmountPaise
		full := "partial"
		if w.AmountPaise == fresh.AmountPaise-fresh.Refunded {
			full = "full"
		}
		fmt.Fprintf(&b, "\n  !  REFUND — %s confirmation\n\n", mode)
		fmt.Fprintf(&b, "     Payment      %s\n", fresh.ID)
		fmt.Fprintf(&b, "     Customer     %s  ·  %s\n", fresh.Email, analytics.RedactContact(fresh.Contact))
		fmt.Fprintf(&b, "     Captured     %s  on %s\n", money.FormatShort(fresh.AmountPaise),
			fresh.CreatedAt.Format("2 Jan 2006, 15:04"))
		fmt.Fprintf(&b, "     Refunding    %s  (%s)\n", money.FormatShort(w.AmountPaise), full)
		fmt.Fprintf(&b, "     Remaining    %s\n", money.FormatShort(remaining))
		fmt.Fprintf(&b, "     Speed        %s\n", orDefault(w.Speed, "normal"))
		fmt.Fprintf(&b, "     Idempotency  %s\n\n", d.IdempotencyKey)
		fmt.Fprintf(&b, "     Source: read fresh %s ago — not from cache, not from the model.\n\n",
			time.Since(fresh.FetchedAt).Round(100*time.Millisecond))
		fmt.Fprintf(&b, "  Type the payment ID to confirm, or press Enter to cancel: ")
	case planner.CapCapturePayment:
		fmt.Fprintf(&b, "\n  !  CAPTURE — %s confirmation\n\n", mode)
		fmt.Fprintf(&b, "     Payment      %s\n", fresh.ID)
		fmt.Fprintf(&b, "     Status       %s\n", fresh.Status)
		fmt.Fprintf(&b, "     Amount       %s\n", money.FormatShort(fresh.AmountPaise))
		fmt.Fprintf(&b, "     Idempotency  %s\n\n", d.IdempotencyKey)
		fmt.Fprintf(&b, "     Source: read fresh %s ago.\n\n", time.Since(fresh.FetchedAt).Round(100*time.Millisecond))
		fmt.Fprintf(&b, "  Type the payment ID to confirm, or press Enter to cancel: ")
	}
	return b.String()
}

// ConfirmationCardNoEntity renders confirmations for writes that have no
// existing entity to re-read.
func ConfirmationCardNoEntity(w *planner.WriteIntent, d *Decision, live bool) string {
	mode := "test mode"
	if live {
		mode = "LIVE"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n  !  PAYMENT LINK — %s confirmation\n\n", mode)
	fmt.Fprintf(&b, "     Amount       %s\n", money.FormatShort(w.AmountPaise))
	fmt.Fprintf(&b, "     Customer     %s\n", orDefault(w.Customer, "(unspecified)"))
	fmt.Fprintf(&b, "     Idempotency  %s\n\n", d.IdempotencyKey)
	fmt.Fprintf(&b, "  Type CONFIRM to create it, or press Enter to cancel: ")
	return b.String()
}

// ConfirmToken is what the user must type back. Typing the entity id rather
// than "y" defeats reflexive confirmation.
func ConfirmToken(w *planner.WriteIntent) string {
	if w.PaymentID != "" {
		return w.PaymentID
	}
	return "CONFIRM"
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
