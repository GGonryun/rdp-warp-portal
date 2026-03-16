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
	targets     []credential.TargetInfo
	listErr     error
	getErr      error
	credentials *credential.TargetCredentials
}

func newMockProvider() *mockProvider {
	return &mockProvider{
		targets: []credential.TargetInfo{},
	}
}

func (m *mockProvider) GetTargetCredentials(ctx context.Context, targetID string) (*credential.TargetCredentials, error) {
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

func (m *mockProvider) Close() error {
	return nil
}

func TestTargetsHandler_ListTargets_Success(t *testing.T) {
	provider := newMockProvider()
	provider.targets = []credential.TargetInfo{
		{ID: "dc-01", Name: "Domain Controller 01", Hostname: "dc-01.corp.local"},
		{ID: "ws-01", Name: "Workstation 01", Hostname: "ws-01.corp.local"},
	}

	handler := NewTargetsHandler(provider)
	router := NewRouter("", nil)
	handler.RegisterRoutes(router)

	req := httptest.NewRequest("GET", "/api/targets", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp []TargetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp) != 2 {
		t.Errorf("expected 2 targets, got %d", len(resp))
	}

	if resp[0].ID != "dc-01" {
		t.Errorf("expected first target ID 'dc-01', got %q", resp[0].ID)
	}
	if resp[0].Name != "Domain Controller 01" {
		t.Errorf("expected first target name 'Domain Controller 01', got %q", resp[0].Name)
	}
	if resp[0].Hostname != "dc-01.corp.local" {
		t.Errorf("expected first target hostname 'dc-01.corp.local', got %q", resp[0].Hostname)
	}
}

func TestTargetsHandler_ListTargets_Empty(t *testing.T) {
	provider := newMockProvider()
	provider.targets = []credential.TargetInfo{}

	handler := NewTargetsHandler(provider)
	router := NewRouter("", nil)
	handler.RegisterRoutes(router)

	req := httptest.NewRequest("GET", "/api/targets", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp []TargetResponse
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
	router := NewRouter("", nil)
	handler.RegisterRoutes(router)

	req := httptest.NewRequest("GET", "/api/targets", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Message != "failed to list targets" {
		t.Errorf("expected message 'failed to list targets', got %q", resp.Message)
	}
}

// Auth tests removed — /api/targets no longer requires authentication.

func TestNewTargetsHandler(t *testing.T) {
	provider := newMockProvider()
	handler := NewTargetsHandler(provider)

	if handler == nil {
		t.Fatal("NewTargetsHandler returned nil")
	}
	if handler.provider != provider {
		t.Error("expected provider to be set")
	}
}

func TestTargetsHandler_RegisterRoutes(t *testing.T) {
	provider := newMockProvider()
	handler := NewTargetsHandler(provider)
	router := NewRouter("test-secret", nil)

	// Should not panic
	handler.RegisterRoutes(router)
}
