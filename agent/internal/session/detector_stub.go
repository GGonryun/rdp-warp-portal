//go:build !windows

package session

import (
	"context"
	"log/slog"
	"time"
)

// SessionInfo holds information about an RDP session.
type SessionInfo struct {
	ID       uint32
	User     string
	State    uint32
	ClientIP string
}

// Detector polls for RDP session logon and logoff events.
type Detector struct {
	pollInterval time.Duration
	onLogon      func(SessionInfo)
	onLogoff     func(SessionInfo)
}

// NewDetector creates a new session detector.
func NewDetector(pollInterval time.Duration, onLogon, onLogoff func(SessionInfo)) *Detector {
	return &Detector{
		pollInterval: pollInterval,
		onLogon:      onLogon,
		onLogoff:     onLogoff,
	}
}

// Run is a stub that logs a warning and blocks until the context is cancelled.
// Session detection is only supported on Windows.
func (d *Detector) Run(ctx context.Context) {
	slog.Warn("session detection not supported on this platform")
	<-ctx.Done()
	slog.Info("session detector stopped")
}
