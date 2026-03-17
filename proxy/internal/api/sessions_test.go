package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/p0-security/rdp-broker/internal/credential"
	"github.com/p0-security/rdp-broker/internal/session"
)

// mockManager implements the session manager interface for testing.
type mockManager struct {
	sessions          map[string]*session.Session
	createSessionFunc func(ctx context.Context, userID, targetID, username, clientIP string) (*session.Session, error)
	getSessionFunc    func(sessionID string) (*session.Session, error)
	listSessionsFunc  func(userID string) []*session.Session
	terminateFunc     func(ctx context.Context, sessionID string) error
	generateRDPFunc   func(sessionID string) ([]byte, error)
	tokenExpiryFunc   func(sessionID string) (time.Time, error)
	activeCount       int
	availablePorts    int
}

func newMockManager() *mockManager {
	return &mockManager{
		sessions:       make(map[string]*session.Session),
		activeCount:    0,
		availablePorts: 100,
	}
}

func (m *mockManager) CreateSession(ctx context.Context, userID, targetID, username, clientIP string) (*session.Session, error) {
	if m.createSessionFunc != nil {
		return m.createSessionFunc(ctx, userID, targetID, username, clientIP)
	}
	return nil, errors.New("not implemented")
}

func (m *mockManager) GetSession(sessionID string) (*session.Session, error) {
	if m.getSessionFunc != nil {
		return m.getSessionFunc(sessionID)
	}
	if sess, ok := m.sessions[sessionID]; ok {
		return sess, nil
	}
	return nil, session.ErrSessionNotFound
}

func (m *mockManager) ListSessions(userID string) []*session.Session {
	if m.listSessionsFunc != nil {
		return m.listSessionsFunc(userID)
	}
	var result []*session.Session
	for _, s := range m.sessions {
		if userID == "" || s.UserID == userID {
			result = append(result, s)
		}
	}
	return result
}

func (m *mockManager) TerminateSession(ctx context.Context, sessionID string) error {
	if m.terminateFunc != nil {
		return m.terminateFunc(ctx, sessionID)
	}
	if _, ok := m.sessions[sessionID]; ok {
		delete(m.sessions, sessionID)
		return nil
	}
	return session.ErrSessionNotFound
}

func (m *mockManager) GenerateRDPFile(sessionID string) ([]byte, error) {
	if m.generateRDPFunc != nil {
		return m.generateRDPFunc(sessionID)
	}
	if _, ok := m.sessions[sessionID]; ok {
		return []byte("full address:s:localhost:3389"), nil
	}
	return nil, session.ErrSessionNotFound
}

func (m *mockManager) TokenExpiry(sessionID string) (time.Time, error) {
	if m.tokenExpiryFunc != nil {
		return m.tokenExpiryFunc(sessionID)
	}
	return time.Now().Add(time.Hour), nil
}

func (m *mockManager) ActiveSessionCount() int {
	return m.activeCount
}

func (m *mockManager) AvailablePorts() int {
	return m.availablePorts
}

// Helper to create a test session
func createTestSession(id, userID, targetID string) *session.Session {
	now := time.Now()
	expiresAt := now.Add(time.Hour)
	return &session.Session{
		ID:           id,
		UserID:       userID,
		TargetID:     targetID,
		TargetHost:   "test-host.local",
		ExternalPort: 3400,
		State:        session.StateActive,
		CreatedAt:    now,
		ExpiresAt:    &expiresAt,
	}
}

