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
	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
)

const (
	whMouseLL     = 14
	wmLButtonDown = 0x0201
	wmRButtonDown = 0x0204
	wmMButtonDown = 0x0207
)

// InputEvent represents a mouse input event.
type InputEvent struct {
	Timestamp time.Time
	Type      string // "mouse_click"
	Button    string // "left", "right", "middle"
	X         int32
	Y         int32
	Window    string
	PID       uint32
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

// InputTracker captures mouse click events using a low-level Windows hook.
type InputTracker struct {
	onEvent   func(InputEvent)
	mouseHook uintptr
	cancel    context.CancelFunc
	mu        sync.Mutex
	stopped   chan struct{}
}

// NewInputTracker creates a new input tracker.
func NewInputTracker(onEvent func(InputEvent)) *InputTracker {
	return &InputTracker{
		onEvent: onEvent,
		stopped: make(chan struct{}),
	}
}

// global reference so the hook callback can reach the tracker.
var globalInputTracker *InputTracker

// Run installs the mouse hook and runs a message pump. Blocks until ctx is cancelled.
func (t *InputTracker) Run(ctx context.Context) {
	slog.Info("starting input tracker (mouse clicks)")
	ctx, t.cancel = context.WithCancel(ctx)

	globalInputTracker = t

	// Install mouse hook.
	mouseCallback := syscall.NewCallback(mouseHookProc)
	mouseHook, _, err := procSetWindowsHookExW.Call(whMouseLL, mouseCallback, 0, 0)
	if mouseHook == 0 {
		slog.Warn("failed to install mouse hook", "error", err)
	} else {
		t.mouseHook = mouseHook
		slog.Info("mouse hook installed")
	}

	// Run message pump — hooks require it.
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
