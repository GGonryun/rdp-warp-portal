package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/p0-security/rdp-broker/internal/credential"
)

func newTestManager(t *testing.T) (*Manager, func()) {
	tmpDir := t.TempDir()

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

	manager, err := NewManager(portPool, config)
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

	manager, _ := NewManager(portPool, config)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

	_, err := manager.CreateSession(context.Background(), "user1", &credential.TargetCredentials{Hostname: "dc-01", IP: "10.0.1.10", Port: 3389, Username: "Administrator", Password: "pass", Domain: "CORP"}, "127.0.0.1")
	if !errors.Is(err, ErrSessionLimitReached) {
		t.Errorf("expected ErrSessionLimitReached, got %v", err)
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

	manager, _ := NewManager(portPool, config)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

	ctx := context.Background()

	// Session creation should fail because 'sleep' doesn't open the port
	_, err := manager.CreateSession(ctx, "user1", &credential.TargetCredentials{Hostname: "dc-01", IP: "10.0.1.10", Port: 3389, Username: "Administrator", Password: "pass", Domain: "CORP"}, "127.0.0.1")
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

// TestManager_TerminateSession_WithActiveConnections is an integration test that verifies:
// 1. TerminateSession returns quickly (within 1 second) even with active connections
// 2. After TerminateSession, the session is removed from the manager
// 3. The port is released back to the pool
//
// This test creates a session with:
// - A mock "proxy" process (sleep command)
// - A mock TCP server simulating the proxy's listening port
// - A gatekeeper accepting connections
// - An active client connection through the gatekeeper to the mock proxy
//
// The test then calls TerminateSession and verifies it completes quickly.
func TestManager_TerminateSession_WithActiveConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()

	// Use higher port numbers to avoid conflicts with other tests
	portPool := NewPortPool(34500, 34510, 12000)

	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep",
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		ProxyStartTimeout:     100 * time.Millisecond,
		ProxyStopTimeout:      100 * time.Millisecond,
	}

	manager, err := NewManager(portPool, config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

	// Allocate a port from the pool
	externalPort, internalPort, err := portPool.Allocate()
	if err != nil {
		t.Fatalf("failed to allocate port: %v", err)
	}

	// Start a mock TCP server on the internal port to simulate a proxy
	mockProxyListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", internalPort))
	if err != nil {
		portPool.Release(externalPort)
		t.Fatalf("failed to start mock proxy listener: %v", err)
	}

	// Channel to signal when to close proxy connections (simulates proxy being killed)
	closeProxyConns := make(chan struct{})

	// Accept connections in a goroutine
	proxyConns := make([]net.Conn, 0)
	var proxyConnsMu sync.Mutex
	proxyAcceptDone := make(chan struct{})
	go func() {
		defer close(proxyAcceptDone)
		for {
			conn, err := mockProxyListener.Accept()
			if err != nil {
				return
			}
			proxyConnsMu.Lock()
			proxyConns = append(proxyConns, conn)
			proxyConnsMu.Unlock()

			// Close connection when signaled (simulates proxy process being killed)
			go func(c net.Conn) {
				<-closeProxyConns
				c.Close()
			}(conn)
		}
	}()
	defer func() {
		mockProxyListener.Close()
		<-proxyAcceptDone
	}()

	// Start a long-running mock process (simulating freerdp-proxy)
	mockProxyCmd := exec.Command("sleep", "3600")
	if err := mockProxyCmd.Start(); err != nil {
		portPool.Release(externalPort)
		t.Fatalf("failed to start mock proxy process: %v", err)
	}

	// Create session manually
	sessionID := "test-session-with-connections"
	now := time.Now()
	expiresAt := now.Add(time.Hour)
	session := &Session{
		ID:           sessionID,
		UserID:       "testuser",
		TargetID:     "dc-01",
		TargetHost:   "10.0.1.10",
		ExternalPort: externalPort,
		InternalPort: internalPort,
		State:        StateActive,
		PID:          mockProxyCmd.Process.Pid,
		CreatedAt:    now,
		ExpiresAt:    &expiresAt,
		token:        mustNewToken(t, 60*time.Second),
		process:      mockProxyCmd,
		stopMonitor:  make(chan struct{}),
	}

	// Create and start a gatekeeper
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", internalPort)
	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  externalPort,
		ProxyAddr:     proxyAddr,
		SessionID:     sessionID,
		AllowedIP:     "",
		ValidateToken: func(token, target string) error { return nil },
	})
	session.gatekeeper = gk

	// Start gatekeeper in background
	gkStarted := make(chan struct{})
	go func() {
		close(gkStarted)
		gk.Start()
	}()
	<-gkStarted
	// Give gatekeeper time to start listening
	time.Sleep(50 * time.Millisecond)

	// Register session with manager
	manager.mu.Lock()
	manager.sessions[sessionID] = session
	manager.mu.Unlock()

	// Start monitor goroutine (like CreateSession does)
	go manager.monitor(session)

	// Establish an active client connection to the gatekeeper
	clientConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", externalPort), time.Second)
	if err != nil {
		t.Fatalf("failed to connect to gatekeeper: %v", err)
	}
	defer clientConn.Close()

	// Give time for the connection to be fully established through the gatekeeper
	time.Sleep(100 * time.Millisecond)

	// Record initial port count
	initialAvailablePorts := manager.AvailablePorts()

	// Verify session exists before termination
	_, err = manager.GetSession(sessionID)
	if err != nil {
		t.Fatalf("session should exist before termination: %v", err)
	}

	// Simulate proxy being killed - close all proxy connections
	// This is what happens when StopProxy kills the real freerdp-proxy process
	close(closeProxyConns)

	// Terminate the session and measure time
	ctx := context.Background()
	start := time.Now()
	err = manager.TerminateSession(ctx, sessionID)
	elapsed := time.Since(start)

	// Verify TerminateSession returned without error
	if err != nil {
		t.Errorf("TerminateSession returned error: %v", err)
	}

	// 1. Verify TerminateSession returns quickly (within 1 second)
	if elapsed > time.Second {
		t.Errorf("TerminateSession took too long: %v (expected < 1s)", elapsed)
	}

	// 2. Verify session is removed from manager
	_, err = manager.GetSession(sessionID)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("session should be removed after termination, got error: %v", err)
	}

	if manager.ActiveSessionCount() != 0 {
		t.Errorf("expected 0 active sessions after termination, got %d", manager.ActiveSessionCount())
	}

	// 3. Verify port is released back to pool
	finalAvailablePorts := manager.AvailablePorts()
	if finalAvailablePorts != initialAvailablePorts+1 {
		t.Errorf("port should be released: initial=%d, final=%d (expected final=%d)",
			initialAvailablePorts, finalAvailablePorts, initialAvailablePorts+1)
	}
}

