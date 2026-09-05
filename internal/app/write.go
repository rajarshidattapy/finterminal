package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rajarshidattapy/finterminal/internal/analytics"
	"github.com/rajarshidattapy/finterminal/internal/audit"
	"github.com/rajarshidattapy/finterminal/internal/planner"
	"github.com/rajarshidattapy/finterminal/internal/policy"
)

// WriteRequest is a decided-but-not-executed write. The caller must show Card
// and pass what the user typed to Confirm; there is no path that skips it.
type WriteRequest struct {
	Intent   *planner.WriteIntent `json:"intent"`
	Decision *policy.Decision     `json:"decision"`
	Card     string               `json:"-"`
	Token    string               `json:"-"`
	fresh    *analytics.PaymentView
	utter    string
}

func (s *Session) handleWrite(utterance string, p *planner.Plan, t0 time.Time) *Answer {
	w := p.Write
	sess := policy.Session{
		WriteEnabled: s.Cfg.WriteMode,
		LiveMode:     s.Cfg.LiveMode,
		Interactive:  s.interactive(),
		LinksSent:    s.LinksSent,
	}

	// Layer 4: re-read the entity now, from the source, and render from that.
	var fresh *analytics.PaymentView
	if w.PaymentID != "" && strings.HasPrefix(w.PaymentID, "pay_") {
		if v, err := analytics.FetchPayment(s.Store, w.PaymentID); err == nil {
			fresh = v
		}
	}
	// A refund with no stated amount means the full refundable balance — resolved
	// from the fresh read, never from the model.
	if w.Capability == planner.CapCreateRefund && w.AmountPaise == 0 && fresh != nil {
		w.AmountPaise = fresh.AmountPaise - fresh.Refunded
	}

	d := policy.Evaluate(s.Policy, sess, w, fresh)
	if !d.Allowed {
		s.record(audit.Entry{Utterance: utterance, Kind: "write_refused", Capability: string(w.Capability),
			PlanSource: p.Source, Plan: w, Policy: d, Refusal: d.Refusal,
			LatencyMS: time.Since(t0).Milliseconds()})
		return &Answer{Plan: p, Refusal: d.Refusal}
	}

	if err := s.Store.ReserveIdempotencyKey(d.IdempotencyKey, string(w.Capability), mustJSON(w)); err != nil {
		return &Answer{Plan: p, Refusal: "could not reserve an idempotency key: " + err.Error()}
	}

	card := policy.ConfirmationCardNoEntity(w, d, s.Cfg.LiveMode)
	if fresh != nil {
		card = policy.ConfirmationCard(w, fresh, d, s.Cfg.LiveMode)
	}
	s.record(audit.Entry{Utterance: utterance, Kind: "write_pending", Capability: string(w.Capability),
		PlanSource: p.Source, Plan: w, Policy: d, LatencyMS: time.Since(t0).Milliseconds()})

	return &Answer{Plan: p, WriteReq: &WriteRequest{
		Intent: w, Decision: d, Card: card, Token: policy.ConfirmToken(w),
		fresh: fresh, utter: utterance,
	}}
}

// Confirm executes the write if and only if typed matches the required token.
func (s *Session) Confirm(r *WriteRequest, typed string) string {
	ok := strings.TrimSpace(typed) == r.Token
	confirmed := ok
	if !ok {
		s.record(audit.Entry{Utterance: r.utter, Kind: "write_cancelled",
			Capability: string(r.Intent.Capability), Plan: r.Intent, Confirmed: &confirmed})
		return "  Cancelled. Nothing was sent to the API."
	}

	// A key that already carries a response means this is a retry, not a second write.
	if respID, done := s.Store.LookupIdempotency(r.Decision.IdempotencyKey); done {
		return fmt.Sprintf("  Already executed under %s → %s (idempotent, not repeated).",
			r.Decision.IdempotencyKey, respID)
	}

	respID, msg, err := s.apply(r)
	if err != nil {
		s.record(audit.Entry{Utterance: r.utter, Kind: "write_failed",
			Capability: string(r.Intent.Capability), Plan: r.Intent, Confirmed: &confirmed,
			Refusal: err.Error()})
		return "  Failed: " + err.Error()
	}
	_ = s.Store.RecordIdempotencyResult(r.Decision.IdempotencyKey, respID)
	s.record(audit.Entry{Utterance: r.utter, Kind: "write_executed",
		Capability: string(r.Intent.Capability), Plan: r.Intent, Policy: r.Decision,
		Confirmed: &confirmed, ResponseID: respID})
	return msg
}

// apply is the executor. In test mode it applies the effect to the local
// mirror; the MCP-backed executor plugs in here behind the same signature.
func (s *Session) apply(r *WriteRequest) (string, string, error) {
	w := r.Intent
	switch w.Capability {
	case planner.CapCreateRefund:
		id := "rfnd_" + strings.TrimPrefix(r.Decision.IdempotencyKey, "rzpai_")
		newRefunded := r.fresh.Refunded + w.AmountPaise
		status := "captured"
		if newRefunded >= r.fresh.AmountPaise {
			status = "refunded"
		}
		if _, err := s.Store.DB.Exec(`UPDATE payments SET refunded_paise=?, status=? WHERE id=?`,
			newRefunded, status, w.PaymentID); err != nil {
			return "", "", err
		}
		return id, fmt.Sprintf("  Refund %s created against %s.", id, w.PaymentID), nil

	case planner.CapCapturePayment:
		if _, err := s.Store.DB.Exec(`UPDATE payments SET status='captured', captured=1 WHERE id=?`,
			w.PaymentID); err != nil {
			return "", "", err
		}
		return w.PaymentID, fmt.Sprintf("  Captured %s.", w.PaymentID), nil

	case planner.CapCreatePaymentLink:
		id := "plink_" + strings.TrimPrefix(r.Decision.IdempotencyKey, "rzpai_")
		return id, fmt.Sprintf("  Payment link %s created.", id), nil

	case planner.CapSendPaymentLink:
		s.LinksSent++
		return w.PaymentID, fmt.Sprintf("  Sent %s.", w.PaymentID), nil
	}
	return "", "", fmt.Errorf("no executor for %s", w.Capability)
}

// interactive reports the session's TTY status, with an override used by the
// eval harness so safety cases run identically in CI and in a terminal.
func (s *Session) interactive() bool {
	if s.ForceInteractive != nil {
		return *s.ForceInteractive
	}
	return Interactive()
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
