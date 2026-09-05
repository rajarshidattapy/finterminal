// Package config loads local configuration that is deliberately not in the
// repository — chiefly the model API key.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadDotEnv reads KEY=VALUE pairs from the first file it finds and puts them
// in the environment. A variable already set in the real environment always
// wins, so `set OPENAI_API_KEY=... && finterminal` overrides the file rather
// than fighting it.
//
// Search order: ./.env, then ~/.razorpay/ai.env. Returns the file it used, or
// "" when there was none — having no .env is a supported state, not an error.
func LoadDotEnv() string {
	for _, path := range candidatePaths() {
		if applied, err := loadFile(path); err == nil && applied {
			return path
		}
	}
	return ""
}

func candidatePaths() []string {
	paths := []string{".env"}
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(wd, ".env"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".razorpay", "ai.env"))
	}
	return paths
}

func loadFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		key, value, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		if _, already := os.LookupEnv(key); already {
			continue
		}
		_ = os.Setenv(key, value)
	}
	return true, sc.Err()
}

// parseLine handles the shapes a .env actually shows up in: comments, blank
// lines, a leading `export`, quoted values, and CRLF endings from Windows
// editors.
func parseLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	key, value, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return key, value, true
}
