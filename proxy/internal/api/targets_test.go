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

func (m *mockProvider) Close() error {
	return nil
}

func TestTargetsHandler_ListTargets_Success(t *testing.T) {
	provider := newMockProvider()
	provider.destinations = []credential.TargetDestination{
		{
			ID: "dc-01", Hostname: "dc-01", IP: "10.0.1.10",
			Users: []credential.TargetUser{
				{Username: "Administrator"},
			},
		},
		{
			ID: "ws-01", Hostname: "ws-01", IP: "10.0.1.50",
			Users: []credential.TargetUser{
				{Username: "svc-rdp"},
				{Username: "admin"},
			},
		},
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
		t.Fatalf("expected 2 targets, got %d", len(resp))
	}

	if resp[0].ID != "dc-01" {
		t.Errorf("expected first target ID 'dc-01', got %q", resp[0].ID)
	}
	if len(resp[0].Users) != 1 {
		t.Fatalf("expected 1 user for dc-01, got %d", len(resp[0].Users))
	}
	if resp[0].Users[0].Username != "Administrator" {
		t.Errorf("expected username 'Administrator', got %q", resp[0].Users[0].Username)
	}
	if len(resp[1].Users) != 2 {
		t.Fatalf("expected 2 users for ws-01, got %d", len(resp[1].Users))
	}
}

func TestTargetsHandler_ListTargets_Empty(t *testing.T) {
	provider := newMockProvider()

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
	provider.destErr = errors.New("database connection failed")

	handler := NewTargetsHandler(provider)
	router := NewRouter("", nil)
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
