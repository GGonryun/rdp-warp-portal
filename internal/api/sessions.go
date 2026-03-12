package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/p0-security/rdp-broker/internal/credential"
	"github.com/p0-security/rdp-broker/internal/session"
)

// SessionsHandler handles session-related API endpoints.
type SessionsHandler struct {
	manager    *session.Manager
	brokerHost string
}

// NewSessionsHandler creates a new sessions handler.
func NewSessionsHandler(manager *session.Manager, brokerHost string) *SessionsHandler {
	return &SessionsHandler{
		manager:    manager,
		brokerHost: brokerHost,
	}
}

// CreateSessionRequest is the request body for creating a session.
type CreateSessionRequest struct {
	TargetID string `json:"target_id"`
	UserID   string `json:"user_id,omitempty"` // Optional: if not provided, uses JWT sub claim
}

// CreateSessionResponse is the response body for creating a session.
type CreateSessionResponse struct {
	SessionID      string `json:"session_id"`
	ProxyHost      string `json:"proxy_host"`
	ProxyPort      int    `json:"proxy_port"`
	RDPFileURL     string `json:"rdp_file_url"`
	State          string `json:"state"`
	CreatedAt      string `json:"created_at"`
	TokenExpiresAt string `json:"token_expires_at"`
}

// SessionResponse is the response body for a single session.
type SessionResponse struct {
	SessionID   string  `json:"session_id"`
	UserID      string  `json:"user_id"`
	TargetID    string  `json:"target_id"`
	TargetHost  string  `json:"target_host"`
	ProxyPort   int     `json:"proxy_port"`
	State       string  `json:"state"`
	PID         int     `json:"pid,omitempty"`
	CreatedAt   string  `json:"created_at"`
	ConnectedAt *string `json:"connected_at,omitempty"`
	ExpiresAt   *string `json:"expires_at,omitempty"`
}

// RegisterRoutes registers the session routes on the router.
func (h *SessionsHandler) RegisterRoutes(router *Router) {
	// Auth is optional - user_id can be passed in request body for dev mode
	router.HandleFunc("POST /api/sessions", h.CreateSession, false)
	router.HandleFunc("GET /api/sessions", h.ListSessions, false)
	router.HandleFunc("GET /api/sessions/{id}", h.GetSession, false)
	router.HandleFunc("GET /api/sessions/{id}/rdp", h.DownloadRDPFile, false)
	router.HandleFunc("DELETE /api/sessions/{id}", h.DeleteSession, false)
}

// CreateSession handles POST /api/sessions.
func (h *SessionsHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TargetID == "" {
		writeError(w, http.StatusBadRequest, "target_id is required")
		return
	}

	// Use user_id from request body, fall back to JWT claim
	userID := req.UserID
	if userID == "" {
		userID = getUserID(r.Context())
	}
	if userID == "" {
		userID = "anonymous" // Default for dev mode
	}

	// Extract client IP for session binding
	clientIP := extractClientIP(r)

	sess, err := h.manager.CreateSession(r.Context(), userID, req.TargetID, clientIP)
	if err != nil {
		slog.Error("failed to create session", "error", err, "target_id", req.TargetID, "user_id", userID)
		switch {
		case errors.Is(err, credential.ErrTargetNotFound):
			writeError(w, http.StatusNotFound, "target not found")
		case errors.Is(err, session.ErrSessionLimitReached):
			writeError(w, http.StatusServiceUnavailable, "session limit reached")
		case errors.Is(err, session.ErrProviderUnavailable):
			writeError(w, http.StatusServiceUnavailable, "credential provider unavailable")
		default:
			writeError(w, http.StatusInternalServerError, "failed to create session")
		}
		return
	}

	tokenExpiry, _ := h.manager.TokenExpiry(sess.ID)

	resp := CreateSessionResponse{
		SessionID:      sess.ID,
		ProxyHost:      h.brokerHost,
		ProxyPort:      sess.ExternalPort,
		RDPFileURL:     "/api/sessions/" + sess.ID + "/rdp",
		State:          string(sess.State),
		CreatedAt:      sess.CreatedAt.Format("2006-01-02T15:04:05Z"),
		TokenExpiresAt: tokenExpiry.Format("2006-01-02T15:04:05Z"),
	}

	writeJSON(w, http.StatusCreated, resp)
}

// ListSessions handles GET /api/sessions.
func (h *SessionsHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = getUserID(r.Context())
	}
	if userID == "" {
		userID = "anonymous"
	}

	sessions := h.manager.ListSessions(userID)

	resp := make([]SessionResponse, 0, len(sessions))
	for _, sess := range sessions {
		sr := sessionToResponse(sess)
		resp = append(resp, sr)
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetSession handles GET /api/sessions/{id}.
func (h *SessionsHandler) GetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session ID is required")
		return
	}

	sess, err := h.manager.GetSession(sessionID)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to get session")
		}
		return
	}

	resp := sessionToResponse(sess)
	writeJSON(w, http.StatusOK, resp)
}

// DownloadRDPFile handles GET /api/sessions/{id}/rdp.
func (h *SessionsHandler) DownloadRDPFile(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session ID is required")
		return
	}

	sess, err := h.manager.GetSession(sessionID)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to get session")
		}
		return
	}

	rdpContent, err := h.manager.GenerateRDPFile(sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate RDP file")
		return
	}

	filename := session.RDPFilename(sess.TargetID)

	w.Header().Set("Content-Type", "application/x-rdp")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Content-Length", itoa(len(rdpContent)))
	w.WriteHeader(http.StatusOK)
	w.Write(rdpContent)
}

// DeleteSession handles DELETE /api/sessions/{id}.
func (h *SessionsHandler) DeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session ID is required")
		return
	}

	if err := h.manager.TerminateSession(r.Context(), sessionID); err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to terminate session")
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// sessionToResponse converts a Session to a SessionResponse.
func sessionToResponse(sess *session.Session) SessionResponse {
	sr := SessionResponse{
		SessionID:  sess.ID,
		UserID:     sess.UserID,
		TargetID:   sess.TargetID,
		TargetHost: sess.TargetHost,
		ProxyPort:  sess.ExternalPort,
		State:      string(sess.State),
		PID:        sess.PID,
		CreatedAt:  sess.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}

	if sess.ConnectedAt != nil {
		connAt := sess.ConnectedAt.Format("2006-01-02T15:04:05Z")
		sr.ConnectedAt = &connAt
	}

	if sess.ExpiresAt != nil {
		expAt := sess.ExpiresAt.Format("2006-01-02T15:04:05Z")
		sr.ExpiresAt = &expAt
	}

	return sr
}

// itoa converts an int to a string (simple implementation).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var neg bool
	if n < 0 {
		neg = true
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// extractSessionID extracts the session ID from the URL path.
// Expected format: /api/sessions/{id} or /api/sessions/{id}/rdp
func extractSessionID(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "sessions" {
		return parts[2]
	}
	return ""
}

// extractClientIP extracts the client IP from the request.
// Checks X-Forwarded-For and X-Real-IP headers first, then falls back to RemoteAddr.
func extractClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (may contain multiple IPs)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the list
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr (format: "ip:port" or "[ipv6]:port")
	remoteAddr := r.RemoteAddr
	if idx := strings.LastIndex(remoteAddr, ":"); idx != -1 {
		// Handle IPv6 addresses like "[::1]:8080"
		ip := remoteAddr[:idx]
		if strings.HasPrefix(ip, "[") && strings.HasSuffix(ip, "]") {
			return ip[1 : len(ip)-1]
		}
		return ip
	}
	return remoteAddr
}