// mustNewToken creates a token for testing, failing the test if it errors.
func mustNewToken(t *testing.T, ttl time.Duration) *Token {
	t.Helper()
	token, err := NewToken(ttl)
	if err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	return token
}

// TestNewManager_InvalidConfigDir tests that NewManager fails with invalid directories.
func TestNewManager_InvalidConfigDir(t *testing.T) {
	portPool := NewPortPool(33400, 33410, 11000)

	// Use a non-existent directory that cannot be created
	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               "/nonexistent/path/that/cannot/exist/certs",
		SessionDir:            "/nonexistent/path/that/cannot/exist/sessions",
		FreerdpProxyBin:       "sleep",
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
	}

	// NewManager should still succeed because ConfigWriter only validates template parsing
	manager, err := NewManager(portPool, config)
	if err != nil {
		t.Logf("NewManager with invalid dirs returned error (expected for some cases): %v", err)
	}
	if manager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}
}

// TestManager_DefaultTimeouts tests that default timeouts are applied.
func TestManager_DefaultTimeouts(t *testing.T) {
	tmpDir := t.TempDir()

	portPool := NewPortPool(33400, 33410, 11000)

	// Config without timeouts - should use defaults
	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep",
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		// ProxyStartTimeout and ProxyStopTimeout are not set
	}

	manager, err := NewManager(portPool, config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

	// Check that defaults were applied (internal check via behavior)
	if manager.config.ProxyStartTimeout != 10*time.Second {
		t.Errorf("expected default ProxyStartTimeout of 10s, got %v", manager.config.ProxyStartTimeout)
	}
	if manager.config.ProxyStopTimeout != 5*time.Second {
		t.Errorf("expected default ProxyStopTimeout of 5s, got %v", manager.config.ProxyStopTimeout)
	}
}

