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

	"github.com/p0-security/rdp-broker/internal/acl"
	"github.com/p0-security/rdp-broker/internal/credential"
	"github.com/p0-security/rdp-broker/internal/session"
)

// mockManager implements the session manager interface for testing.
type mockManager struct {
	sessions          map[string]*session.Session
	createSessionFunc func(ctx context.Context, userID string, creds *credential.TargetCredentials, clientIP string) (*session.Session, error)
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

func (m *mockManager) CreateSession(ctx context.Context, userID string, creds *credential.TargetCredentials, clientIP string) (*session.Session, error) {
	if m.createSessionFunc != nil {
		return m.createSessionFunc(ctx, userID, creds, clientIP)
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

// withIdentity adds a verified user identity to the request context.
func withIdentity(req *http.Request, userID string) *http.Request {
	ctx := context.WithValue(req.Context(), ContextKeyUserID, userID)
	return req.WithContext(ctx)
}

func createTestSession(id, userID, hostname string) *session.Session {
	now := time.Now()
	expiresAt := now.Add(time.Hour)
	return &session.Session{
		ID:           id,
		UserID:       userID,
		TargetID:     hostname,
		TargetHost:   "10.0.1.10",
		ExternalPort: 3400,
		State:        session.StateActive,
		CreatedAt:    now,
		ExpiresAt:    &expiresAt,
	}
}

// testACLStore creates an ACL store pre-populated with a grant for testing.
func testACLStore() acl.Store {
	store := acl.NewMemoryStore()
	store.GrantAccess(context.Background(), acl.Grant{
		Host: acl.HostConfig{Hostname: "dc-01", IP: "10.0.1.10", Port: 3389, Domain: "CORP"},
		User: acl.UserConfig{Email: "test-user@example.com", Username: "rdpadmin", Secret: "test-secret"},
	})
	return store
}

func testSecretResolver(_ context.Context, secretName string) (string, error) {
	return "resolved-password", nil
}

func newTestHandler(mock *mockManager) *SessionsHandler {
	return NewSessionsHandler(mock, "broker.local", testACLStore(), testSecretResolver)
}

func TestSessionsHandler_CreateSession_Success(t *testing.T) {
	mock := newMockManager()
	testSession := createTestSession("sess-123", "test-user@example.com", "dc-01")
	mock.createSessionFunc = func(ctx context.Context, userID string, creds *credential.TargetCredentials, clientIP string) (*session.Session, error) {
		return testSession, nil
	}

	handler := newTestHandler(mock)

	body := `{"hostname": "dc-01"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withIdentity(req, "test-user@example.com")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d; body: %s", rec.Code, rec.Body.String())
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
}

func TestSessionsHandler_CreateSession_InvalidJSON(t *testing.T) {
	handler := newTestHandler(newMockManager())

	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req = withIdentity(req, "test-user@example.com")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestSessionsHandler_CreateSession_MissingHostname(t *testing.T) {
	handler := newTestHandler(newMockManager())

	body := `{}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withIdentity(req, "test-user@example.com")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestSessionsHandler_CreateSession_NoGrant(t *testing.T) {
	handler := newTestHandler(newMockManager())

	body := `{"hostname": "unknown-host"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withIdentity(req, "test-user@example.com")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", rec.Code)
	}
}

func TestSessionsHandler_CreateSession_SessionLimitReached(t *testing.T) {
	mock := newMockManager()
	mock.createSessionFunc = func(ctx context.Context, userID string, creds *credential.TargetCredentials, clientIP string) (*session.Session, error) {
		return nil, session.ErrSessionLimitReached
	}

	handler := newTestHandler(mock)

	body := `{"hostname": "dc-01"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withIdentity(req, "test-user@example.com")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}
}

func TestSessionsHandler_CreateSession_InternalError(t *testing.T) {
	mock := newMockManager()
	mock.createSessionFunc = func(ctx context.Context, userID string, creds *credential.TargetCredentials, clientIP string) (*session.Session, error) {
		return nil, errors.New("unexpected error")
	}

	handler := newTestHandler(mock)

	body := `{"hostname": "dc-01"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withIdentity(req, "test-user@example.com")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

func TestSessionsHandler_CreateSession_NoIdentity(t *testing.T) {
	handler := newTestHandler(newMockManager())

	body := `{"hostname": "dc-01"}`
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.CreateSession(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}
}

func TestSessionsHandler_ListSessions_Success(t *testing.T) {
	mock := newMockManager()
	mock.sessions["sess-1"] = createTestSession("sess-1", "user-1", "dc-01")
	mock.sessions["sess-2"] = createTestSession("sess-2", "user-1", "dc-02")

	handler := newTestHandler(mock)

	req := httptest.NewRequest("GET", "/api/sessions?user_id=user-1", nil)
	rec := httptest.NewRecorder()

	handler.ListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp []SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(resp) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(resp))
	}
}

func TestSessionsHandler_GetSession_Success(t *testing.T) {
	mock := newMockManager()
	mock.sessions["sess-123"] = createTestSession("sess-123", "user-1", "dc-01")

	handler := newTestHandler(mock)

	req := httptest.NewRequest("GET", "/api/sessions/sess-123", nil)
	req.SetPathValue("id", "sess-123")
	rec := httptest.NewRecorder()

	handler.GetSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestSessionsHandler_GetSession_NotFound(t *testing.T) {
	handler := newTestHandler(newMockManager())

	req := httptest.NewRequest("GET", "/api/sessions/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()

	handler.GetSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestSessionsHandler_DeleteSession_Success(t *testing.T) {
	mock := newMockManager()
	mock.sessions["sess-123"] = createTestSession("sess-123", "user-1", "dc-01")

	handler := newTestHandler(mock)

	req := httptest.NewRequest("DELETE", "/api/sessions/sess-123", nil)
	req.SetPathValue("id", "sess-123")
	rec := httptest.NewRecorder()

	handler.DeleteSession(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rec.Code)
	}
}

func TestSessionsHandler_DeleteSession_NotFound(t *testing.T) {
	handler := newTestHandler(newMockManager())

	req := httptest.NewRequest("DELETE", "/api/sessions/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()

	handler.DeleteSession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestSessionsHandler_RegisterRoutes(t *testing.T) {
	handler := newTestHandler(newMockManager())
	router := NewRouter("test-secret", nil, nil)
	handler.RegisterRoutes(router)
}

func TestExtractClientIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if ip := extractClientIP(req); ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}
}

func TestExtractClientIP_XRealIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Real-IP", "1.2.3.4")
	if ip := extractClientIP(req); ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}
}

func TestExtractClientIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	if ip := extractClientIP(req); ip != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", ip)
	}
}
