package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/p0rtal-4/gateway-agent/internal/config"
	"github.com/p0rtal-4/gateway-agent/internal/credentials"
	"github.com/p0rtal-4/gateway-agent/internal/recording"
)

// Manager is the core orchestration component that manages RDP bastion
// sessions, user pool allocation, RDS session monitoring, and timeout
// enforcement.
type Manager struct {
	cfg       *config.Config
	credStore *credentials.Store
	userPool  *UserPool
	sessions  map[string]*Session
	mu        sync.RWMutex
	stopCh    chan struct{}
	startTime time.Time
}

// NewManager creates a Manager, initialises the user pool, and starts the
// background monitoring goroutines.
func NewManager(cfg *config.Config, credStore *credentials.Store) (*Manager, error) {
	pool, err := NewUserPool(cfg)
	if err != nil {
		return nil, fmt.Errorf("session manager: create user pool: %w", err)
	}

	m := &Manager{
		cfg:       cfg,
		credStore: credStore,
		userPool:  pool,
		sessions:  make(map[string]*Session),
		stopCh:    make(chan struct{}),
		startTime: time.Now(),
	}

	go m.MonitorRDSSessions()
	go m.WatchTimeouts()

	return m, nil
}

// generateSessionID returns a session ID of the form "sess_" followed by 8
// random hex characters (4 random bytes).
func generateSessionID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	return "sess_" + hex.EncodeToString(b), nil
}

// CreateSession validates the request, acquires a user pool account, prepares
// the RDS environment, and returns the new session.
func (m *Manager) CreateSession(req *CreateSessionRequest) (*Session, error) {
	target, err := m.credStore.Get(req.TargetID)
	if err != nil {
		return nil, fmt.Errorf("invalid target: %w", err)
	}

	sessionID, err := generateSessionID()
	if err != nil {
		return nil, err
	}

	gwUser, gwPass, err := m.userPool.Acquire(sessionID)
	if err != nil {
		return nil, fmt.Errorf("acquire user: %w", err)
	}

	timeoutMinutes := req.TimeoutMinutes
	if timeoutMinutes <= 0 {
		timeoutMinutes = m.cfg.SessionTimeoutMinutes
	}

	now := time.Now()
	sess := &Session{
		ID:          sessionID,
		Status:      StatusPending,
		TargetID:    req.TargetID,
		TargetHost:  target.Host,
		TargetName:  target.Name,
		TargetUser:  target.Username,
		GatewayUser: gwUser,
		GatewayPass: gwPass,
		StartedAt:   now,
		ExpiresAt:   now.Add(time.Duration(timeoutMinutes) * time.Minute),
		Metadata:    req.Metadata,
	}

	if err := PrepareSession(sess, m.cfg, target); err != nil {
		m.userPool.Release(gwUser)
		return nil, fmt.Errorf("prepare session: %w", err)
	}

	m.mu.Lock()
	m.sessions[sessionID] = sess
	m.mu.Unlock()

	return sess, nil
}

// GetSession returns the session with the given ID or an error if not found.
func (m *Manager) GetSession(id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sess, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return sess, nil
}

// ListSessions returns all sessions, optionally filtered by status.
// An empty filter value matches everything.
func (m *Manager) ListSessions(status string) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []*Session
	for _, sess := range m.sessions {
		if status != "" && sess.Status != status {
			continue
		}
		out = append(out, sess)
	}
	return out
}

// TerminateSession forcefully ends an active session, optionally notifying the
// connected user first.
func (m *Manager) TerminateSession(id string, reason string, notifyUser bool) error {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("session %q not found", id)
	}

	// Only sessions that are still alive can be terminated.
	switch sess.Status {
	case StatusCompleted, StatusTerminated, StatusFailed:
		m.mu.Unlock()
		return fmt.Errorf("session %q is already in state %s", id, sess.Status)
	}
	m.mu.Unlock()

	if notifyUser && sess.RDSSessionID > 0 {
		if err := SendMessage(sess.RDSSessionID, reason); err != nil {
			log.Printf("session %s: failed to send termination message: %v", id, err)
		}
	}

	if err := TerminateRDSSession(sess); err != nil {
		log.Printf("session %s: failed to terminate RDS session: %v", id, err)
	}

	m.userPool.Release(sess.GatewayUser)

	m.mu.Lock()
	now := time.Now()
	sess.Status = StatusTerminated
	sess.EndedAt = &now
	m.mu.Unlock()

	return nil
}

