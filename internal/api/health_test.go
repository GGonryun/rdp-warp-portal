package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandler_Health_Success(t *testing.T) {
	mock := newMockManager()
	mock.activeCount = 5
	mock.availablePorts = 95

	handler := NewHealthHandler(mock)

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()

	handler.Health(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}
	if resp.ActiveSessions != 5 {
		t.Errorf("expected active_sessions 5, got %d", resp.ActiveSessions)
	}
	if resp.AvailablePorts != 95 {
		t.Errorf("expected available_ports 95, got %d", resp.AvailablePorts)
	}
}

func TestHealthHandler_Health_NoSessions(t *testing.T) {
	mock := newMockManager()
	mock.activeCount = 0
	mock.availablePorts = 100

	handler := NewHealthHandler(mock)

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()

	handler.Health(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.ActiveSessions != 0 {
		t.Errorf("expected active_sessions 0, got %d", resp.ActiveSessions)
	}
}

func TestHealthHandler_Ready_Success(t *testing.T) {
	mock := newMockManager()
	mock.activeCount = 3
	mock.availablePorts = 97

	handler := NewHealthHandler(mock)

	req := httptest.NewRequest("GET", "/ready", nil)
	rec := httptest.NewRecorder()

	handler.Ready(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Status != "ready" {
		t.Errorf("expected status 'ready', got %q", resp.Status)
	}
	if resp.ActiveSessions != 3 {
		t.Errorf("expected active_sessions 3, got %d", resp.ActiveSessions)
	}
	if resp.AvailablePorts != 97 {
		t.Errorf("expected available_ports 97, got %d", resp.AvailablePorts)
	}
}

func TestHealthHandler_Ready_NoPortsAvailable(t *testing.T) {
	mock := newMockManager()
	mock.activeCount = 100
	mock.availablePorts = 0

	handler := NewHealthHandler(mock)

	req := httptest.NewRequest("GET", "/ready", nil)
	rec := httptest.NewRecorder()

	handler.Ready(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Message != "no ports available" {
		t.Errorf("expected message 'no ports available', got %q", resp.Message)
	}
}

func TestHealthHandler_Ready_NegativePorts(t *testing.T) {
	mock := newMockManager()
	mock.availablePorts = -1 // Edge case

	handler := NewHealthHandler(mock)

	req := httptest.NewRequest("GET", "/ready", nil)
	rec := httptest.NewRecorder()

	handler.Ready(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}
}

func TestHealthHandler_RegisterRoutes(t *testing.T) {
	mock := newMockManager()
	handler := NewHealthHandler(mock)
	router := NewRouter("test-secret", nil)

	// Should not panic
	handler.RegisterRoutes(router)

	// Test /health endpoint
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected /health to return 200, got %d", rec.Code)
	}

	// Test /healthz endpoint
	req = httptest.NewRequest("GET", "/healthz", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected /healthz to return 200, got %d", rec.Code)
	}

	// Test /ready endpoint
	req = httptest.NewRequest("GET", "/ready", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected /ready to return 200, got %d", rec.Code)
	}
}

func TestHealthHandler_NoAuth_Required(t *testing.T) {
	mock := newMockManager()
	handler := NewHealthHandler(mock)
	router := NewRouter("test-secret", nil)
	handler.RegisterRoutes(router)

	// Health endpoints should work without auth
	endpoints := []string{"/health", "/healthz", "/ready"}

	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			req := httptest.NewRequest("GET", endpoint, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			// Should not return 401
			if rec.Code == http.StatusUnauthorized {
				t.Errorf("expected %s to not require auth, got 401", endpoint)
			}
		})
	}
}

func TestNewHealthHandler(t *testing.T) {
	mock := newMockManager()
	handler := NewHealthHandler(mock)

	if handler == nil {
		t.Fatal("NewHealthHandler returned nil")
	}
}
