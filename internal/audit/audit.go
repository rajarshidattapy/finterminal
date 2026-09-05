// Package audit is the append-only record of everything the tool did, with
// contacts, emails and card fragments redacted on the way in.
package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Entry struct {
	TS           time.Time      `json:"ts"`
	Utterance    string         `json:"utterance"`
	Kind         string         `json:"kind"`
	Capability   string         `json:"capability,omitempty"`
	PlanSource   string         `json:"plan_source,omitempty"`
	Plan         any            `json:"plan,omitempty"`
	Policy       any            `json:"policy,omitempty"`
	Confirmed    *bool          `json:"confirmed,omitempty"`
	ResponseID   string         `json:"response_id,omitempty"`
	Refusal      string         `json:"refusal,omitempty"`
	Rows         int            `json:"rows,omitempty"`
	Queries      int            `json:"queries,omitempty"`
	NarrationVia string         `json:"narration_via,omitempty"`
	LatencyMS    int64          `json:"latency_ms"`
	Extra        map[string]any `json:"extra,omitempty"`
}

type Log struct{ Path string }

// DefaultPath is ~/.razorpay/ai-audit.log.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "ai-audit.log"
	}
	return filepath.Join(home, ".razorpay", "ai-audit.log")
}

func Open(path string) *Log {
	if path == "" {
		path = DefaultPath()
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return &Log{Path: path}
}

var (
	reEmail = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.]+`)
	rePhone = regexp.MustCompile(`(\+?\d[\d\- ]{7,}\d)`)
	reCard  = regexp.MustCompile(`\b\d{12,19}\b`)
)

// Redact removes contact details before anything is written to disk.
func Redact(s string) string {
	s = reEmail.ReplaceAllStringFunc(s, func(m string) string {
		at := strings.Index(m, "@")
		return "***" + m[at:]
	})
	s = reCard.ReplaceAllStringFunc(s, func(m string) string { return "****" + m[len(m)-4:] })
	s = rePhone.ReplaceAllStringFunc(s, func(m string) string {
		d := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, m)
		if len(d) < 4 {
			return "****"
		}
		return "*******" + d[len(d)-4:]
	})
	return s
}

// Append writes one redacted JSONL record. Failure to audit is never silent,
// but it also never blocks the answer the user already saw.
func (l *Log) Append(e Entry) error {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	e.Utterance = Redact(e.Utterance)
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line := Redact(string(raw))
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// Tail returns the last n entries, oldest first.
func (l *Log) Tail(n int) ([]Entry, error) {
	f, err := os.Open(l.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var all []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err == nil {
			all = append(all, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}