// UpdateSessionStatus is called by the internal API callback from the
// PowerShell launch script to report status changes.
func (m *Manager) UpdateSessionStatus(id string, cb *StatusCallback) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[id]
	if !ok {
		return fmt.Errorf("session %q not found", id)
	}

	if cb.Status != "" {
		sess.Status = cb.Status
	}
	if cb.FFmpegPID != 0 {
		sess.FFmpegPID = cb.FFmpegPID
	}
	if cb.RecordingPath != "" {
		sess.RecordingPath = cb.RecordingPath
	}

	if cb.Status == StatusCompleted {
		// Release lock before calling CompleteSession, which takes its own lock.
		m.mu.Unlock()
		err := m.CompleteSession(id)
		m.mu.Lock() // re-acquire so the deferred Unlock is valid
		return err
	}

	return nil
}

// CompleteSession marks a session as completed, releases its user pool account,
// and kicks off background recording finalization.
func (m *Manager) CompleteSession(id string) error {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("session %q not found", id)
	}

	m.userPool.Release(sess.GatewayUser)

	now := time.Now()
	sess.Status = StatusCompleted
	sess.EndedAt = &now
	m.mu.Unlock()

	// Finalize the HLS playlist so it can be played back as a VOD.
	go func() {
		if err := recording.FinalizePlaylist(id, m.cfg); err != nil {
			log.Printf("session %s: playlist finalize failed: %v", id, err)
		}
	}()

	return nil
}

// MonitorRDSSessions polls qwinsta on a 5-second interval to detect RDS
// session state transitions (active, disconnected, reconnected).
func (m *Manager) MonitorRDSSessions() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkRDSSessions()
		}
	}
}

// checkRDSSessions runs qwinsta, parses the output, and updates managed
// sessions based on the observed RDS state.
func (m *Manager) checkRDSSessions() {
	cmd := exec.Command("qwinsta")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("monitor: qwinsta failed: %v", err)
		return
	}

	rdsSessions := parseQwinsta(string(output))

	// Build a lookup by username for quick matching.
	rdsByUser := make(map[string]RDSSession, len(rdsSessions))
	for _, rds := range rdsSessions {
		if rds.Username != "" {
			rdsByUser[strings.ToLower(rds.Username)] = rds
		}
	}

	type rotateInfo struct {
		sessionID   string
		gatewayUser string
	}
	var toRotate []rotateInfo

	m.mu.Lock()

	for _, sess := range m.sessions {
		// Only monitor sessions that are still alive.
		switch sess.Status {
		case StatusCompleted, StatusTerminated, StatusFailed:
			continue
		}

		rds, found := rdsByUser[strings.ToLower(sess.GatewayUser)]
		now := time.Now()

		switch {
		case found && strings.EqualFold(rds.State, "Active"):
			// RDS session is active.
			sess.RDSSessionID = rds.ID
			if sess.Status == StatusReady || sess.Status == StatusPending {
				// First connection — schedule token rotation.
				sess.Status = StatusActive
				sess.ConnectedAt = &now
				toRotate = append(toRotate, rotateInfo{sess.ID, sess.GatewayUser})
			} else if sess.Status == StatusDisconnected {
				// Reconnection.
				sess.Status = StatusActive
				sess.ConnectedAt = &now
				sess.DisconnectedAt = nil
			}

		case found && strings.EqualFold(rds.State, "Disc"):
			// RDS session is disconnected.
			sess.RDSSessionID = rds.ID
			if sess.Status == StatusActive {
				sess.Status = StatusDisconnected
				sess.DisconnectedAt = &now
			}

		case !found && sess.Status == StatusActive:
			// RDS session disappeared entirely — treat as disconnect.
			sess.Status = StatusDisconnected
			sess.DisconnectedAt = &now
		}
	}
	m.mu.Unlock()

	// Rotate passwords outside the lock — this invalidates the session
	// token so it cannot be reused for another connection.
	for _, ri := range toRotate {
		if err := m.userPool.RotatePassword(ri.gatewayUser); err != nil {
			log.Printf("session %s: failed to rotate token: %v", ri.sessionID, err)
			continue
		}
		m.mu.Lock()
		if sess, ok := m.sessions[ri.sessionID]; ok {
			sess.GatewayPass = ""
		}
		m.mu.Unlock()
		log.Printf("session %s: session token invalidated after connect", ri.sessionID)
	}
}

// WatchTimeouts checks for expired sessions and stale disconnected sessions
// on a 30-second interval.
func (m *Manager) WatchTimeouts() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.enforceTimeouts()
		}
	}
}

