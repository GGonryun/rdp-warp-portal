//go:build !windows

package main

import "fmt"

func isWindowsService() bool {
	return false
}

func runAsService() {}

func installService(_ string) error {
	return fmt.Errorf("service commands are only supported on Windows")
}

func uninstallService() error {
	return fmt.Errorf("service commands are only supported on Windows")
}

func reinstallService(_ string) error {
	return fmt.Errorf("service commands are only supported on Windows")
}

func startService() error {
	return fmt.Errorf("service commands are only supported on Windows")
}

func stopService() error {
	return fmt.Errorf("service commands are only supported on Windows")
}

func queryService() error {
	return fmt.Errorf("service commands are only supported on Windows")
}

func tailLogs() error {
	return fmt.Errorf("service commands are only supported on Windows")
}
