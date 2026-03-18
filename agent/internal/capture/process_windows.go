//go:build windows

package capture

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	advapi32                 = syscall.NewLazyDLL("advapi32.dll")
	procCreateProcessAsUserW = advapi32.NewProc("CreateProcessAsUserW")

	wtsapi32Token          = syscall.NewLazyDLL("wtsapi32.dll")
	procWTSQueryUserToken  = wtsapi32Token.NewProc("WTSQueryUserToken")

	procDuplicateTokenEx = advapi32.NewProc("DuplicateTokenEx")
)

const (
	tokenPrimary           = 1
	securityImpersonation  = 2
	createUnicodeEnv       = 0x00000400
	createNoWindow         = 0x08000000
)

// launchInSession starts a process in the specified Windows session using
// WTSQueryUserToken + CreateProcessAsUser. This allows a Session 0 service
// to launch ffmpeg with access to a user's desktop for screen capture.
func launchInSession(sessionID uint32, exePath string, args []string) (*os.Process, error) {
	// Get the user token for the target session.
	var userToken syscall.Handle
	ret, _, err := procWTSQueryUserToken.Call(
		uintptr(sessionID),
		uintptr(unsafe.Pointer(&userToken)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("WTSQueryUserToken for session %d: %w", sessionID, err)
	}
	defer syscall.CloseHandle(userToken)

	// Duplicate as a primary token so CreateProcessAsUser accepts it.
	var dupToken syscall.Handle
	ret, _, err = procDuplicateTokenEx.Call(
		uintptr(userToken),
		0x02000000, // MAXIMUM_ALLOWED
		0, // default security attributes
		securityImpersonation,
		tokenPrimary,
		uintptr(unsafe.Pointer(&dupToken)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("DuplicateTokenEx: %w", err)
	}
	defer syscall.CloseHandle(dupToken)

	// Build the command line.
	cmdLine := `"` + exePath + `"`
	if len(args) > 0 {
		cmdLine += " " + strings.Join(args, " ")
	}
	cmdLinePtr, err := syscall.UTF16PtrFromString(cmdLine)
	if err != nil {
		return nil, fmt.Errorf("UTF16PtrFromString: %w", err)
	}

	var si syscall.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	// Set the desktop to the user's session desktop.
	desktop, _ := syscall.UTF16PtrFromString(`WinSta0\Default`)
	si.Desktop = desktop

	var pi syscall.ProcessInformation

	ret, _, err = procCreateProcessAsUserW.Call(
		uintptr(dupToken),
		0, // lpApplicationName — embedded in cmdLine
		uintptr(unsafe.Pointer(cmdLinePtr)),
		0, // lpProcessAttributes
		0, // lpThreadAttributes
		0, // bInheritHandles = false
		createNoWindow|createUnicodeEnv,
		0, // lpEnvironment — inherit
		0, // lpCurrentDirectory — inherit
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("CreateProcessAsUser: %w", err)
	}

	// Close the thread handle; we only need the process handle.
	syscall.CloseHandle(pi.Thread)

	// Wrap in an os.Process for management.
	proc, err := os.FindProcess(int(pi.ProcessId))
	if err != nil {
		syscall.CloseHandle(pi.Process)
		return nil, fmt.Errorf("FindProcess: %w", err)
	}

	return proc, nil
}