// enforceTimeouts terminates sessions that have exceeded their expiry or
// that have been disconnected longer than the reconnect grace period.
func (m *Manager) enforceTimeouts() {
	now := time.Now()
	graceDuration := time.Duration(m.cfg.ReconnectGraceMinutes) * time.Minute

	// Collect session IDs to terminate outside the read lock to avoid
	// deadlocking with TerminateSession / CompleteSession which take a
	// write lock.
	type action struct {
		id     string
		reason string
		term   bool // true = terminate, false = complete
	}

	var actions []action

	m.mu.RLock()
	for _, sess := range m.sessions {
		switch sess.Status {
		case StatusCompleted, StatusTerminated, StatusFailed:
			continue
		}

		if now.After(sess.ExpiresAt) {
			actions = append(actions, action{
				id:     sess.ID,
				reason: "Session expired",
				term:   true,
			})
			continue
		}

		if sess.Status == StatusDisconnected && sess.DisconnectedAt != nil {
			if now.Sub(*sess.DisconnectedAt) > graceDuration {
				actions = append(actions, action{
					id:     sess.ID,
					reason: "Reconnect grace period exceeded",
					term:   false,
				})
			}
		}
	}
	m.mu.RUnlock()

	for _, a := range actions {
		if a.term {
			if err := m.TerminateSession(a.id, a.reason, true); err != nil {
				log.Printf("timeout: failed to terminate session %s: %v", a.id, err)
			}
		} else {
			if err := m.CompleteSession(a.id); err != nil {
				log.Printf("timeout: failed to complete session %s: %v", a.id, err)
			}
		}
	}
}

// Shutdown stops all background goroutines and gracefully terminates every
// active session.
func (m *Manager) Shutdown() {
	close(m.stopCh)

	m.mu.RLock()
	ids := make([]string, 0, len(m.sessions))
	for id, sess := range m.sessions {
		switch sess.Status {
		case StatusCompleted, StatusTerminated, StatusFailed:
			continue
		}
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		if err := m.TerminateSession(id, "Gateway shutting down", true); err != nil {
			log.Printf("shutdown: failed to terminate session %s: %v", id, err)
		}
	}
}

// parseQwinsta parses the column-formatted output of the Windows "qwinsta"
// command and returns a slice of RDSSession entries.
//
// Expected format (variable whitespace):
//
//	SESSIONNAME  USERNAME  ID  STATE  TYPE  DEVICE
//	rdp-tcp#0    user1     2   Active
//	console      admin     1   Active
func parseQwinsta(output string) []RDSSession {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) <= 1 {
		return nil
	}

	var sessions []RDSSession
	// Skip header line.
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Strip leading '>' character that qwinsta uses to mark the current
		// session.
		line = strings.TrimPrefix(line, ">")
		line = strings.TrimSpace(line)

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		// Fields: SESSIONNAME USERNAME ID STATE [TYPE [DEVICE]]
		// The ID field is always numeric. Walk the fields to find it so we
		// can handle lines where USERNAME might be blank (qwinsta sometimes
		// omits it, shifting fields left).
		var sessionName, username, state string
		var id int
		var found bool

		for i, f := range fields {
			n, err := strconv.Atoi(f)
			if err == nil {
				// This is the ID column.
				id = n
				found = true
				if i >= 2 {
					sessionName = fields[0]
					username = fields[1]
				} else if i == 1 {
					sessionName = fields[0]
					username = ""
				}
				if i+1 < len(fields) {
					state = fields[i+1]
				}
				break
			}
		}

		if !found {
			continue
		}

		sessions = append(sessions, RDSSession{
			SessionName: sessionName,
			Username:    username,
			ID:          id,
			State:       state,
		})
	}
	return sessions
}

// FindSessionByPin returns the session whose GatewayPass matches the given
// PIN, or nil if no match is found. Only sessions with a non-empty password
// (i.e. not yet connected / rotated) are considered.
func (m *Manager) FindSessionByPin(pin string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, sess := range m.sessions {
		if sess.GatewayPass != "" && sess.GatewayPass == pin {
			return sess
		}
	}
	return nil
}

// StartTime returns the time the manager was created, used for uptime
// calculations in the health endpoint.
func (m *Manager) StartTime() time.Time {
	return m.startTime
}

// ActiveCount returns the number of sessions currently in an active state.
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, sess := range m.sessions {
		if sess.Status == StatusActive {
			count++
		}
	}
	return count
}

// AvailableUsers returns the number of user pool accounts not currently
// assigned to a session.
func (m *Manager) AvailableUsers() int {
	return m.userPool.Available()
}