func TestSessionsHandler_CreateSession_Success(t *testing.T) {
	mock := newMockManager()
	testSession := createTestSession("sess-123", "user-1", "target-1")
	mock.createSessionFunc = func(ctx context.Context, userID, targetID, username, clientIP string) (*session.Session, error) {
		return testSession, nil
	}

	handler := NewSessionsHandler(mock, "broker.local", nil)

	body := `{"target_id": "target-1", "username": "admin", "user_id": "user-1"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}

	var resp CreateSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.SessionID != "sess-123" {
		t.Errorf("expected session_id 'sess-123', got %q", resp.SessionID)
	}
	if resp.ProxyHost != "broker.local" {
		t.Errorf("expected proxy_host 'broker.local', got %q", resp.ProxyHost)
	}
	if resp.ProxyPort != 3400 {
		t.Errorf("expected proxy_port 3400, got %d", resp.ProxyPort)
	}
	if resp.State != "active" {
		t.Errorf("expected state 'active', got %q", resp.State)
	}
}

func TestSessionsHandler_CreateSession_InvalidJSON(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Message != "invalid request body" {
		t.Errorf("expected message 'invalid request body', got %q", resp.Message)
	}
}

func TestSessionsHandler_CreateSession_MissingTargetID(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)

	body := `{"user_id": "user-1"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Message != "target_id is required" {
		t.Errorf("expected message 'target_id is required', got %q", resp.Message)
	}
}

func TestSessionsHandler_CreateSession_TargetNotFound(t *testing.T) {
	mock := newMockManager()
	mock.createSessionFunc = func(ctx context.Context, userID, targetID, username, clientIP string) (*session.Session, error) {
		return nil, credential.ErrTargetNotFound
	}

	handler := NewSessionsHandler(mock, "broker.local", nil)

	body := `{"target_id": "nonexistent", "username": "admin", "user_id": "user-1"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestSessionsHandler_CreateSession_SessionLimitReached(t *testing.T) {
	mock := newMockManager()
	mock.createSessionFunc = func(ctx context.Context, userID, targetID, username, clientIP string) (*session.Session, error) {
		return nil, session.ErrSessionLimitReached
	}

	handler := NewSessionsHandler(mock, "broker.local", nil)

	body := `{"target_id": "target-1", "username": "admin", "user_id": "user-1"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}
}

func TestSessionsHandler_CreateSession_ProviderUnavailable(t *testing.T) {
	mock := newMockManager()
	mock.createSessionFunc = func(ctx context.Context, userID, targetID, username, clientIP string) (*session.Session, error) {
		return nil, session.ErrProviderUnavailable
	}

	handler := NewSessionsHandler(mock, "broker.local", nil)

	body := `{"target_id": "target-1", "username": "admin", "user_id": "user-1"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}
}

func TestSessionsHandler_CreateSession_InternalError(t *testing.T) {
	mock := newMockManager()
	mock.createSessionFunc = func(ctx context.Context, userID, targetID, username, clientIP string) (*session.Session, error) {
		return nil, errors.New("unexpected error")
	}

	handler := NewSessionsHandler(mock, "broker.local", nil)

	body := `{"target_id": "target-1", "username": "admin", "user_id": "user-1"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

func TestSessionsHandler_CreateSession_MissingUsername(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)

	body := `{"target_id": "target-1"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestSessionsHandler_CreateSession_AnonymousUser(t *testing.T) {
	mock := newMockManager()
	var capturedUserID string
	mock.createSessionFunc = func(ctx context.Context, userID, targetID, username, clientIP string) (*session.Session, error) {
		capturedUserID = userID
		return createTestSession("sess-123", userID, targetID), nil
	}

	handler := NewSessionsHandler(mock, "broker.local", nil)

	// No user_id in body, no JWT context, but username is provided
	body := `{"target_id": "target-1", "username": "admin"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}

	if capturedUserID != "anonymous" {
		t.Errorf("expected user_id 'anonymous', got %q", capturedUserID)
	}
}

func TestSessionsHandler_ListSessions_Success(t *testing.T) {
	mock := newMockManager()
	sess1 := createTestSession("sess-1", "user-1", "target-1")
	sess2 := createTestSession("sess-2", "user-1", "target-2")
	mock.sessions["sess-1"] = sess1
	mock.sessions["sess-2"] = sess2

	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("GET", "/api/sessions?user_id=user-1", nil)
	rec := httptest.NewRecorder()

	handler.ListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp []SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(resp))
	}
}

func TestSessionsHandler_ListSessions_Empty(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("GET", "/api/sessions?user_id=user-1", nil)
	rec := httptest.NewRecorder()

	handler.ListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp []SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(resp))
	}
}

func TestSessionsHandler_ListSessions_FromJWTContext(t *testing.T) {
	mock := newMockManager()
	sess := createTestSession("sess-1", "jwt-user", "target-1")
	mock.sessions["sess-1"] = sess

	handler := NewSessionsHandler(mock, "broker.local", nil)

	// Create request with user ID in context (simulating JWT auth)
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	ctx := context.WithValue(req.Context(), ContextKeyUserID, "jwt-user")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.ListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp []SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 1 {
		t.Errorf("expected 1 session, got %d", len(resp))
	}
}

func TestSessionsHandler_ListSessions_NoFilter(t *testing.T) {
	mock := newMockManager()
	var capturedUserID string
	mock.listSessionsFunc = func(userID string) []*session.Session {
		capturedUserID = userID
		return []*session.Session{}
	}

	handler := NewSessionsHandler(mock, "broker.local", nil)

	// No user_id in query, no JWT context — should list all sessions
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()

	handler.ListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	if capturedUserID != "" {
		t.Errorf("expected empty user_id (all sessions), got %q", capturedUserID)
	}
}

