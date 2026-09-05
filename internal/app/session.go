// Package app wires the pieces together: plan, execute, compute, narrate,
// audit. It is the only place that knows the order of operations.
package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rajarshidattapy/finterminal/internal/analytics"
	"github.com/rajarshidattapy/finterminal/internal/audit"
	"github.com/rajarshidattapy/finterminal/internal/llm"
	"github.com/rajarshidattapy/finterminal/internal/narrator"
	"github.com/rajarshidattapy/finterminal/internal/planner"
	"github.com/rajarshidattapy/finterminal/internal/policy"
	"github.com/rajarshidattapy/finterminal/internal/store"
)

type Config struct {
	DBPath    string
	AuditPath string
	WriteMode bool
	LiveMode  bool
	JSONOut   bool
	Fresh     bool
	Now       func() time.Time
}

type Session struct {
	Cfg      Config
	Store    *store.Store
	Planner  planner.Interface
	Narrator *narrator.Narrator
	Audit    *audit.Log
	Policy   policy.Config
	Provider llm.Provider

	LinksSent           int
	ForceInteractive    *bool
	LastExplain *Explain
}

// Explain is what `--explain` / `/explain` prints. Principle 4 is enforced by
// producing this on every turn, not on request.
type Explain struct {
	Utterance    string `json:"utterance"`
	Capability   string `json:"capability"`
	PlanSource   string `json:"plan_source"`
	Window       string `json:"window"`
	CompareTo    string `json:"compare_to,omitempty"`
	Source       string `json:"source"`
	SyncedAt     string `json:"synced_at"`
	Queries      int    `json:"queries"`
	QueryMS      int64  `json:"query_ms"`
	Rows         int    `json:"rows_scanned"`
	PlanMS       int64  `json:"planner_ms"`
	NarrationVia string `json:"narration_via"`
	Arithmetic   string `json:"arithmetic"`
}

func (e *Explain) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  Utterance   %q\n", e.Utterance)
	fmt.Fprintf(&b, "  Capability  %s\n", e.Capability)
	fmt.Fprintf(&b, "  Planner     %s (%dms)\n", e.PlanSource, e.PlanMS)
	fmt.Fprintf(&b, "  Window      %s", e.Window)
	if e.CompareTo != "" {
		fmt.Fprintf(&b, ", compared to %s", e.CompareTo)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "  Source      %s, synced %s\n", e.Source, e.SyncedAt)
	fmt.Fprintf(&b, "  Queries     %d (%dms total)\n", e.Queries, e.QueryMS)
	fmt.Fprintf(&b, "  Rows        %d scanned\n", e.Rows)
	fmt.Fprintf(&b, "  Narration   %s\n", e.NarrationVia)
	fmt.Fprintf(&b, "  Arithmetic  %s\n", e.Arithmetic)
	return b.String()
}

// Answer is one completed turn.
type Answer struct {
	Plan     *planner.Plan       `json:"plan"`
	Facts    *analytics.FactSet  `json:"facts,omitempty"`
	Text     string              `json:"text"`
	Explain  *Explain            `json:"explain"`
	Refusal  string              `json:"refusal,omitempty"`
	WriteReq *WriteRequest       `json:"write_request,omitempty"`
}

func Open(cfg Config) (*Session, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	prov := llm.FromEnv()
	rules := planner.NewRules(cfg.Now)
	var lp *planner.LLMPlanner
	if prov != nil {
		lp = planner.NewLLM(prov, cfg.Now)
	}
	return &Session{
		Cfg:      cfg,
		Store:    st,
		Planner:  planner.NewChain(rules, lp),
		Narrator: narrator.New(prov),
		Audit:    audit.Open(cfg.AuditPath),
		Policy:   policy.Defaults(),
		Provider: prov,
	}, nil
}

func (s *Session) Close() error { return s.Store.Close() }

