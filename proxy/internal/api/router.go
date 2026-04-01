// Package api provides the REST API for the RDP broker.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/p0-security/rdp-broker/internal/auth"
)

// ContextKey is a type for context keys to avoid collisions.
type ContextKey string

const (
	// ContextKeyUserID is the context key for the authenticated user ID.
	ContextKeyUserID ContextKey = "user_id"
	// ContextKeyRequestID is the context key for the request ID.
	ContextKeyRequestID ContextKey = "request_id"
)

// Router wraps http.ServeMux with middleware and logging.
type Router struct {
	mux      *http.ServeMux
	apiKey   string
	verifier *auth.Verifier
	logger   *slog.Logger
}

// NewRouter creates a new API router.
// If apiKey is non-empty, all /api/ requests (except JWT-protected ones) require Bearer <apiKey>.
// The verifier is used for JWT-protected endpoints (POST /api/sessions).
func NewRouter(apiKey string, verifier *auth.Verifier, logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{
		mux:      http.NewServeMux(),
		apiKey:   apiKey,
		verifier: verifier,
		logger:   logger,
	}
}

// Handle registers a handler for a pattern.
// The requireAuth parameter is kept for compatibility but ignored —
// auth is now handled globally based on the API key.
func (r *Router) Handle(pattern string, handler http.Handler, requireAuth bool) {
	handler = r.loggingMiddleware(handler)
	r.mux.Handle(pattern, handler)
}

// HandleFunc registers a handler function for a pattern.
func (r *Router) HandleFunc(pattern string, handler http.HandlerFunc, requireAuth bool) {
	r.Handle(pattern, handler, requireAuth)
}

// ServeHTTP implements http.Handler.
// If an API key is configured, all /api/ requests are gated.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Set CORS headers for all requests
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	// Handle preflight OPTIONS requests
	if req.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if strings.HasPrefix(req.URL.Path, "/api/") {
		if r.isJWTProtected(req) {
			// JWT-protected endpoint: verify signed token, extract identity.
			authHeader := req.Header.Get("Authorization")
			if authHeader == "" {
				writeError(w, http.StatusUnauthorized, "missing authorization header")
				return
			}
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				writeError(w, http.StatusUnauthorized, "invalid authorization header")
				return
			}

			claims, err := r.verifier.Verify(req.Context(), parts[1])
			if err != nil {
				r.logger.Warn("JWT verification failed", "error", err, "path", req.URL.Path)
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			sub, _ := claims.GetSubject()
			ctx := context.WithValue(req.Context(), ContextKeyUserID, sub)
			req = req.WithContext(ctx)
		} else if r.apiKey != "" {
			// All other /api/ endpoints: check shared API key.
			authHeader := req.Header.Get("Authorization")
			if authHeader == "" {
				writeError(w, http.StatusUnauthorized, "missing authorization header")
				return
			}
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" || parts[1] != r.apiKey {
				writeError(w, http.StatusUnauthorized, "invalid api key")
				return
			}
		}
	}
	r.mux.ServeHTTP(w, req)
}

// isJWTProtected returns true for endpoints that require JWT identity verification
// instead of the shared API key.
func (r *Router) isJWTProtected(req *http.Request) bool {
	if r.verifier == nil {
		return false
	}
	return req.Method == http.MethodPost && req.URL.Path == "/api/sessions"
}

// loggingMiddleware logs request details.
func (r *Router) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()

		requestID := generateRequestID()
		ctx := context.WithValue(req.Context(), ContextKeyRequestID, requestID)
		req = req.WithContext(ctx)

		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, req)

		duration := time.Since(start)

		r.logger.Info("request completed",
			"method", req.Method,
			"path", req.URL.Path,
			"query", req.URL.RawQuery,
			"status", wrapped.statusCode,
			"duration_ms", duration.Milliseconds(),
			"request_id", requestID,
			"remote_addr", req.RemoteAddr,
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// ErrorResponse is the standard error response format.
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{
		Error:   http.StatusText(status),
		Code:    status,
		Message: message,
	})
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// getUserID extracts the user ID from the request context.
func getUserID(ctx context.Context) string {
	if userID, ok := ctx.Value(ContextKeyUserID).(string); ok {
		return userID
	}
	return ""
}

// generateRequestID generates a simple request ID.
func generateRequestID() string {
	return time.Now().Format("20060102150405.000000000")
}