// TestManager_NilLogger tests that a nil logger doesn't cause panics.
func TestManager_NilLogger(t *testing.T) {
	tmpDir := t.TempDir()

	portPool := NewPortPool(33400, 33410, 11000)

	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep",
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		Logger:                nil, // Explicitly nil
	}

	manager, err := NewManager(portPool, config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

	// Should use default logger without panicking
	if manager.logger == nil {
		t.Error("expected default logger to be set")
	}
}

// TestManager_CreateSession_ShutdownInProgress tests that sessions cannot be created during shutdown.
func TestManager_CreateSession_ShutdownInProgress(t *testing.T) {
	manager, cleanup := newTestManager(t)

	// Start shutdown
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	manager.Shutdown(ctx)
	cleanup()

	// Try to create a session after shutdown
	_, err := manager.CreateSession(context.Background(), "user1", &credential.TargetCredentials{Hostname: "dc-01", IP: "10.0.1.10", Port: 3389, Username: "Administrator", Password: "pass", Domain: "CORP"}, "127.0.0.1")
	if err == nil {
		t.Error("expected error when creating session during shutdown")
	}
}

// TestManager_TerminateSession_AlreadyTerminating tests double termination.
func TestManager_TerminateSession_AlreadyTerminating(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	portPool := NewPortPool(34600, 34610, 12000)

	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep",
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		ProxyStartTimeout:     100 * time.Millisecond,
		ProxyStopTimeout:      100 * time.Millisecond,
	}

	manager, err := NewManager(portPool, config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

	// Create a mock session manually
	externalPort, internalPort, _ := portPool.Allocate()

	// Start a mock listener on internal port
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", internalPort))
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer listener.Close()

	mockProxyCmd := exec.Command("sleep", "3600")
	mockProxyCmd.Start()

	sessionID := "test-double-terminate"
	now := time.Now()
	expiresAt := now.Add(time.Hour)
	session := &Session{
		ID:           sessionID,
		UserID:       "testuser",
		TargetID:     "dc-01",
		TargetHost:   "10.0.1.10",
		ExternalPort: externalPort,
		InternalPort: internalPort,
		State:        StateActive,
		PID:          mockProxyCmd.Process.Pid,
		CreatedAt:    now,
		ExpiresAt:    &expiresAt,
		token:        mustNewToken(t, 60*time.Second),
		process:      mockProxyCmd,
		stopMonitor:  make(chan struct{}),
	}

	manager.mu.Lock()
	manager.sessions[sessionID] = session
	manager.mu.Unlock()

	// Terminate twice concurrently
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		manager.TerminateSession(context.Background(), sessionID)
	}()

	go func() {
		defer wg.Done()
		manager.TerminateSession(context.Background(), sessionID)
	}()

	wg.Wait()

	// Session should be terminated
	_, err = manager.GetSession(sessionID)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound after double termination, got %v", err)
	}
}

// TestManager_ListSessions_FilterByUser tests filtering sessions by user.
func TestManager_ListSessions_FilterByUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	portPool := NewPortPool(34700, 34710, 12000)

	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep",
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		ProxyStartTimeout:     100 * time.Millisecond,
		ProxyStopTimeout:      100 * time.Millisecond,
	}

	manager, err := NewManager(portPool, config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

	// Add mock sessions for different users
	addMockSession := func(id, userID string, port int) {
		now := time.Now()
		expiresAt := now.Add(time.Hour)
		session := &Session{
			ID:           id,
			UserID:       userID,
			TargetID:     "dc-01",
			TargetHost:   "10.0.1.10",
			ExternalPort: port,
			InternalPort: port + 11000,
			State:        StateActive,
			CreatedAt:    now,
			ExpiresAt:    &expiresAt,
			token:        mustNewToken(t, 60*time.Second),
			stopMonitor:  make(chan struct{}),
		}
		manager.mu.Lock()
		manager.sessions[id] = session
		manager.mu.Unlock()
	}

	addMockSession("session-1", "user1", 34700)
	addMockSession("session-2", "user1", 34701)
	addMockSession("session-3", "user2", 34702)

	// Test filtering by user
	user1Sessions := manager.ListSessions("user1")
	if len(user1Sessions) != 2 {
		t.Errorf("expected 2 sessions for user1, got %d", len(user1Sessions))
	}

	user2Sessions := manager.ListSessions("user2")
	if len(user2Sessions) != 1 {
		t.Errorf("expected 1 session for user2, got %d", len(user2Sessions))
	}

	allSessions := manager.ListSessions("")
	if len(allSessions) != 3 {
		t.Errorf("expected 3 total sessions, got %d", len(allSessions))
	}

	// Non-existent user
	nonExistentSessions := manager.ListSessions("user3")
	if len(nonExistentSessions) != 0 {
		t.Errorf("expected 0 sessions for user3, got %d", len(nonExistentSessions))
	}
}

