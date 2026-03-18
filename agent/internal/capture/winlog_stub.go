//go:build !windows

package capture

import (
	"context"
	"log/slog"
	"time"
)

// WinLogEvent represents a captured Windows Event Log entry.
type WinLogEvent struct {
	Timestamp   time.Time
	EventID     int
	Log         string
	Source      string
	Message     string
	User        string
	Level       string
	ScriptBlock string
}

// WinLogCapture captures Windows Event Log entries.
type WinLogCapture struct {
	onEvent func(WinLogEvent)
}

// NewWinLogCapture creates a new Windows Event Log capture.
func NewWinLogCapture(onEvent func(WinLogEvent)) *WinLogCapture {
	return &WinLogCapture{onEvent: onEvent}
}

// Start is a stub on non-Windows platforms.
func (w *WinLogCapture) Start(_ context.Context) error {
	slog.Warn("windows event log capture not supported on this platform")
	return nil
}

// Stop is a no-op on non-Windows platforms.
func (w *WinLogCapture) Stop() error {
	return nil
}
