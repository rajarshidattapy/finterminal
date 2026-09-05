// finterminal — an AI-native terminal for Razorpay finance operations.
//
// The model plans and explains; it does not calculate and it does not move
// money. Everything in this program exists to make that sentence true.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rajarshidattapy/finterminal/internal/app"
	"github.com/rajarshidattapy/finterminal/internal/audit"
	"github.com/rajarshidattapy/finterminal/internal/eval"
	"github.com/rajarshidattapy/finterminal/internal/fixtures"
	"github.com/rajarshidattapy/finterminal/internal/planner"
	"github.com/rajarshidattapy/finterminal/internal/store"
)

const version = "0.1.0"

func main() {
	var (
		writeMode = flag.Bool("write", false, "enable the write plane (confirmations still required)")
		liveMode  = flag.Bool("live", false, "operate against live mode (refunds stay blocked in v1)")
		jsonOut   = flag.Bool("json", false, "emit JSON, no narration — for scripts and cron")
		explain   = flag.Bool("explain", false, "print the query plan, sources and row counts with the answer")
		dbPath    = flag.String("db", store.DefaultPath(), "path to the local read model")
		auditPath = flag.String("audit-log", audit.DefaultPath(), "path to the append-only audit log")
		days      = flag.Int("days", 90, "days of history to mirror on sync")
		lastN     = flag.Int("last", 20, "audit: entries to show")
		set       = flag.String("set", "all", "eval: a, b, c or all")
		verbose   = flag.Bool("v", false, "eval: show passing cases too")
	)
	flag.Usage = usage
	// Flags are accepted before or after the question, so `finterminal "..."
	// --explain` reads the way a person would write it.
	flag.CommandLine.Parse(reorder(os.Args[1:]))

	args := flag.Args()
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}

	cfg := app.Config{
		DBPath: *dbPath, AuditPath: *auditPath,
		WriteMode: *writeMode, LiveMode: *liveMode, JSONOut: *jsonOut,
	}

	switch cmd {
	case "sync":
		os.Exit(cmdSync(cfg, *days))
	case "audit":
		os.Exit(cmdAudit(cfg, *lastN))
	case "eval":
		os.Exit(cmdEval(cfg, *set, *verbose))
	case "version":
		fmt.Println("finterminal " + version)
	case "help":
		usage()
	case "":
		os.Exit(cmdREPL(cfg, *explain))
	default:
		os.Exit(cmdOneShot(cfg, strings.Join(args, " "), *explain))
	}
}

// reorder floats flags ahead of positional arguments so the standard flag
// package sees them wherever the user put them.
func reorder(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// A flag that takes a separate value carries it along.
			if !strings.Contains(a, "=") && i+1 < len(args) && takesValue(strings.TrimLeft(a, "-")) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(flags, rest...)
}

func takesValue(name string) bool {
	f := flag.Lookup(name)
	if f == nil {
		return false
	}
	switch f.Value.String() {
	case "true", "false":
		return false
	}
	return true
}

