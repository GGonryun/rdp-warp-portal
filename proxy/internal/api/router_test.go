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

func TestGetUserID_NoContext(t *testing.T) {
	// Test getUserID with a context that has no user ID
	req := httptest.NewRequest("GET", "/test", nil)
	userID := getUserID(req.Context())

	if userID != "" {
		t.Errorf("expected empty user ID, got %q", userID)
	}
}

func TestRouter_DevMode_InvalidTokenFormat(t *testing.T) {
	// When no secret is configured, but token is malformed
	router := NewRouter("", nil)

	router.HandleFunc("GET /protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, true)

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-jwt-token")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for invalid token format in dev mode, got %d", rec.Code)
	}
}

func TestRouter_DevMode_MissingSubClaim(t *testing.T) {
	// Dev mode with valid token but missing sub claim - should default to "dev-user"
	router := NewRouter("", nil)

	var extractedUserID string
	router.HandleFunc("GET /protected", func(w http.ResponseWriter, r *http.Request) {
		extractedUserID = getUserID(r.Context())
		w.WriteHeader(http.StatusOK)
	}, true)

	// Create a JWT without "sub" claim
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iss": "test-issuer",
	})
	tokenString, _ := token.SignedString([]byte("any-secret"))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if extractedUserID != "dev-user" {
		t.Errorf("expected user ID 'dev-user', got %q", extractedUserID)
	}
}

func TestRouter_ProductionMode_MissingSubClaim(t *testing.T) {
	secret := "test-secret"
	router := NewRouter(secret, nil)

	router.HandleFunc("GET /protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, true)

	// Create a valid JWT without "sub" claim
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iss": "test-issuer",
	})
	tokenString, _ := token.SignedString([]byte(secret))

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for missing sub claim, got %d", rec.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Message != "missing user ID in token" {
		t.Errorf("expected message 'missing user ID in token', got %q", resp.Message)
	}
}

func TestRouter_ProductionMode_WrongSigningMethod(t *testing.T) {
	secret := "test-secret"
	router := NewRouter(secret, nil)

	router.HandleFunc("GET /protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, true)

	// Create a token with RS256 signing method (but fake signature)
	// This is tricky because we can't actually sign with RS256 without a key
	// Instead, we'll test with a manually crafted token that has "alg": "none"
	// or by using a token signed with a different algorithm

	// Create a JWT that will fail method validation
	// We'll use a valid HS256 structure but the library will parse it
	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": "test-user",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString(jwt.UnsafeAllowNoneSignatureType)

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for wrong signing method, got %d", rec.Code)
	}
}

func TestRouter_Handle_WithAuth(t *testing.T) {
	secret := "test-secret"
	router := NewRouter(secret, nil)

	// Test using Handle instead of HandleFunc
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	router.Handle("GET /test-handle", handler, true)

	// Without auth - should fail
	req := httptest.NewRequest("GET", "/test-handle", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}

	// With valid auth - should succeed
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "test-user",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString([]byte(secret))

	req = httptest.NewRequest("GET", "/test-handle", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestGenerateRequestID(t *testing.T) {
	id1 := generateRequestID()
	if id1 == "" {
		t.Error("expected non-empty request ID")
	}

	// Wait a tiny bit to ensure different timestamp
	time.Sleep(time.Millisecond)
	id2 := generateRequestID()

	if id1 == id2 {
		t.Error("expected different request IDs for different calls")
	}
}
