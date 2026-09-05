package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		in, key, val string
		ok           bool
	}{
		{"OPENAI_API_KEY=sk-abc", "OPENAI_API_KEY", "sk-abc", true},
		{"  OPENAI_API_KEY = sk-abc  ", "OPENAI_API_KEY", "sk-abc", true},
		{`export KEY="quoted value"`, "KEY", "quoted value", true},
		{"KEY='single'", "KEY", "single", true},
		{"KEY=sk-abc\r", "KEY", "sk-abc", true},
		{"# a comment", "", "", false},
		{"", "", "", false},
		{"NOEQUALS", "", "", false},
		{"=novalue", "", "", false},
	}
	for _, c := range cases {
		k, v, ok := parseLine(c.in)
		if ok != c.ok || k != c.key || v != c.val {
			t.Errorf("parseLine(%q) = (%q, %q, %v), want (%q, %q, %v)", c.in, k, v, ok, c.key, c.val, c.ok)
		}
	}
}

func TestRealEnvironmentWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("WINS_TEST=from_file\nOTHER_TEST=from_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WINS_TEST", "from_shell")
	if _, err := loadFile(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("WINS_TEST"); got != "from_shell" {
		t.Errorf("shell value overwritten: %q", got)
	}
	if got := os.Getenv("OTHER_TEST"); got != "from_file" {
		t.Errorf("file value not applied: %q", got)
	}
}
