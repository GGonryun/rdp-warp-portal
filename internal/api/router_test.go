package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestNewRouter(t *testing.T) {
	router := NewRouter("test-secret", nil)
	if router == nil {
		t.Fatal("NewRouter returned nil")
	}
}

func TestRouter_HandleFunc_NoAuth(t *testing.T) {
	router := NewRouter("test-secret", nil)

	called := false
	router.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}, false) // No auth required

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if !called {
		t.Error("handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestRouter_HandleFunc_WithAuth_NoToken(t *testing.T) {
	router := NewRouter("test-secret", nil)

	router.HandleFunc("GET /protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, true) // Auth required

	req := httptest.NewRequest("GET", "/protected", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}
}

func TestRouter_HandleFunc_WithAuth_InvalidFormat(t *testing.T) {
	router := NewRouter("test-secret", nil)

	router.HandleFunc("GET /protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, true)

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "InvalidFormat") // Not "Bearer <token>"
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}
}

func TestRouter_HandleFunc_WithAuth_ValidToken(t *testing.T) {
	secret := "test-secret"
	router := NewRouter(secret, nil)

	var extractedUserID string
	router.HandleFunc("GET /protected", func(w http.ResponseWriter, r *http.Request) {
		extractedUserID = getUserID(r.Context())
		w.WriteHeader(http.StatusOK)
	}, true)

	// Create a valid JWT
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "test-user-123",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString([]byte(secret))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if extractedUserID != "test-user-123" {
		t.Errorf("expected user ID 'test-user-123', got %q", extractedUserID)
	}
}

func TestRouter_HandleFunc_WithAuth_ExpiredToken(t *testing.T) {
	secret := "test-secret"
	router := NewRouter(secret, nil)

	router.HandleFunc("GET /protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, true)

	// Create an expired JWT
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "test-user",
		"exp": time.Now().Add(-time.Hour).Unix(), // Expired
	})
	tokenString, _ := token.SignedString([]byte(secret))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for expired token, got %d", rec.Code)
	}
}

func TestRouter_HandleFunc_WithAuth_WrongSecret(t *testing.T) {
	router := NewRouter("correct-secret", nil)

	router.HandleFunc("GET /protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, true)

	// Create a JWT signed with wrong secret
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "test-user",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString([]byte("wrong-secret"))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for wrong secret, got %d", rec.Code)
	}
}

func TestRouter_DevMode_NoSecret(t *testing.T) {
	// When no secret is configured, accept any token (dev mode)
	router := NewRouter("", nil)

	var extractedUserID string
	router.HandleFunc("GET /protected", func(w http.ResponseWriter, r *http.Request) {
		extractedUserID = getUserID(r.Context())
		w.WriteHeader(http.StatusOK)
	}, true)

	// Create a JWT (unsigned is fine in dev mode)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "dev-user",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString([]byte("any-secret"))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 in dev mode, got %d", rec.Code)
	}
	if extractedUserID != "dev-user" {
		t.Errorf("expected user ID 'dev-user', got %q", extractedUserID)
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusNotFound, "resource not found")

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}

	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json")
	}

	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Code != 404 {
		t.Errorf("expected code 404, got %d", resp.Code)
	}
	if resp.Message != "resource not found" {
		t.Errorf("expected message 'resource not found', got %q", resp.Message)
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	data := map[string]string{"key": "value"}
	writeJSON(rec, http.StatusOK, data)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json")
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["key"] != "value" {
		t.Errorf("expected key='value', got %q", resp["key"])
	}
}

func TestGetUserID(t *testing.T) {
	secret := "test-secret"
	router := NewRouter(secret, nil)

	var userID string
	router.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		userID = getUserID(r.Context())
		w.WriteHeader(http.StatusOK)
	}, true)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-from-jwt",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString([]byte(secret))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if userID != "user-from-jwt" {
		t.Errorf("expected user ID 'user-from-jwt', got %q", userID)
	}
}
