//go:build !windows

package capture

// getScreenResolution is a no-op on non-Windows platforms.
// Returns (0, 0) so the monitor loop never triggers a restart.
func getScreenResolution() (int, int) {
	return 0, 0
}
