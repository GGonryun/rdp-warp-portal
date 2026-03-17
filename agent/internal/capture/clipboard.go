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
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
)

const cfUnicodeText = 13

// ClipboardEvent represents a clipboard change event.
type ClipboardEvent struct {
	Timestamp time.Time
	Content   string // truncated clipboard text
	Window    string
	PID       uint32
}

// ClipboardTracker monitors the clipboard for changes by polling the sequence number.
type ClipboardTracker struct {
	pollInterval time.Duration
	maxTextLen   int
	onEvent      func(ClipboardEvent)
	lastSeq      uint32
}

// NewClipboardTracker creates a new clipboard tracker.
func NewClipboardTracker(pollInterval time.Duration, onEvent func(ClipboardEvent)) *ClipboardTracker {
	return &ClipboardTracker{
		pollInterval: pollInterval,
		maxTextLen:   512,
		onEvent:      onEvent,
	}
}

// Run polls the clipboard sequence number and emits events on changes.
func (c *ClipboardTracker) Run(ctx context.Context) {
	slog.Info("starting clipboard tracker", "poll_interval", c.pollInterval)

	// Get initial sequence number so we don't emit on startup.
	c.lastSeq = getClipboardSeq()

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("clipboard tracker stopped")
			return
		case <-ticker.C:
			seq := getClipboardSeq()
			if seq == c.lastSeq {
				continue
			}
			c.lastSeq = seq

			text := readClipboardText()
			if text == "" {
				continue
			}

			// Truncate long clipboard content.
			if len(text) > c.maxTextLen {
				text = text[:c.maxTextLen] + "..."
			}

			title, pid := activeWindowInfo()

			if c.onEvent != nil {
				c.onEvent(ClipboardEvent{
					Timestamp: time.Now(),
					Content:   text,
					Window:    title,
					PID:       pid,
				})
			}
		}
	}
}

func getClipboardSeq() uint32 {
	ret, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(ret)
}

func readClipboardText() string {
	ret, _, _ := procOpenClipboard.Call(0)
	if ret == 0 {
		return ""
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}

	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return ""
	}
	defer procGlobalUnlock.Call(h)

	// Read UTF-16 string from pointer.
	var utf16 []uint16
	for i := 0; ; i++ {
		ch := *(*uint16)(unsafe.Pointer(ptr + uintptr(i)*2))
		if ch == 0 {
			break
		}
		utf16 = append(utf16, ch)
		if i > 2048 {
			break // safety limit
		}
	}

	return syscall.UTF16ToString(utf16)
}