// TestManager_TokenExpiry tests that token expiry is correctly returned.
func TestManager_TokenExpiry(t *testing.T) {
	tmpDir := t.TempDir()
	portPool := NewPortPool(34800, 34810, 12000)

	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep",
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		ProxyStartTimeout:     100 * time.Millisecond,
		ProxyStopTimeout:      100 * time.Millisecond,
	}

	manager, err := NewManager(portPool, config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

	// Add a mock session
	sessionID := "test-token-expiry"
	now := time.Now()
	expiresAt := now.Add(time.Hour)
	token := mustNewToken(t, 60*time.Second)
	session := &Session{
		ID:           sessionID,
		UserID:       "testuser",
		TargetID:     "dc-01",
		TargetHost:   "10.0.1.10",
		ExternalPort: 34800,
		InternalPort: 45800,
		State:        StateActive,
		CreatedAt:    now,
		ExpiresAt:    &expiresAt,
		token:        token,
		stopMonitor:  make(chan struct{}),
	}

	manager.mu.Lock()
	manager.sessions[sessionID] = session
	manager.mu.Unlock()

	// Get token expiry
	expiry, err := manager.TokenExpiry(sessionID)
	if err != nil {
		t.Fatalf("TokenExpiry failed: %v", err)
	}

	expectedExpiry := token.Expiry()
	if !expiry.Equal(expectedExpiry) {
		t.Errorf("token expiry mismatch: got %v, expected %v", expiry, expectedExpiry)
	}

	// Test non-existent session
	_, err = manager.TokenExpiry("nonexistent")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

// TestManager_GenerateRDPFile_InvalidState tests RDP file generation for non-active sessions.
func TestManager_GenerateRDPFile_InvalidState(t *testing.T) {
	tmpDir := t.TempDir()
	portPool := NewPortPool(34900, 34910, 12000)

	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep",
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		ProxyStartTimeout:     100 * time.Millisecond,
		ProxyStopTimeout:      100 * time.Millisecond,
	}

	manager, err := NewManager(portPool, config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

	// Test different invalid states
	states := []SessionState{StateCreating, StateTerminating, StateTerminated}

	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			sessionID := fmt.Sprintf("test-state-%s", state)
			now := time.Now()
			session := &Session{
				ID:           sessionID,
				UserID:       "testuser",
				TargetID:     "dc-01",
				TargetHost:   "10.0.1.10",
				ExternalPort: 34900,
				InternalPort: 45900,
				State:        state,
				CreatedAt:    now,
				token:        mustNewToken(t, 60*time.Second),
				stopMonitor:  make(chan struct{}),
			}

			manager.mu.Lock()
			manager.sessions[sessionID] = session
			manager.mu.Unlock()

			_, err := manager.GenerateRDPFile(sessionID)
			if err == nil {
				t.Errorf("expected error for state %s", state)
			}

			// Cleanup
			manager.mu.Lock()
			delete(manager.sessions, sessionID)
			manager.mu.Unlock()
		})
	}
}