func usage() {
	fmt.Fprint(os.Stderr, `finterminal — natural-language terminal for Razorpay finance operations

  finterminal                              start the REPL
  finterminal "why did revenue drop"       one-shot question
  finterminal "settlement total" --json    scriptable, no narration
  finterminal --write                      REPL with the write plane enabled
  finterminal sync [--days 90]             refresh the local read model
  finterminal audit [--last 20]            read the append-only audit log
  finterminal eval [--set a|b|c|all]       run the eval harness

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprint(os.Stderr, `
Model: set ANTHROPIC_API_KEY to enable the LLM planner and narrator. Without
one, the deterministic planner and template narrator carry every capability —
the numbers are identical either way, because Go computes them.
`)
}

func cmdSync(cfg app.Config, days int) int {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  sync failed:", err)
		return 1
	}
	defer st.Close()

	t0 := time.Now()
	// v1 ships with the fixture executor: a deterministic, credential-free
	// dataset so `git clone` to a working answer is under five minutes. The MCP
	// executor (internal/mcp) plugs in here behind the same call.
	res, err := fixtures.Generate(st, time.Now(), days)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  sync failed:", err)
		return 1
	}
	fmt.Printf("\n  Synced %d payments, %d orders, %d invoices, %d settlements in %s\n",
		res.Payments, res.Orders, res.Invoices, res.Settlements, time.Since(t0).Round(time.Millisecond))
	fmt.Printf("  Window %s → %s · mirror at %s\n\n",
		res.From.Format("2006-01-02"), res.To.Format("2006-01-02"), st.Path)
	return 0
}

func cmdAudit(cfg app.Config, n int) int {
	entries, err := audit.Open(cfg.AuditPath).Tail(n)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  could not read the audit log:", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Println("\n  No audit entries yet.")
		return 0
	}
	fmt.Printf("\n  %d most recent entries · %s\n\n", len(entries), cfg.AuditPath)
	for _, e := range entries {
		fmt.Printf("  %s  %-15s %-22s %s\n", e.TS.Format("2006-01-02 15:04:05"),
			e.Kind, e.Capability, truncate(e.Utterance, 44))
		if e.Refusal != "" {
			fmt.Printf("  %24s%s\n", "", truncate(strings.ReplaceAll(e.Refusal, "\n", " "), 76))
		}
		if e.ResponseID != "" {
			fmt.Printf("  %24s→ %s\n", "", e.ResponseID)
		}
	}
	fmt.Println()
	return 0
}

func cmdEval(cfg app.Config, set string, verbose bool) int {
	now := time.Now()
	failures := 0

	newSession := func(write, live, interactive bool) (*app.Session, error) {
		c := cfg
		c.WriteMode, c.LiveMode = write, live
		s, err := app.Open(c)
		if err != nil {
			return nil, err
		}
		tty := interactive
		s.ForceInteractive = &tty
		return s, nil
	}

	run := func(r *eval.Report, gate bool) {
		fmt.Print(r.Render(verbose))
		if r.Passed() != r.Total() {
			failures++
			if gate {
				fmt.Println("    ↑ Set C failures block release.")
			}
		}
	}

	if set == "a" || set == "all" {
		s, err := app.Open(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		run(eval.RunA(s.Planner, now), false)
		s.Close()
	}
	if set == "b" || set == "all" {
		s, err := app.Open(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if s.Store.CountPayments() == 0 {
			fmt.Println("\n  Set B needs a mirror. Run `finterminal sync` first.")
			s.Close()
			return 1
		}
		run(eval.RunB(s.Store, now), false)
		s.Close()
	}
	if set == "c" || set == "safety" || set == "all" {
		run(eval.RunC(newSession, now), true)
	}
	fmt.Println()
	if failures > 0 {
		return 1
	}
	return 0
}

func cmdOneShot(cfg app.Config, utterance string, explain bool) int {
	s, err := app.Open(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer s.Close()
	a := s.Ask(utterance)
	if cfg.JSONOut {
		fmt.Println(app.MarshalAnswer(a))
		if a.Refusal != "" {
			return 2
		}
		return 0
	}
	return render(s, a, explain, bufio.NewReader(os.Stdin))
}

func cmdREPL(cfg app.Config, explain bool) int {
	s, err := app.Open(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer s.Close()

	in := bufio.NewReader(os.Stdin)
	fmt.Printf("\n  %s\n  Ask a question, or /help.\n\n", s.StatusLine())
	if s.Store.CountPayments() == 0 {
		fmt.Print("  The local mirror is empty — run `finterminal sync` in another shell.\n")
	}
	for {
		fmt.Print("finterminal> ")
		line, err := in.ReadString('\n')
		if err != nil {
			fmt.Println()
			return 0
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case line == "/quit" || line == "/exit" || line == "exit":
			return 0
		case line == "/help":
			fmt.Printf("\n  /explain   how the last answer was produced\n"+
				"  /status    session and mirror state\n"+
				"  /caps      the supported capability surface\n"+
				"  /quit      leave\n\n%s\n", planner.CapabilityList())
			continue
		case line == "/status":
			fmt.Printf("\n  %s\n\n", s.StatusLine())
			continue
		case line == "/caps":
			fmt.Printf("\n  %s\n", planner.CapabilityList())
			continue
		case line == "/explain":
			if s.LastExplain == nil {
				fmt.Print("\n  Nothing answered yet.\n\n")
				continue
			}
			fmt.Printf("\n%s\n", s.LastExplain.String())
			continue
		}
		render(s, s.Ask(line), explain, in)
	}
}

// render prints an answer and, for a write, drives the confirmation.
func render(s *app.Session, a *app.Answer, explain bool, in *bufio.Reader) int {
	fmt.Println()
	switch {
	case a.WriteReq != nil:
		fmt.Print(a.WriteReq.Card)
		typed, _ := in.ReadString('\n')
		fmt.Println()
		fmt.Println(s.Confirm(a.WriteReq, typed))
		fmt.Println()
		return 0
	case a.Refusal != "":
		fmt.Printf("  %s\n\n", strings.TrimSpace(a.Refusal))
		return 2
	default:
		fmt.Println(a.Text)
		if a.Facts != nil {
			fmt.Printf("\n  ↳ %d %s over %d rows · %dms · /explain for detail\n\n",
				a.Facts.Queries, plural("query", "queries", a.Facts.Queries), a.Facts.Rows, a.Facts.QueryMS)
		}
		if explain && a.Explain != nil {
			fmt.Printf("%s\n", a.Explain.String())
		}
		return 0
	}
}

func plural(one, many string, n int) string {
	if n == 1 {
		return one
	}
	return many
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
