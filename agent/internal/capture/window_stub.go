//go:build !windows

package capture

import (
	"context"
	"log/slog"
	"time"
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

// Run is a stub that blocks until the context is cancelled.
// Window tracking is only supported on Windows.
func (w *WindowTracker) Run(ctx context.Context) {
	slog.Warn("window tracking not supported on this platform")
	<-ctx.Done()
	slog.Info("window tracker stopped")
}
