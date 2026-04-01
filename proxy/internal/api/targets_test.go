package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/p0-security/rdp-broker/internal/credential"
)

// mockProvider implements the credential.CredentialProvider interface for testing.
type mockProvider struct {
	targets      []credential.TargetInfo
	destinations []credential.TargetDestination
	listErr      error
	destErr      error
	getErr       error
	credentials  *credential.TargetCredentials
}

func newMockProvider() *mockProvider {
	return &mockProvider{
		targets:      []credential.TargetInfo{},
		destinations: []credential.TargetDestination{},
	}
}

func (m *mockProvider) GetTargetCredentials(ctx context.Context, targetID, username string) (*credential.TargetCredentials, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.credentials != nil {
		return m.credentials, nil
	}
	return nil, credential.ErrTargetNotFound
}

func (m *mockProvider) ListTargets(ctx context.Context) ([]credential.TargetInfo, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.targets, nil
}

func (m *mockProvider) ListDestinations(ctx context.Context) ([]credential.TargetDestination, error) {
	if m.destErr != nil {
		return nil, m.destErr
	}
	return m.destinations, nil
}

func (m *mockProvider) ResolveUsername(_ context.Context, email string) (string, error) {
	return "resolved-user", nil
}

func (m *mockProvider) Close() error {
	return nil
}

func TestTargetsHandler_ListTargets_Success(t *testing.T) {
	provider := newMockProvider()
	provider.targets = []credential.TargetInfo{
		{ID: "dc-01", Hostname: "dc-01", IP: "10.0.1.10", Domain: "CORP"},
		{ID: "ws-01", Hostname: "ws-01", IP: "10.0.1.50"},
	}

	handler := NewTargetsHandler(provider)
	router := NewRouter("", nil, nil)
	handler.RegisterRoutes(router)

	req := httptest.NewRequest("GET", "/api/targets", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp []credential.TargetInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(resp))
	}

	if resp[0].ID != "dc-01" {
		t.Errorf("expected first target ID 'dc-01', got %q", resp[0].ID)
	}
	if resp[0].Domain != "CORP" {
		t.Errorf("expected domain 'CORP', got %q", resp[0].Domain)
	}
	if resp[1].Domain != "" {
		t.Errorf("expected empty domain, got %q", resp[1].Domain)
	}
}

func TestTargetsHandler_ListTargets_Empty(t *testing.T) {
	provider := newMockProvider()

	handler := NewTargetsHandler(provider)
	router := NewRouter("", nil, nil)
	handler.RegisterRoutes(router)

	req := httptest.NewRequest("GET", "/api/targets", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp []credential.TargetInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 0 {
		t.Errorf("expected 0 targets, got %d", len(resp))
	}
}

func TestTargetsHandler_ListTargets_ProviderError(t *testing.T) {
	provider := newMockProvider()
	provider.listErr = errors.New("database connection failed")

	handler := NewTargetsHandler(provider)
	router := NewRouter("", nil, nil)
	handler.RegisterRoutes(router)

	req := httptest.NewRequest("GET", "/api/targets", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

func TestNewTargetsHandler(t *testing.T) {
	provider := newMockProvider()
	handler := NewTargetsHandler(provider)
	if handler == nil {
		t.Fatal("NewTargetsHandler returned nil")
	}
}

func TestTargetsHandler_ListTargets_WithAPIKey(t *testing.T) {
	provider := newMockProvider()
	provider.targets = []credential.TargetInfo{
		{ID: "dc-01", Hostname: "dc-01", IP: "10.0.1.10"},
	}

	handler := NewTargetsHandler(provider)
	router := NewRouter("test-secret", nil, nil)
	handler.RegisterRoutes(router)

	req := httptest.NewRequest("GET", "/api/targets", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}
