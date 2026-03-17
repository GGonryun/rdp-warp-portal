//go:build !windows

package capture

// getScreenResolution returns a dummy resolution on non-Windows platforms.
func getScreenResolution() (int, int) {
	return 1920, 1080
}
