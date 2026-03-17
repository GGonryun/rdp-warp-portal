//go:build windows

package capture

import (
	"context"
	"log/slog"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	procSetWindowsHookExW  = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx     = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procGetKeyNameTextW    = user32.NewProc("GetKeyNameTextW")
	procMapVirtualKeyW     = user32.NewProc("MapVirtualKeyW")
)

const (
	whKeyboardLL = 13
	whMouseLL    = 14
	wmKeyDown    = 0x0100
	wmSysKeyDown = 0x0104
	wmLButtonDown = 0x0201
	wmRButtonDown = 0x0204
	wmMButtonDown = 0x0207
)

// InputEvent represents a keyboard or mouse input event.
type InputEvent struct {
	Timestamp time.Time
	Type      string // "key_press", "mouse_click"
	Key       string // key name (for key_press)
	Button    string // "left", "right", "middle" (for mouse_click)
	X         int32  // screen X (for mouse_click)
	Y         int32  // screen Y (for mouse_click)
	Window    string // active window title at time of event
	PID       uint32 // PID of active window
}

type kbdLLHookStruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type msLLHookStruct struct {
	X           int32
	Y           int32
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// InputTracker captures keyboard and mouse events using low-level Windows hooks.
type InputTracker struct {
	onEvent     func(InputEvent)
	kbHook      uintptr
	mouseHook   uintptr
	cancel      context.CancelFunc
	mu          sync.Mutex
	stopped     chan struct{}
}

// NewInputTracker creates a new input tracker.
func NewInputTracker(onEvent func(InputEvent)) *InputTracker {
	return &InputTracker{
		onEvent: onEvent,
		stopped: make(chan struct{}),
	}
}

// global reference so the hook callbacks can reach the tracker.
var globalInputTracker *InputTracker

// Run installs low-level hooks and runs a message pump. Blocks until ctx is cancelled.
func (t *InputTracker) Run(ctx context.Context) {
	slog.Info("starting input tracker")
	ctx, t.cancel = context.WithCancel(ctx)

	globalInputTracker = t

	// Install keyboard hook.
	kbCallback := syscall.NewCallback(keyboardHookProc)
	kbHook, _, err := procSetWindowsHookExW.Call(whKeyboardLL, kbCallback, 0, 0)
	if kbHook == 0 {
		slog.Warn("failed to install keyboard hook", "error", err)
	} else {
		t.kbHook = kbHook
		slog.Info("keyboard hook installed")
	}

	// Install mouse hook.
	mouseCallback := syscall.NewCallback(mouseHookProc)
	mouseHook, _, err := procSetWindowsHookExW.Call(whMouseLL, mouseCallback, 0, 0)
	if mouseHook == 0 {
		slog.Warn("failed to install mouse hook", "error", err)
	} else {
		t.mouseHook = mouseHook
		slog.Info("mouse hook installed")
	}

	// Run message pump in a goroutine — hooks require it.
	go func() {
		defer close(t.stopped)
		var m msg
		for {
			ret, _, _ := procGetMessageW.Call(
				uintptr(unsafe.Pointer(&m)),
				0, 0, 0,
			)
			if ret == 0 || ret == ^uintptr(0) {
				return
			}

			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()

	<-ctx.Done()
	t.unhook()
	slog.Info("input tracker stopped")
}

func (t *InputTracker) unhook() {
	if t.kbHook != 0 {
		procUnhookWindowsHookEx.Call(t.kbHook)
		t.kbHook = 0
	}
	if t.mouseHook != 0 {
		procUnhookWindowsHookEx.Call(t.mouseHook)
		t.mouseHook = 0
	}
}

func activeWindowInfo() (string, uint32) {
	hwnd := getForegroundWindow()
	if hwnd == 0 {
		return "", 0
	}
	title := getWindowText(hwnd)
	var pid uint32
	getWindowThreadProcessId(hwnd, &pid)
	return title, pid
}

func getKeyName(vkCode, scanCode uint32) string {
	// Map virtual key to scan code if needed, then get key name.
	if scanCode == 0 {
		sc, _, _ := procMapVirtualKeyW.Call(uintptr(vkCode), 0)
		scanCode = uint32(sc)
	}

	// Build lParam for GetKeyNameText: scan code in bits 16-23, extended flag in bit 24.
	lParam := uintptr(scanCode) << 16

	buf := make([]uint16, 64)
	ret, _, _ := procGetKeyNameTextW.Call(lParam, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if ret == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:ret])
}

func keyboardHookProc(nCode int, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 && (wParam == wmKeyDown || wParam == wmSysKeyDown) {
		kb := (*kbdLLHookStruct)(unsafe.Pointer(lParam))
		keyName := getKeyName(kb.VkCode, kb.ScanCode)
		if keyName == "" {
			keyName = "Unknown"
		}

		title, pid := activeWindowInfo()

		if globalInputTracker != nil && globalInputTracker.onEvent != nil {
			globalInputTracker.onEvent(InputEvent{
				Timestamp: time.Now(),
				Type:      "key_press",
				Key:       keyName,
				Window:    title,
				PID:       pid,
			})
		}
	}

	ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}

func mouseHookProc(nCode int, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 {
		var button string
		switch wParam {
		case wmLButtonDown:
			button = "left"
		case wmRButtonDown:
			button = "right"
		case wmMButtonDown:
			button = "middle"
		}

		if button != "" {
			ms := (*msLLHookStruct)(unsafe.Pointer(lParam))
			title, pid := activeWindowInfo()

			if globalInputTracker != nil && globalInputTracker.onEvent != nil {
				globalInputTracker.onEvent(InputEvent{
					Timestamp: time.Now(),
					Type:      "mouse_click",
					Button:    button,
					X:         ms.X,
					Y:         ms.Y,
					Window:    title,
					PID:       pid,
				})
			}
		}
	}

	ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}
