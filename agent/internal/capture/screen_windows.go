//go:build windows

package capture

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

var (
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")

	wtsapi32Screen                    = syscall.NewLazyDLL("wtsapi32.dll")
	procWTSQuerySessionInformationW   = wtsapi32Screen.NewProc("WTSQuerySessionInformationW")
	procWTSFreeMemory                 = wtsapi32Screen.NewProc("WTSFreeMemory")
)

const (
	smCXScreen = 0 // SM_CXSCREEN
	smCYScreen = 1 // SM_CYSCREEN

	wtsSessionInfoClass = 24 // WTSSessionInfo — returns WTSINFOW
)

// WTSINFOW contains the horizontal and vertical resolution at offsets 56 and 60.
// Full struct is large; we only read the fields we need.
const (
	wtsinfoHResOffset = 56
	wtsinfoVResOffset = 60
)

// getScreenResolution returns the current primary screen width and height.
// This only works correctly when called from the same session.
func getScreenResolution() (int, int) {
	w, _, _ := syscall.SyscallN(procGetSystemMetrics.Addr(), smCXScreen)
	h, _, _ := syscall.SyscallN(procGetSystemMetrics.Addr(), smCYScreen)
	return int(w), int(h)
}

// getSessionResolution queries the desktop resolution for a specific Windows session
// using WTSQuerySessionInformation. Returns (0,0) on failure.
func getSessionResolution(sessionID uint32) (int, int) {
	var buffer uintptr
	var bytesReturned uint32

	ret, _, _ := procWTSQuerySessionInformationW.Call(
		0, // WTS_CURRENT_SERVER_HANDLE
		uintptr(sessionID),
		wtsSessionInfoClass,
		uintptr(unsafe.Pointer(&buffer)),
		uintptr(unsafe.Pointer(&bytesReturned)),
	)
	if ret == 0 {
		return 0, 0
	}
	defer procWTSFreeMemory.Call(buffer)

	if bytesReturned < uint32(wtsinfoVResOffset+4) {
		return 0, 0
	}

	data := unsafe.Slice((*byte)(unsafe.Pointer(buffer)), bytesReturned)
	hRes := int(binary.LittleEndian.Uint32(data[wtsinfoHResOffset:]))
	vRes := int(binary.LittleEndian.Uint32(data[wtsinfoVResOffset:]))

	return hRes, vRes
}
