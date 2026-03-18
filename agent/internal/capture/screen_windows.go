//go:build windows

package capture

import "syscall"

var (
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	smCXScreen = 0 // SM_CXSCREEN
	smCYScreen = 1 // SM_CYSCREEN
)

// getScreenResolution returns the current primary screen width and height.
func getScreenResolution() (int, int) {
	w, _, _ := syscall.SyscallN(procGetSystemMetrics.Addr(), smCXScreen)
	h, _, _ := syscall.SyscallN(procGetSystemMetrics.Addr(), smCYScreen)
	return int(w), int(h)
}
