//go:build !windows

package capture

import (
	"context"
	"log/slog"
	"time"
)

// ClipboardEvent represents a clipboard change event.
type ClipboardEvent struct {
	Timestamp time.Time
	Content   string
	Window    string
	PID       uint32
}

// ClipboardTracker monitors the clipboard for changes.
type ClipboardTracker struct {
	pollInterval time.Duration
	onEvent      func(ClipboardEvent)
}

// NewClipboardTracker creates a new clipboard tracker.
func NewClipboardTracker(pollInterval time.Duration, onEvent func(ClipboardEvent)) *ClipboardTracker {
	return &ClipboardTracker{
		pollInterval: pollInterval,
		onEvent:      onEvent,
	}
}

// Run is a stub that blocks until the context is cancelled.
func (c *ClipboardTracker) Run(ctx context.Context) {
	slog.Warn("clipboard tracking not supported on this platform")
	<-ctx.Done()
	slog.Info("clipboard tracker stopped")
}