// Ask runs one turn end to end.
func (s *Session) Ask(utterance string) *Answer {
	t0 := time.Now()
	p := s.Planner.Plan(utterance, s.Cfg.WriteMode)

	switch p.Kind {
	case planner.KindRefuse:
		a := &Answer{Plan: p, Refusal: p.Reason + "\n\n  " + planner.CapabilityList()}
		s.record(audit.Entry{Utterance: utterance, Kind: "refuse", PlanSource: p.Source,
			Refusal: p.Reason, LatencyMS: time.Since(t0).Milliseconds()})
		return a
	case planner.KindWrite:
		return s.handleWrite(utterance, p, t0)
	}

	fs, err := s.execute(p.Query)
	if err != nil {
		a := &Answer{Plan: p, Refusal: err.Error()}
		s.record(audit.Entry{Utterance: utterance, Kind: "error", Capability: string(p.Query.Capability),
			PlanSource: p.Source, Refusal: err.Error(), LatencyMS: time.Since(t0).Milliseconds()})
		return a
	}

	text, via := s.Narrator.Narrate(fs, utterance)
	ex := &Explain{
		Utterance: utterance, Capability: string(p.Query.Capability), PlanSource: p.Source,
		Window: fmt.Sprintf("%s → %s", fs.WindowFrom, fs.WindowTo), CompareTo: fs.CompareTo,
		Source: "local mirror (" + s.Store.Path + ")", SyncedAt: s.syncedLabel(),
		Queries: fs.Queries, QueryMS: fs.QueryMS, Rows: fs.Rows, PlanMS: p.LatencyMS,
		NarrationVia: via, Arithmetic: "computed in Go — internal/analytics/analytics.go",
	}
	s.LastExplain = ex
	s.record(audit.Entry{
		Utterance: utterance, Kind: "query", Capability: string(p.Query.Capability),
		PlanSource: p.Source, Plan: p.Query, Rows: fs.Rows, Queries: fs.Queries,
		NarrationVia: via, LatencyMS: time.Since(t0).Milliseconds(),
	})
	return &Answer{Plan: p, Facts: fs, Text: text, Explain: ex}
}

func (s *Session) execute(q *planner.QueryPlan) (*analytics.FactSet, error) {
	e := analytics.New(s.Store)
	w, err := toWindow(q.Window)
	if err != nil {
		return nil, err
	}
	compare := q.CompareTo == "previous_period"
	switch q.Capability {
	case planner.CapRevenueBreakdown:
		return e.RevenueBreakdown(w, compare)
	case planner.CapSuccessRate:
		return e.SuccessRate(w, q.Filters.Method)
	case planner.CapFailureAnalysis:
		return e.FailureAnalysis(w, q.Filters.Method)
	case planner.CapListPayments:
		return e.ListPayments(w, q.Filters.Status, q.Filters.Method, q.Filters.MinAmountPaise, q.Limit)
	case planner.CapUnpaidInvoices:
		return e.UnpaidInvoices(q.Limit)
	case planner.CapSettlementSummary:
		return e.SettlementSummary(w)
	case planner.CapDuplicateDetect:
		return e.DuplicateDetection(w, 30*time.Minute)
	case planner.CapEntityLookup:
		return e.EntityLookup(q.EntityID)
	}
	return nil, fmt.Errorf("unsupported capability %q", q.Capability)
}

func toWindow(w planner.Window) (analytics.Window, error) {
	from, err := time.ParseInLocation("2006-01-02", w.From, time.Local)
	if err != nil {
		return analytics.Window{}, fmt.Errorf("bad window start %q", w.From)
	}
	to, err := time.ParseInLocation("2006-01-02", w.To, time.Local)
	if err != nil {
		return analytics.Window{}, fmt.Errorf("bad window end %q", w.To)
	}
	return analytics.Window{From: from, To: to.AddDate(0, 0, 1)}, nil
}

func (s *Session) syncedLabel() string {
	ts := s.Store.LastSync()
	if ts.IsZero() {
		return "never — run `finterminal sync`"
	}
	return fmt.Sprintf("%s (%s ago)", ts.Format(time.RFC3339), time.Since(ts).Round(time.Minute))
}

// StatusLine is the REPL banner.
func (s *Session) StatusLine() string {
	mode := "read-only"
	if s.Cfg.WriteMode {
		mode = "WRITE ENABLED"
	}
	env := "test mode"
	if s.Cfg.LiveMode {
		env = "live mode"
	}
	model := "no model (rules + templates)"
	if s.Provider != nil {
		model = s.Provider.Name()
	}
	return fmt.Sprintf("finterminal · %s · %s · synced %s · %d payments · %s",
		env, mode, s.syncedLabel(), s.Store.CountPayments(), model)
}

func (s *Session) record(e audit.Entry) {
	if err := s.Audit.Append(e); err != nil {
		fmt.Printf("  (audit write failed: %v)\n", err)
	}
}

// MarshalAnswer renders an answer for --json.
func MarshalAnswer(a *Answer) string {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(b)
}