func TestSessionsHandler_GetSession_Success(t *testing.T) {
	mock := newMockManager()
	sess := createTestSession("sess-123", "user-1", "target-1")
	sess.PID = 12345
	connAt := time.Now()
	sess.ConnectedAt = &connAt
	mock.sessions["sess-123"] = sess

	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("GET", "/api/sessions/sess-123", nil)
	req.SetPathValue("id", "sess-123")
	rec := httptest.NewRecorder()

	handler.GetSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.SessionID != "sess-123" {
		t.Errorf("expected session_id 'sess-123', got %q", resp.SessionID)
	}
	if resp.UserID != "user-1" {
		t.Errorf("expected user_id 'user-1', got %q", resp.UserID)
	}
	if resp.TargetID != "target-1" {
		t.Errorf("expected target_id 'target-1', got %q", resp.TargetID)
	}
	if resp.PID != 12345 {
		t.Errorf("expected pid 12345, got %d", resp.PID)
	}
	if resp.ConnectedAt == nil {
		t.Error("expected connected_at to be set")
	}
}

func TestSessionsHandler_GetSession_NotFound(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("GET", "/api/sessions/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()

	handler.GetSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestSessionsHandler_GetSession_MissingID(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("GET", "/api/sessions/", nil)
	// No path value set
	rec := httptest.NewRecorder()

	handler.GetSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestSessionsHandler_GetSession_InternalError(t *testing.T) {
	mock := newMockManager()
	mock.getSessionFunc = func(sessionID string) (*session.Session, error) {
		return nil, errors.New("database error")
	}

	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("GET", "/api/sessions/sess-123", nil)
	req.SetPathValue("id", "sess-123")
	rec := httptest.NewRecorder()

	handler.GetSession(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

func TestSessionsHandler_DeleteSession_Success(t *testing.T) {
	mock := newMockManager()
	mock.sessions["sess-123"] = createTestSession("sess-123", "user-1", "target-1")

	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("DELETE", "/api/sessions/sess-123", nil)
	req.SetPathValue("id", "sess-123")
	rec := httptest.NewRecorder()

	handler.DeleteSession(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rec.Code)
	}

	// Verify session was deleted
	if _, ok := mock.sessions["sess-123"]; ok {
		t.Error("expected session to be deleted")
	}
}

func TestSessionsHandler_DeleteSession_NotFound(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("DELETE", "/api/sessions/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()

	handler.DeleteSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestSessionsHandler_DeleteSession_MissingID(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("DELETE", "/api/sessions/", nil)
	// No path value set
	rec := httptest.NewRecorder()

	handler.DeleteSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestSessionsHandler_DeleteSession_InternalError(t *testing.T) {
	mock := newMockManager()
	mock.terminateFunc = func(ctx context.Context, sessionID string) error {
		return errors.New("termination failed")
	}

	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("DELETE", "/api/sessions/sess-123", nil)
	req.SetPathValue("id", "sess-123")
	rec := httptest.NewRecorder()

	handler.DeleteSession(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

func TestSessionsHandler_DownloadRDPFile_Success(t *testing.T) {
	mock := newMockManager()
	sess := createTestSession("sess-123", "user-1", "target-1")
	mock.sessions["sess-123"] = sess

	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("GET", "/api/sessions/sess-123/rdp", nil)
	req.SetPathValue("id", "sess-123")
	rec := httptest.NewRecorder()

	handler.DownloadRDPFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/x-rdp" {
		t.Errorf("expected Content-Type 'application/x-rdp', got %q", contentType)
	}

	contentDisp := rec.Header().Get("Content-Disposition")
	if contentDisp == "" {
		t.Error("expected Content-Disposition header to be set")
	}

	if rec.Body.Len() == 0 {
		t.Error("expected response body to contain RDP file content")
	}
}

func TestSessionsHandler_DownloadRDPFile_NotFound(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("GET", "/api/sessions/nonexistent/rdp", nil)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()

	handler.DownloadRDPFile(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestSessionsHandler_DownloadRDPFile_MissingID(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("GET", "/api/sessions//rdp", nil)
	// No path value set
	rec := httptest.NewRecorder()

	handler.DownloadRDPFile(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestSessionsHandler_DownloadRDPFile_GenerateError(t *testing.T) {
	mock := newMockManager()
	sess := createTestSession("sess-123", "user-1", "target-1")
	mock.sessions["sess-123"] = sess
	mock.generateRDPFunc = func(sessionID string) ([]byte, error) {
		return nil, errors.New("generation failed")
	}

	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("GET", "/api/sessions/sess-123/rdp", nil)
	req.SetPathValue("id", "sess-123")
	rec := httptest.NewRecorder()

	handler.DownloadRDPFile(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

func TestSessionsHandler_DownloadRDPFile_GetSessionInternalError(t *testing.T) {
	mock := newMockManager()
	mock.getSessionFunc = func(sessionID string) (*session.Session, error) {
		return nil, errors.New("database error")
	}

	handler := NewSessionsHandler(mock, "broker.local", nil)

	req := httptest.NewRequest("GET", "/api/sessions/sess-123/rdp", nil)
	req.SetPathValue("id", "sess-123")
	rec := httptest.NewRecorder()

	handler.DownloadRDPFile(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

func TestSessionsHandler_RegisterRoutes(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)
	router := NewRouter("test-secret", nil)

	// Should not panic
	handler.RegisterRoutes(router)
}

func TestExtractClientIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18, 150.172.238.178")

	ip := extractClientIP(req)

	if ip != "203.0.113.195" {
		t.Errorf("expected IP '203.0.113.195', got %q", ip)
	}
}

func TestExtractClientIP_XForwardedFor_Single(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.195")

	ip := extractClientIP(req)

	if ip != "203.0.113.195" {
		t.Errorf("expected IP '203.0.113.195', got %q", ip)
	}
}

func TestExtractClientIP_XRealIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Real-IP", "203.0.113.195")

	ip := extractClientIP(req)

	if ip != "203.0.113.195" {
		t.Errorf("expected IP '203.0.113.195', got %q", ip)
	}
}

func TestExtractClientIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	ip := extractClientIP(req)

	if ip != "192.168.1.1" {
		t.Errorf("expected IP '192.168.1.1', got %q", ip)
	}
}

func TestExtractClientIP_RemoteAddr_IPv6(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[::1]:8080"

	ip := extractClientIP(req)

	if ip != "::1" {
		t.Errorf("expected IP '::1', got %q", ip)
	}
}

func TestExtractClientIP_RemoteAddr_NoPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1"

	ip := extractClientIP(req)

	if ip != "192.168.1.1" {
		t.Errorf("expected IP '192.168.1.1', got %q", ip)
	}
}

func TestExtractSessionID(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/api/sessions/abc123", "abc123"},
		{"/api/sessions/abc123/rdp", "abc123"},
		{"/api/sessions/", ""},
		{"/api/", ""},
		{"/other/path", ""},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := extractSessionID(tc.path)
			if result != tc.expected {
				t.Errorf("extractSessionID(%q) = %q, expected %q", tc.path, result, tc.expected)
			}
		})
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{123, "123"},
		{-1, "-1"},
		{-123, "-123"},
		{1000000, "1000000"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			result := itoa(tc.input)
			if result != tc.expected {
				t.Errorf("itoa(%d) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestSessionToResponse(t *testing.T) {
	now := time.Now()
	connAt := now.Add(-10 * time.Minute)
	expAt := now.Add(time.Hour)

	sess := &session.Session{
		ID:           "sess-123",
		UserID:       "user-1",
		TargetID:     "target-1",
		TargetHost:   "host.local",
		ExternalPort: 3400,
		State:        session.StateConnected,
		PID:          12345,
		CreatedAt:    now,
		ConnectedAt:  &connAt,
		ExpiresAt:    &expAt,
	}

	resp := sessionToResponse(sess)

	if resp.SessionID != "sess-123" {
		t.Errorf("expected session_id 'sess-123', got %q", resp.SessionID)
	}
	if resp.UserID != "user-1" {
		t.Errorf("expected user_id 'user-1', got %q", resp.UserID)
	}
	if resp.TargetID != "target-1" {
		t.Errorf("expected target_id 'target-1', got %q", resp.TargetID)
	}
	if resp.TargetHost != "host.local" {
		t.Errorf("expected target_host 'host.local', got %q", resp.TargetHost)
	}
	if resp.ProxyPort != 3400 {
		t.Errorf("expected proxy_port 3400, got %d", resp.ProxyPort)
	}
	if resp.State != "connected" {
		t.Errorf("expected state 'connected', got %q", resp.State)
	}
	if resp.PID != 12345 {
		t.Errorf("expected pid 12345, got %d", resp.PID)
	}
	if resp.ConnectedAt == nil {
		t.Error("expected connected_at to be set")
	}
	if resp.ExpiresAt == nil {
		t.Error("expected expires_at to be set")
	}
}

func TestSessionToResponse_MinimalSession(t *testing.T) {
	sess := &session.Session{
		ID:           "sess-123",
		UserID:       "user-1",
		TargetID:     "target-1",
		TargetHost:   "host.local",
		ExternalPort: 3400,
		State:        session.StateActive,
		CreatedAt:    time.Now(),
	}

	resp := sessionToResponse(sess)

	if resp.ConnectedAt != nil {
		t.Error("expected connected_at to be nil")
	}
	if resp.ExpiresAt != nil {
		t.Error("expected expires_at to be nil")
	}
}

func TestNewSessionsHandler(t *testing.T) {
	mock := newMockManager()
	handler := NewSessionsHandler(mock, "broker.local", nil)

	if handler == nil {
		t.Fatal("NewSessionsHandler returned nil")
	}
	if handler.brokerHost != "broker.local" {
		t.Errorf("expected brokerHost 'broker.local', got %q", handler.brokerHost)
	}
}
