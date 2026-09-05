//go:build windows

package ui

import (
	"os"
	"syscall"
	"unsafe"
)

const enableVirtualTerminalProcessing = 0x0004

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

// enableVT switches a Windows console into ANSI mode. Windows Terminal already
// is; the older conhost needs asking, and if it refuses we print plain text.
func enableVT(f *os.File) bool {
	h := syscall.Handle(f.Fd())
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return false
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return true
	}
	r, _, _ = procSetConsoleMode.Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing))
	return r != 0
}
