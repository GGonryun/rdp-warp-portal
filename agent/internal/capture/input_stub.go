//go:build !windows

package capture

import (
	"context"
	"log/slog"
	"time"
)

// InputEvent represents a mouse input event.
type InputEvent struct {
	Timestamp time.Time
	Type      string
	Button    string
	X         int32
	Y         int32
	Window    string
	PID       uint32
}

// InputTracker captures mouse click events.
type InputTracker struct {
	onEvent func(InputEvent)
}

// NewInputTracker creates a new input tracker.
func NewInputTracker(onEvent func(InputEvent)) *InputTracker {
	return &InputTracker{onEvent: onEvent}
}

// Run is a stub that blocks until the context is cancelled.
func (t *InputTracker) Run(ctx context.Context) {
	slog.Warn("input tracking not supported on this platform")
	<-ctx.Done()
	slog.Info("input tracker stopped")
}
