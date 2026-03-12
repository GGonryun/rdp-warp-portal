package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/p0-security/rdp-broker/internal/credential"
)

func newTestManager(t *testing.T) (*Manager, func()) {
	tmpDir := t.TempDir()

	provider := credential.NewMockProvider()
	portPool := NewPortPool(33400, 33410, 11000)

	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep", // Use sleep as a mock proxy
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		ProxyStartTimeout:     100 * time.Millisecond, // Short timeout for tests
		ProxyStopTimeout:      100 * time.Millisecond,
	}

	manager, err := NewManager(provider, portPool, config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}

	return manager, cleanup
}

func TestNewManager(t *testing.T) {
	manager, cleanup := newTestManager(t)
	defer cleanup()

	if manager == nil {
		t.Fatal("NewManager returned nil")
	}

	if manager.ActiveSessionCount() != 0 {
		t.Errorf("expected 0 active sessions, got %d", manager.ActiveSessionCount())
	}

	if manager.AvailablePorts() != 11 {
		t.Errorf("expected 11 available ports, got %d", manager.AvailablePorts())
	}
}

func TestManager_GetSession_NotFound(t *testing.T) {
	manager, cleanup := newTestManager(t)
	defer cleanup()

	_, err := manager.GetSession("nonexistent")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestManager_TerminateSession_NotFound(t *testing.T) {
	manager, cleanup := newTestManager(t)
	defer cleanup()

	err := manager.TerminateSession(context.Background(), "nonexistent")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestManager_ListSessions_Empty(t *testing.T) {
	manager, cleanup := newTestManager(t)
	defer cleanup()

	sessions := manager.ListSessions("")
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestManager_Shutdown_Empty(t *testing.T) {
	manager, cleanup := newTestManager(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := manager.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}
}

func TestManager_GenerateRDPFile_NotFound(t *testing.T) {
	manager, cleanup := newTestManager(t)
	defer cleanup()

	_, err := manager.GenerateRDPFile("nonexistent")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestManager_SessionLimitReached(t *testing.T) {
	tmpDir := t.TempDir()

	provider := credential.NewMockProvider()
	portPool := NewPortPool(33400, 33400, 11000) // Only 1 port available

	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep",
		MaxConcurrentSessions: 0, // No sessions allowed
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		ProxyStartTimeout:     100 * time.Millisecond,
		ProxyStopTimeout:      100 * time.Millisecond,
	}

	manager, _ := NewManager(provider, portPool, config)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

	_, err := manager.CreateSession(context.Background(), "user1", "dc-01", "127.0.0.1")
	if !errors.Is(err, ErrSessionLimitReached) {
		t.Errorf("expected ErrSessionLimitReached, got %v", err)
	}
}

func TestManager_TargetNotFound(t *testing.T) {
	manager, cleanup := newTestManager(t)
	defer cleanup()

	_, err := manager.CreateSession(context.Background(), "user1", "nonexistent-target", "127.0.0.1")
	if !errors.Is(err, credential.ErrTargetNotFound) {
		t.Errorf("expected ErrTargetNotFound, got %v", err)
	}
}

func TestManager_ActiveSessionCount(t *testing.T) {
	manager, cleanup := newTestManager(t)
	defer cleanup()

	if manager.ActiveSessionCount() != 0 {
		t.Errorf("expected 0, got %d", manager.ActiveSessionCount())
	}
}

func TestManager_AvailablePorts(t *testing.T) {
	manager, cleanup := newTestManager(t)
	defer cleanup()

	initial := manager.AvailablePorts()
	if initial != 11 {
		t.Errorf("expected 11, got %d", initial)
	}
}

// Integration test that requires 'sleep' command (available on most systems)
func TestManager_CreateAndTerminateSession_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()

	provider := credential.NewMockProvider()
	portPool := NewPortPool(33400, 33410, 11000)

	// Use 'nc -l' as a mock server that listens on a port
	// But for simplicity, we'll just check that session creation
	// handles the "proxy not ready" case gracefully
	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep", // sleep doesn't listen on a port
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		ProxyStartTimeout:     200 * time.Millisecond, // Short timeout
		ProxyStopTimeout:      100 * time.Millisecond,
	}

	manager, _ := NewManager(provider, portPool, config)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

	ctx := context.Background()

	// Session creation should fail because 'sleep' doesn't open the port
	_, err := manager.CreateSession(ctx, "user1", "dc-01", "127.0.0.1")
	if err == nil {
		t.Error("expected error because proxy doesn't listen on port")
	}

	// Port should be released after failure
	if manager.AvailablePorts() != 11 {
		t.Errorf("expected 11 available ports after failed creation, got %d", manager.AvailablePorts())
	}
}

func TestSessionState(t *testing.T) {
	states := []SessionState{
		StateCreating,
		StateActive,
		StateConnected,
		StateTerminating,
		StateTerminated,
	}

	for _, state := range states {
		if state == "" {
			t.Errorf("state should not be empty")
		}
	}
}

func TestSession_Fields(t *testing.T) {
	now := time.Now()
	expires := now.Add(time.Hour)
	connected := now.Add(time.Minute)

	session := &Session{
		ID:           "test-session-id",
		UserID:       "john.doe",
		TargetID:     "dc-01",
		TargetHost:   "10.0.1.10",
		ExternalPort: 33400,
		InternalPort: 44400,
		State:        StateActive,
		PID:          12345,
		CreatedAt:    now,
		ConnectedAt:  &connected,
		ExpiresAt:    &expires,
	}

	if session.ID != "test-session-id" {
		t.Errorf("ID mismatch")
	}
	if session.UserID != "john.doe" {
		t.Errorf("UserID mismatch")
	}
	if session.TargetID != "dc-01" {
		t.Errorf("TargetID mismatch")
	}
	if session.ExternalPort != 33400 {
		t.Errorf("ExternalPort mismatch")
	}
	if session.State != StateActive {
		t.Errorf("State mismatch")
	}
}
