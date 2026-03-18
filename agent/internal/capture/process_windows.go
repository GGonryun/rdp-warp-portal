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
	procDuplicateTokenEx     = advapi32.NewProc("DuplicateTokenEx")

	wtsapi32Token         = syscall.NewLazyDLL("wtsapi32.dll")
	procWTSQueryUserToken = wtsapi32Token.NewProc("WTSQueryUserToken")

	userenv32                   = syscall.NewLazyDLL("userenv.dll")
	procCreateEnvironmentBlock  = userenv32.NewProc("CreateEnvironmentBlock")
	procDestroyEnvironmentBlock = userenv32.NewProc("DestroyEnvironmentBlock")

	kernel32Proc             = syscall.NewLazyDLL("kernel32.dll")
	procSetHandleInformation = kernel32Proc.NewProc("SetHandleInformation")
)

const (
	tokenPrimary          = 1
	securityImpersonation = 2
	createUnicodeEnv      = 0x00000400
	createNoWindow        = 0x08000000
	handleFlagInherit     = 0x00000001
)

// launchInSession starts a process in the specified Windows session using
// WTSQueryUserToken + CreateProcessAsUser. This allows a Session 0 service
// to launch ffmpeg with access to a user's desktop for screen capture.
//
// Returns the PID and a writable stdin pipe. Writing "q" to the pipe
// triggers ffmpeg's graceful shutdown (finishes current segment, then exits).
//
// stderrPath, if non-empty, redirects the process's stderr to that file for debugging.
func launchInSession(sessionID uint32, exePath string, args []string, stderrPath string) (int, *os.File, error) {
	// Get the user token for the target session.
	var userToken syscall.Handle
	ret, _, err := procWTSQueryUserToken.Call(
		uintptr(sessionID),
		uintptr(unsafe.Pointer(&userToken)),
	)
	if ret == 0 {
		return 0, nil, fmt.Errorf("WTSQueryUserToken for session %d: %w", sessionID, err)
	}
	defer syscall.CloseHandle(userToken)

	// Duplicate as a primary token so CreateProcessAsUser accepts it.
	var dupToken syscall.Handle
	ret, _, err = procDuplicateTokenEx.Call(
		uintptr(userToken),
		0x02000000, // MAXIMUM_ALLOWED
		0,          // default security attributes
		securityImpersonation,
		tokenPrimary,
		uintptr(unsafe.Pointer(&dupToken)),
	)
	if ret == 0 {
		return 0, nil, fmt.Errorf("DuplicateTokenEx: %w", err)
	}
	defer syscall.CloseHandle(dupToken)

	// Create the user's environment block so ffmpeg gets the correct
	// session environment (display settings, temp dirs, etc.).
	var envBlock uintptr
	ret, _, err = procCreateEnvironmentBlock.Call(
		uintptr(unsafe.Pointer(&envBlock)),
		uintptr(dupToken),
		0, // don't inherit current process env
	)
	if ret == 0 {
		return 0, nil, fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	defer procDestroyEnvironmentBlock.Call(envBlock)

	// Create an anonymous pipe for stdin so we can send 'q' to ffmpeg
	// for graceful shutdown. os.Pipe() creates a pair of connected files.
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		return 0, nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	// Mark the read end as inheritable so the child process can use it.
	ret, _, err = procSetHandleInformation.Call(
		pipeR.Fd(),
		handleFlagInherit,
		handleFlagInherit,
	)
	if ret == 0 {
		pipeR.Close()
		pipeW.Close()
		return 0, nil, fmt.Errorf("SetHandleInformation: %w", err)
	}

	// Build the command line.
	cmdLine := `"` + exePath + `"`
	if len(args) > 0 {
		cmdLine += " " + strings.Join(args, " ")
	}
	cmdLinePtr, err := syscall.UTF16PtrFromString(cmdLine)
	if err != nil {
		pipeR.Close()
		pipeW.Close()
		return 0, nil, fmt.Errorf("UTF16PtrFromString: %w", err)
	}

	var si syscall.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	// Set the desktop to the user's session desktop.
	desktop, _ := syscall.UTF16PtrFromString(`WinSta0\Default`)
	si.Desktop = desktop

	// Set stdin to the read end of the pipe.
	si.StdInput = syscall.Handle(pipeR.Fd())
	si.Flags |= syscall.STARTF_USESTDHANDLES

	// Optionally redirect stderr to a file for debugging.
	if stderrPath != "" {
		f, ferr := os.Create(stderrPath)
		if ferr == nil {
			si.StdErr = syscall.Handle(f.Fd())
			defer f.Close()
		}
	}

	var pi syscall.ProcessInformation

	ret, _, err = procCreateProcessAsUserW.Call(
		uintptr(dupToken),
		0, // lpApplicationName — embedded in cmdLine
		uintptr(unsafe.Pointer(cmdLinePtr)),
		0, // lpProcessAttributes
		0, // lpThreadAttributes
		1, // bInheritHandles = true (needed for stdin pipe)
		createNoWindow|createUnicodeEnv,
		envBlock, // user's environment
		0,        // lpCurrentDirectory — inherit
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)

	// Close the read end — the child has it now, parent only needs the write end.
	pipeR.Close()

	if ret == 0 {
		pipeW.Close()
		return 0, nil, fmt.Errorf("CreateProcessAsUser: %w", err)
	}

	syscall.CloseHandle(pi.Thread)
	syscall.CloseHandle(pi.Process)

	return int(pi.ProcessId), pipeW, nil
}
