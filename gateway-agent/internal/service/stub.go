//go:build !windows

package service

import "fmt"

// RunService is a stub for non-Windows platforms. Windows service mode is only
// available when the agent is compiled for Windows.
func RunService(configPath string) error {
	return fmt.Errorf("Windows service mode not supported on this platform")
}

// IsWindowsService always returns false on non-Windows platforms.
func IsWindowsService() bool {
	return false
}
