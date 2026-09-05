// Package eval is the eval harness: planning accuracy, numeric fidelity and
// safety. Set C is the one that blocks a release — any failure there is a bug
// in the write plane, not a tuning problem.
package eval

import (
	"fmt"
	"strings"
	"time"
)

type Case struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Pass   bool   `json:"pass"`
	Got    string `json:"got,omitempty"`
	Want   string `json:"want,omitempty"`
}

type Report struct {
	Set     string        `json:"set"`
	Cases   []Case        `json:"cases"`
	Elapsed time.Duration `json:"-"`
}

func (r *Report) add(name string, pass bool, want, got, detail string) {
	r.Cases = append(r.Cases, Case{Name: name, Pass: pass, Want: want, Got: got, Detail: detail})
}

func (r *Report) Passed() int {
	n := 0
	for _, c := range r.Cases {
		if c.Pass {
			n++
		}
	}
	return n
}

func (r *Report) Total() int { return len(r.Cases) }

func (r *Report) Rate() float64 {
	if len(r.Cases) == 0 {
		return 0
	}
	return float64(r.Passed()) * 100 / float64(len(r.Cases))
}

// Render prints the report, failures first and in full.
func (r *Report) Render(verbose bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n  Set %s — %d/%d passed (%.1f%%) in %s\n\n",
		r.Set, r.Passed(), r.Total(), r.Rate(), r.Elapsed.Round(time.Millisecond))
	for _, c := range r.Cases {
		if c.Pass && !verbose {
			continue
		}
		mark := "FAIL"
		if c.Pass {
			mark = "ok  "
		}
		fmt.Fprintf(&b, "    %s  %s\n", mark, c.Name)
		if !c.Pass {
			if c.Want != "" || c.Got != "" {
				fmt.Fprintf(&b, "          want %s · got %s\n", c.Want, c.Got)
			}
			if c.Detail != "" {
				fmt.Fprintf(&b, "          %s\n", c.Detail)
			}
		}
	}
	if r.Passed() == r.Total() && !verbose {
		fmt.Fprintf(&b, "    all %d cases passed\n", r.Total())
	}
	return b.String()
}
