package app

import "os"

// Interactive reports whether stdin is a terminal. Writes are unavailable
// without one: there is no confirmation path in CI or cron, and v1 ships no
// token-based bypass.
func Interactive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
