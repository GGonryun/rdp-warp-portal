//go:build windows

package capture

var (
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	smCxScreen = 0 // SM_CXSCREEN - primary screen width
	smCyScreen = 1 // SM_CYSCREEN - primary screen height
)

// getScreenResolution returns the current primary screen width and height.
func getScreenResolution() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(smCxScreen)
	h, _, _ := procGetSystemMetrics.Call(smCyScreen)
	return int(w), int(h)
}
