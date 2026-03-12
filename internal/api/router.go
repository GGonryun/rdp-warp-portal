// Package api provides the REST API for the RDP broker.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	mux       *http.ServeMux
	jwtSecret []byte
	logger    *slog.Logger
}

// NewRouter creates a new API router.
func NewRouter(jwtSecret string, logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{
		mux:       http.NewServeMux(),
		jwtSecret: []byte(jwtSecret),
		logger:    logger,
	}
}

// Handle registers a handler for a pattern with optional authentication.
func (r *Router) Handle(pattern string, handler http.Handler, requireAuth bool) {
	if requireAuth {
		handler = r.authMiddleware(handler)
	}
	handler = r.loggingMiddleware(handler)
	r.mux.Handle(pattern, handler)
}

// HandleFunc registers a handler function for a pattern with optional authentication.
func (r *Router) HandleFunc(pattern string, handler http.HandlerFunc, requireAuth bool) {
	r.Handle(pattern, handler, requireAuth)
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// loggingMiddleware logs request details.
func (r *Router) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()

		// Generate request ID
		requestID := generateRequestID()
		ctx := context.WithValue(req.Context(), ContextKeyRequestID, requestID)
		req = req.WithContext(ctx)

		// Wrap response writer to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, req)

		duration := time.Since(start)

		r.logger.Info("request completed",
			"method", req.Method,
			"path", req.URL.Path,
			"status", wrapped.statusCode,
			"duration_ms", duration.Milliseconds(),
			"request_id", requestID,
			"remote_addr", req.RemoteAddr,
		)
	})
}

// authMiddleware validates JWT tokens.
func (r *Router) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Extract token from Authorization header
		authHeader := req.Header.Get("Authorization")
		if authHeader == "" {
			writeError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}

		// Expect "Bearer <token>" format
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			writeError(w, http.StatusUnauthorized, "invalid authorization header format")
			return
		}

		tokenString := parts[1]

		// Skip validation if no secret is configured (development mode)
		var userID string
		if len(r.jwtSecret) == 0 {
			// Development mode - accept any token and extract sub claim
			token, _, err := new(jwt.Parser).ParseUnverified(tokenString, jwt.MapClaims{})
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token format")
				return
			}
			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				writeError(w, http.StatusUnauthorized, "invalid token claims")
				return
			}
			userID, _ = claims["sub"].(string)
			if userID == "" {
				userID = "dev-user" // Default for development
			}
		} else {
			// Production mode - validate token
			token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
				// Validate signing method
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return r.jwtSecret, nil
			})

			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok || !token.Valid {
				writeError(w, http.StatusUnauthorized, "invalid token claims")
				return
			}

			// Extract user ID from claims
			userID, _ = claims["sub"].(string)
			if userID == "" {
				writeError(w, http.StatusUnauthorized, "missing user ID in token")
				return
			}

			// Check expiration (jwt.Parse checks this automatically, but we can add custom logic)
			if exp, ok := claims["exp"].(float64); ok {
				if time.Unix(int64(exp), 0).Before(time.Now()) {
					writeError(w, http.StatusUnauthorized, "token expired")
					return
				}
			}
		}

		// Add user ID to context
		ctx := context.WithValue(req.Context(), ContextKeyUserID, userID)
		next.ServeHTTP(w, req.WithContext(ctx))
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
	// Simple implementation - in production, use UUID
	return time.Now().Format("20060102150405.000000000")
}
