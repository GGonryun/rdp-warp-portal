//go:build !windows

package main

import "fmt"

func isWindowsService() bool {
	return false
}

func runAsService() {}

func installService(_ string) error {
	return fmt.Errorf("windows service install is only supported on Windows")
}

func uninstallService() error {
	return fmt.Errorf("windows service uninstall is only supported on Windows")
}
