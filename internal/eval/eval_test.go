package eval_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rajarshidattapy/finterminal/internal/app"
	"github.com/rajarshidattapy/finterminal/internal/eval"
	"github.com/rajarshidattapy/finterminal/internal/fixtures"
	"github.com/rajarshidattapy/finterminal/internal/store"
)

// TestEvalSets runs the whole harness under `go test`, so CI enforces the same
// gates the CLI reports. Set C is the release blocker.
func TestEvalSets(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "cache.db")
	now := time.Now()

	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := fixtures.Generate(st, now, 90); err != nil {
		t.Fatalf("seed fixtures: %v", err)
	}
	st.Close()

	cfg := app.Config{DBPath: db, AuditPath: filepath.Join(dir, "audit.log")}
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

	s, err := app.Open(cfg)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer s.Close()

	t.Run("A_planning_accuracy", func(t *testing.T) {
		r := eval.RunA(s.Planner, now)
		if r.Rate() < 90 {
			t.Errorf("planning accuracy %.1f%% is below the 90%% target\n%s", r.Rate(), r.Render(false))
		}
	})

	t.Run("B_numeric_fidelity", func(t *testing.T) {
		r := eval.RunB(s.Store, now)
		if r.Passed() != r.Total() {
			t.Errorf("numeric fidelity: %d/%d\n%s", r.Passed(), r.Total(), r.Render(false))
		}
	})

	t.Run("C_safety", func(t *testing.T) {
		r := eval.RunC(newSession, now)
		if r.Passed() != r.Total() {
			t.Errorf("safety: %d/%d — this blocks release\n%s", r.Passed(), r.Total(), r.Render(false))
		}
	})
}
