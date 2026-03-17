//go:build windows

package capture

import (
	"context"
	"log/slog"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                     = syscall.NewLazyDLL("user32.dll")
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	procGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW         = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

// WindowEvent represents a window focus change event.
type WindowEvent struct {
	Timestamp time.Time
	Title     string
	Process   string
	PID       uint32
}

// WindowTracker monitors foreground window changes.
type WindowTracker struct {
	pollInterval time.Duration
	onEvent      func(WindowEvent)
	lastTitle    string
}

// NewWindowTracker creates a new window tracker.
func NewWindowTracker(pollInterval time.Duration, onEvent func(WindowEvent)) *WindowTracker {
	return &WindowTracker{
		pollInterval: pollInterval,
		onEvent:      onEvent,
	}
}

// Run polls for foreground window changes and emits events when the title changes.
// It blocks until the context is cancelled.
func (w *WindowTracker) Run(ctx context.Context) {
	slog.Info("starting window tracker", "poll_interval", w.pollInterval)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("window tracker stopped")
			return
		case <-ticker.C:
			hwnd := getForegroundWindow()
			if hwnd == 0 {
				continue
			}

			title := getWindowText(hwnd)
			if title == w.lastTitle {
				continue
			}

			w.lastTitle = title

			var pid uint32
			getWindowThreadProcessId(hwnd, &pid)

			evt := WindowEvent{
				Timestamp: time.Now(),
				Title:     title,
				PID:       pid,
			}

			slog.Debug("window focus changed", "title", title, "pid", pid)

			if w.onEvent != nil {
				w.onEvent(evt)
			}
		}
	}
}

func getForegroundWindow() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	return hwnd
}

func getWindowText(hwnd uintptr) string {
	buf := make([]uint16, 256)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func getWindowThreadProcessId(hwnd uintptr, pid *uint32) {
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(pid)))
}
