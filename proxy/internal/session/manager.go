package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"os/exec"
	"sync"
	"time"

	"github.com/p0-security/rdp-broker/internal/credential"
)

// Manager errors.
var (
	ErrSessionNotFound       = errors.New("session not found")
	ErrSessionLimitReached   = errors.New("maximum concurrent sessions reached")
	ErrSessionAlreadyExists  = errors.New("session already exists")
	ErrProviderUnavailable   = errors.New("credential provider unavailable")
)

// SessionState represents the current state of a session.
type SessionState string

const (
	StateCreating    SessionState = "creating"
	StateActive      SessionState = "active"
	StateConnected   SessionState = "connected"
	StateTerminating SessionState = "terminating"
	StateTerminated  SessionState = "terminated"
)

// Session represents an active RDP broker session.
type Session struct {
	ID           string       `json:"session_id"`
	UserID       string       `json:"user_id"`
	TargetID     string       `json:"target_id"`
	Username     string       `json:"username"`              // Windows user account connected as
	TargetHost   string       `json:"target_host"`
	ExternalPort int          `json:"proxy_port"`
	InternalPort int          `json:"-"`
	ClientIP     string       `json:"client_ip,omitempty"` // IP that created the session
	State        SessionState `json:"state"`
	PID          int          `json:"pid,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	ConnectedAt  *time.Time   `json:"connected_at,omitempty"`
	ExpiresAt    *time.Time   `json:"expires_at,omitempty"`

	// Internal fields (not exposed via JSON)
	token       *Token
	process     *exec.Cmd
	gatekeeper  *Gatekeeper
	stopMonitor chan struct{}
	mu          sync.Mutex
}

// ManagerConfig holds configuration for the session manager.
type ManagerConfig struct {
	BrokerHost            string
	BrokerDomain          string
	CertDir               string
	SessionDir            string
	FreerdpProxyBin       string
	MaxConcurrentSessions int
	SessionMaxDuration    time.Duration
	TokenTTL              time.Duration
	ProxyStartTimeout     time.Duration
	ProxyStopTimeout      time.Duration
	Logger                *slog.Logger
	LogOutput             io.Writer
}

// Manager orchestrates session lifecycle.
type Manager struct {
	mu           sync.RWMutex
	sessions     map[string]*Session
	history      []*Session // terminated sessions kept for listing
	provider     credential.CredentialProvider
	portPool     *PortPool
	configWriter *ConfigWriter
	proxyManager *ProxyManager
	config       ManagerConfig
	logger       *slog.Logger
	shutdown     bool
	shutdownOnce sync.Once
}

// NewManager creates a new session manager.
func NewManager(
	provider credential.CredentialProvider,
	portPool *PortPool,
	config ManagerConfig,
) (*Manager, error) {
	configWriter, err := NewConfigWriter(config.CertDir, config.SessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create config writer: %w", err)
	}

	if config.ProxyStartTimeout == 0 {
		config.ProxyStartTimeout = 10 * time.Second
	}
	if config.ProxyStopTimeout == 0 {
		config.ProxyStopTimeout = 5 * time.Second
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Manager{
		sessions:     make(map[string]*Session),
		provider:     provider,
		portPool:     portPool,
		configWriter: configWriter,
		proxyManager: NewProxyManager(config.FreerdpProxyBin),
		config:       config,
		logger:       logger,
	}, nil
}

// CreateSession creates a new RDP session for the given user and target.
// username specifies which user account to connect as on the target.
// clientIP is used to restrict RDP connections to the IP that created the session.
func (m *Manager) CreateSession(ctx context.Context, userID, targetID, username, clientIP string) (*Session, error) {
	m.mu.Lock()
	if m.shutdown {
		m.mu.Unlock()
		return nil, errors.New("manager is shutting down")
	}

	// Check session limit
	if len(m.sessions) >= m.config.MaxConcurrentSessions {
		m.mu.Unlock()
		return nil, ErrSessionLimitReached
	}
	m.mu.Unlock()

	// Get credentials from provider
	creds, err := m.provider.GetTargetCredentials(ctx, targetID, username)
	if err != nil {
		if errors.Is(err, credential.ErrTargetNotFound) || errors.Is(err, credential.ErrUserNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	// Allocate port pair
	externalPort, internalPort, err := m.portPool.Allocate()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate ports: %w", err)
	}

	// Generate session ID and token
	sessionID, err := GenerateToken()
	if err != nil {
		m.portPool.Release(externalPort)
		return nil, fmt.Errorf("failed to generate session ID: %w", err)
	}

	token, err := NewToken(m.config.TokenTTL)
	if err != nil {
		m.portPool.Release(externalPort)
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	// Create session object
	now := time.Now()
	expiresAt := now.Add(m.config.SessionMaxDuration)
	session := &Session{
		ID:           sessionID,
		UserID:       userID,
		TargetID:     targetID,
		Username:     creds.Username,
		TargetHost:   creds.IP,
		ExternalPort: externalPort,
		InternalPort: internalPort,
		ClientIP:     clientIP,
		State:        StateCreating,
		CreatedAt:    now,
		ExpiresAt:    &expiresAt,
		token:        token,
		stopMonitor:  make(chan struct{}),
	}

	// Write proxy config
	configPath, err := m.configWriter.WriteConfig(sessionID, internalPort, creds)
	if err != nil {
		m.portPool.Release(externalPort)
		return nil, fmt.Errorf("failed to write proxy config: %w", err)
	}

	// Start proxy
	var stdout, stderr io.Writer
	if m.config.LogOutput != nil {
		stdout = NewProxyOutput(sessionID, m.config.LogOutput, "stdout: ")
		stderr = NewProxyOutput(sessionID, m.config.LogOutput, "stderr: ")
	}

	cmd, err := m.proxyManager.StartProxy(configPath, stdout, stderr)
	if err != nil {
		m.configWriter.CleanupSession(sessionID)
		m.portPool.Release(externalPort)
		return nil, fmt.Errorf("failed to start proxy: %w", err)
	}
	session.process = cmd
	session.PID = cmd.Process.Pid

	// Wait for proxy to be ready
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", internalPort)
	if err := m.proxyManager.WaitReady(ctx, proxyAddr, m.config.ProxyStartTimeout); err != nil {
		m.proxyManager.StopProxy(cmd, m.config.ProxyStopTimeout)
		m.configWriter.CleanupSession(sessionID)
		m.portPool.Release(externalPort)
		return nil, fmt.Errorf("proxy did not become ready: %w", err)
	}

	// Delete config file (credentials are now only in proxy memory)
	m.configWriter.DeleteConfig(sessionID)

	// Create token validator
	validateToken := func(tokenValue, target string) error {
		// Verify target matches
		if target != targetID {
			return ErrTokenMismatch
		}
		return session.token.Validate(tokenValue)
	}

	// Start gatekeeper
	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  externalPort,
		ProxyAddr:     proxyAddr,
		SessionID:     sessionID,
		AllowedIP:     clientIP, // Only allow connections from the IP that created the session
		ValidateToken: validateToken,
		OnConnected: func() {
			session.mu.Lock()
			if session.State == StateActive {
				session.State = StateConnected
				now := time.Now()
				session.ConnectedAt = &now
			}
			session.mu.Unlock()
			m.logger.Info("client connected to session",
				"session_id", sessionID,
				"user_id", userID,
				"target_id", targetID,
			)
		},
	})
	session.gatekeeper = gk

	// Start gatekeeper in background
	go func() {
		if err := gk.Start(); err != nil && !gk.IsClosed() {
			m.logger.Error("gatekeeper error",
				"session_id", sessionID,
				"error", err,
			)
		}
	}()

	// Register session
	m.mu.Lock()
	m.sessions[sessionID] = session
	session.State = StateActive
	m.mu.Unlock()

	// Start monitor goroutine
	go m.monitor(session)

	m.logger.Info("session created",
		"session_id", sessionID,
		"user_id", userID,
		"target_id", targetID,
		"external_port", externalPort,
		"pid", session.PID,
	)

	return session, nil
}

// TerminateSession terminates an active session.
func (m *Manager) TerminateSession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	session, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotFound
	}

	// Mark as terminating
	session.mu.Lock()
	if session.State == StateTerminating || session.State == StateTerminated {
		session.mu.Unlock()
		m.mu.Unlock()
		return nil // Already terminating/terminated
	}
	session.State = StateTerminating
	session.mu.Unlock()
	m.mu.Unlock()

	// Signal monitor to stop
	close(session.stopMonitor)

	// Stop gatekeeper
	if session.gatekeeper != nil {
		session.gatekeeper.Stop()
	}

	// Stop proxy
	if session.process != nil {
		m.proxyManager.StopProxy(session.process, m.config.ProxyStopTimeout)
	}

	// Release port
	m.portPool.Release(session.ExternalPort)

	// Cleanup session directory
	m.configWriter.CleanupSession(sessionID)

	// Update state, move to history, and remove from active map
	m.mu.Lock()
	session.mu.Lock()
	session.State = StateTerminated
	session.mu.Unlock()
	delete(m.sessions, sessionID)
	m.history = append(m.history, session)
	m.mu.Unlock()

	m.logger.Info("session terminated",
		"session_id", sessionID,
		"user_id", session.UserID,
		"target_id", session.TargetID,
	)

	return nil
}

// GetSession returns a session by ID.
func (m *Manager) GetSession(sessionID string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}

	return session, nil
}

// matchesUser reports whether a session's UserID matches the given filter.
// It matches on exact equality or, if the filter is an email address, on the
// local part (e.g. "golden.marmot" matches "golden.marmot@p0lab1.internal").
func matchesUser(sessionUserID, filter string) bool {
	if strings.EqualFold(sessionUserID, filter) {
		return true
	}
	if i := strings.Index(filter, "@"); i > 0 {
		localPart := filter[:i]
		if strings.EqualFold(sessionUserID, localPart) {
			return true
		}
	}
	return false
}

// ListSessions returns all sessions (active + terminated) for a user (or all if userID is empty).
func (m *Manager) ListSessions(userID string) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sessions []*Session
	// Active sessions
	for _, s := range m.sessions {
		if userID == "" || matchesUser(s.UserID, userID) {
			sessions = append(sessions, s)
		}
	}
	// Terminated sessions from history
	for _, s := range m.history {
		if userID == "" || matchesUser(s.UserID, userID) {
			sessions = append(sessions, s)
		}
	}

	return sessions
}

// GenerateRDPFile generates the .rdp file content for a session.
func (m *Manager) GenerateRDPFile(sessionID string) ([]byte, error) {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, ErrSessionNotFound
	}

	session.mu.Lock()
	if session.State != StateActive && session.State != StateConnected {
		session.mu.Unlock()
		return nil, fmt.Errorf("session is not active (state: %s)", session.State)
	}
	tokenValue := session.token.Value()
	session.mu.Unlock()

	params := RDPFileParams{
		BrokerHost: m.config.BrokerHost,
		Port:       session.ExternalPort,
		UserID:     session.UserID,
		TargetID:   session.TargetID,
		Token:      tokenValue,
		Domain:     m.config.BrokerDomain,
	}

	return GenerateRDPFile(params), nil
}

// Shutdown gracefully shuts down all sessions.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.shutdownOnce.Do(func() {
		m.mu.Lock()
		m.shutdown = true
		sessionIDs := make([]string, 0, len(m.sessions))
		for id := range m.sessions {
			sessionIDs = append(sessionIDs, id)
		}
		m.mu.Unlock()

		m.logger.Info("shutting down session manager",
			"active_sessions", len(sessionIDs),
		)

		// Terminate all sessions in parallel
		var wg sync.WaitGroup
		for _, id := range sessionIDs {
			wg.Add(1)
			go func(sessionID string) {
				defer wg.Done()
				m.TerminateSession(ctx, sessionID)
			}(id)
		}

		// Wait for all terminations with timeout
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-ctx.Done():
			m.logger.Warn("shutdown timed out, some sessions may not have terminated cleanly")
		}
	})

	return nil
}

// monitor watches a session for termination conditions.
func (m *Manager) monitor(session *Session) {
	// Wait for process exit
	processDone := make(chan struct{})
	go func() {
		if session.process != nil {
			session.process.Wait()
		}
		close(processDone)
	}()

	// Calculate max duration deadline
	var maxDurationTimer <-chan time.Time
	if session.ExpiresAt != nil {
		maxDurationTimer = time.After(time.Until(*session.ExpiresAt))
	}

	select {
	case <-session.stopMonitor:
		// Session is being terminated externally
		return

	case <-processDone:
		// Proxy process exited unexpectedly
		m.logger.Warn("proxy process exited unexpectedly",
			"session_id", session.ID,
			"user_id", session.UserID,
			"target_id", session.TargetID,
		)

	case <-maxDurationTimer:
		// Max duration reached
		m.logger.Info("session max duration reached",
			"session_id", session.ID,
			"user_id", session.UserID,
			"target_id", session.TargetID,
		)
	}

	// Terminate the session
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m.TerminateSession(ctx, session.ID)
}

// ActiveSessionCount returns the number of active sessions.
func (m *Manager) ActiveSessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// AvailablePorts returns the number of available ports.
func (m *Manager) AvailablePorts() int {
	return m.portPool.Available()
}

// TokenExpiry returns the token expiry time for a session.
func (m *Manager) TokenExpiry(sessionID string) (time.Time, error) {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return time.Time{}, ErrSessionNotFound
	}

	return session.token.Expiry(), nil
}
