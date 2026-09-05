//go:build !windows

package ui

import "os"

// enableVT is a no-op everywhere a terminal already speaks ANSI.
func enableVT(*os.File) bool { return true }