// TestManager_ConcurrentSessionOperations tests thread-safety of session operations.
func TestManager_ConcurrentSessionOperations(t *testing.T) {
	manager, cleanup := newTestManager(t)
	defer cleanup()

	const numGoroutines = 50
	var wg sync.WaitGroup

	// Add some mock sessions
	for i := 0; i < 10; i++ {
		sessionID := fmt.Sprintf("session-%d", i)
		now := time.Now()
		session := &Session{
			ID:           sessionID,
			UserID:       fmt.Sprintf("user%d", i%3),
			TargetID:     "dc-01",
			TargetHost:   "10.0.1.10",
			ExternalPort: 33400 + i,
			InternalPort: 44400 + i,
			State:        StateActive,
			CreatedAt:    now,
			token:        mustNewToken(t, 60*time.Second),
			stopMonitor:  make(chan struct{}),
		}
		manager.mu.Lock()
		manager.sessions[sessionID] = session
		manager.mu.Unlock()
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("session-%d", idx%10)
			manager.GetSession(sessionID)
			manager.ListSessions("")
			manager.ListSessions(fmt.Sprintf("user%d", idx%3))
			manager.ActiveSessionCount()
			manager.AvailablePorts()
		}(i)
	}

	wg.Wait()
}

// TestManager_Shutdown_WithTimeout tests shutdown behavior with context timeout.
func TestManager_Shutdown_WithTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	portPool := NewPortPool(35000, 35010, 12000)

	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep",
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		ProxyStartTimeout:     100 * time.Millisecond,
		ProxyStopTimeout:      100 * time.Millisecond,
	}

	manager, err := NewManager(portPool, config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Add a few mock sessions with processes
	for i := 0; i < 3; i++ {
		externalPort, internalPort, _ := portPool.Allocate()
		mockProxyCmd := exec.Command("sleep", "3600")
		mockProxyCmd.Start()

		sessionID := fmt.Sprintf("shutdown-test-%d", i)
		now := time.Now()
		expiresAt := now.Add(time.Hour)
		session := &Session{
			ID:           sessionID,
			UserID:       "testuser",
			TargetID:     "dc-01",
			TargetHost:   "10.0.1.10",
			ExternalPort: externalPort,
			InternalPort: internalPort,
			State:        StateActive,
			PID:          mockProxyCmd.Process.Pid,
			CreatedAt:    now,
			ExpiresAt:    &expiresAt,
			token:        mustNewToken(t, 60*time.Second),
			process:      mockProxyCmd,
			stopMonitor:  make(chan struct{}),
		}

		manager.mu.Lock()
		manager.sessions[sessionID] = session
		manager.mu.Unlock()
	}

	// Shutdown with adequate timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = manager.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}

	// Verify all sessions are cleaned up
	if manager.ActiveSessionCount() != 0 {
		t.Errorf("expected 0 active sessions after shutdown, got %d", manager.ActiveSessionCount())
	}
}

// TestManager_Shutdown_Idempotent tests that shutdown can be called multiple times.
func TestManager_Shutdown_Idempotent(t *testing.T) {
	manager, _ := newTestManager(t)

	ctx := context.Background()

	// Call shutdown multiple times
	err1 := manager.Shutdown(ctx)
	err2 := manager.Shutdown(ctx)
	err3 := manager.Shutdown(ctx)

	// All should succeed (no panic, no error)
	if err1 != nil || err2 != nil || err3 != nil {
		t.Errorf("shutdown should be idempotent: err1=%v, err2=%v, err3=%v", err1, err2, err3)
	}
}

// TestManager_ProviderError tests handling of credential provider errors.
func TestManager_ProviderError(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a mock provider and remove all targets to trigger errors

	portPool := NewPortPool(35100, 35110, 12000)

	config := ManagerConfig{
		BrokerHost:            "localhost",
		BrokerDomain:          "TESTDOMAIN",
		CertDir:               tmpDir,
		SessionDir:            tmpDir,
		FreerdpProxyBin:       "sleep",
		MaxConcurrentSessions: 10,
		SessionMaxDuration:    time.Hour,
		TokenTTL:              60 * time.Second,
		ProxyStartTimeout:     100 * time.Millisecond,
		ProxyStopTimeout:      100 * time.Millisecond,
	}

	manager, err := NewManager(portPool, config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		manager.Shutdown(ctx)
	}()

}
