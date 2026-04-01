package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/p0-security/rdp-broker/internal/acl"
	"github.com/p0-security/rdp-broker/internal/credential"
	"github.com/p0-security/rdp-broker/internal/session"
)

// SecretResolver resolves a secret name to its value (e.g., from Google Secret Manager).
type SecretResolver func(ctx context.Context, secretName string) (string, error)

// SessionManager defines the interface for session management operations.
type SessionManager interface {
	CreateSession(ctx context.Context, userID string, creds *credential.TargetCredentials, clientIP string) (*session.Session, error)
	GetSession(sessionID string) (*session.Session, error)
	ListSessions(userID string) []*session.Session
	TerminateSession(ctx context.Context, sessionID string) error
	GenerateRDPFile(sessionID string) ([]byte, error)
	TokenExpiry(sessionID string) (time.Time, error)
	ActiveSessionCount() int
	AvailablePorts() int
}

// SessionsHandler handles session-related API endpoints.
type SessionsHandler struct {
	manager        SessionManager
	brokerHost     string
	aclStore       acl.Store
	resolveSecret  SecretResolver
}

// NewSessionsHandler creates a new sessions handler.
func NewSessionsHandler(manager SessionManager, brokerHost string, aclStore acl.Store, resolveSecret SecretResolver) *SessionsHandler {
	return &SessionsHandler{
		manager:       manager,
		brokerHost:    brokerHost,
		aclStore:      aclStore,
		resolveSecret: resolveSecret,
	}
}

// CreateSessionRequest is the request body for creating a session.
type CreateSessionRequest struct {
	Hostname string `json:"hostname"`
}

// CreateSessionResponse is the response body for creating a session.
type CreateSessionResponse struct {
	SessionID      string `json:"session_id"`
	ProxyHost      string `json:"proxy_host"`
	ProxyPort      int    `json:"proxy_port"`
	RDPFileContent string `json:"rdp_file_content"`
	State          string `json:"state"`
	CreatedAt      string `json:"created_at"`
	TokenExpiresAt string `json:"token_expires_at"`
}

// SessionResponse is the response body for a single session.
type SessionResponse struct {
	SessionID   string  `json:"session_id"`
	UserID      string  `json:"user_id"`
	TargetID    string  `json:"target_id"`
	Username    string  `json:"username"`
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

	if req.Hostname == "" {
		writeError(w, http.StatusBadRequest, "hostname is required")
		return
	}

	// Identity comes from the JWT-verified sub claim, set by the router.
	userID := getUserID(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}

	// Find the grant for this (email, hostname) in the ACL.
	grant, err := h.aclStore.FindGrant(r.Context(), userID, req.Hostname)
	if err != nil {
		if errors.Is(err, acl.ErrGrantNotFound) {
			writeError(w, http.StatusForbidden, "access not granted")
			return
		}
		slog.Error("acl lookup failed", "error", err, "user_id", userID, "hostname", req.Hostname)
		writeError(w, http.StatusInternalServerError, "access check failed")
		return
	}

	// Resolve the secret to get the actual password.
	if h.resolveSecret == nil {
		slog.Error("secret resolver not configured", "user_id", userID, "hostname", req.Hostname)
		writeError(w, http.StatusServiceUnavailable, "credential provider unavailable")
		return
	}
	password, err := h.resolveSecret(r.Context(), grant.User.Secret)
	if err != nil {
		slog.Error("failed to resolve secret", "error", err, "user_id", userID, "hostname", req.Hostname, "secret", grant.User.Secret)
		writeError(w, http.StatusServiceUnavailable, "credential provider unavailable")
		return
	}

	// Build credentials from the grant + resolved password.
	creds := &credential.TargetCredentials{
		Hostname: grant.Host.Hostname,
		IP:       grant.Host.IP,
		Port:     grant.Host.Port,
		Username: grant.User.Username,
		Password: password,
		Domain:   grant.Host.Domain,
	}

	clientIP := extractClientIP(r)

	sess, err := h.manager.CreateSession(r.Context(), userID, creds, clientIP)
	if err != nil {
		slog.Error("failed to create session", "error", err, "hostname", req.Hostname, "user_id", userID)
		switch {
		case errors.Is(err, session.ErrSessionLimitReached):
			writeError(w, http.StatusServiceUnavailable, "session limit reached")
		default:
			writeError(w, http.StatusInternalServerError, "failed to create session")
		}
		return
	}

	tokenExpiry, _ := h.manager.TokenExpiry(sess.ID)

	// Generate RDP file content inline so the client doesn't need a second request.
	var rdpContent string
	if rdpBytes, err := h.manager.GenerateRDPFile(sess.ID); err == nil {
		rdpContent = string(rdpBytes)
	}

	resp := CreateSessionResponse{
		SessionID:      sess.ID,
		ProxyHost:      h.brokerHost,
		ProxyPort:      sess.ExternalPort,
		RDPFileContent: rdpContent,
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

	sessions := h.manager.ListSessions(userID)

	resp := make([]SessionResponse, 0, len(sessions))
	for _, s := range sessions {
		sr := SessionResponse{
			SessionID:  s.ID,
			UserID:     s.UserID,
			TargetID:   s.TargetID,
			Username:   s.Username,
			TargetHost: s.TargetHost,
			ProxyPort:  s.ExternalPort,
			State:      string(s.State),
			PID:        s.PID,
			CreatedAt:  s.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
		if s.ConnectedAt != nil {
			t := s.ConnectedAt.Format("2006-01-02T15:04:05Z")
			sr.ConnectedAt = &t
		}
		if s.ExpiresAt != nil {
			t := s.ExpiresAt.Format("2006-01-02T15:04:05Z")
			sr.ExpiresAt = &t
		}
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

	s, err := h.manager.GetSession(sessionID)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to get session")
		}
		return
	}

	resp := SessionResponse{
		SessionID:  s.ID,
		UserID:     s.UserID,
		TargetID:   s.TargetID,
		Username:   s.Username,
		TargetHost: s.TargetHost,
		ProxyPort:  s.ExternalPort,
		State:      string(s.State),
		PID:        s.PID,
		CreatedAt:  s.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if s.ConnectedAt != nil {
		t := s.ConnectedAt.Format("2006-01-02T15:04:05Z")
		resp.ConnectedAt = &t
	}
	if s.ExpiresAt != nil {
		t := s.ExpiresAt.Format("2006-01-02T15:04:05Z")
		resp.ExpiresAt = &t
	}

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

// extractClientIP extracts the client IP address from the request.
func extractClientIP(r *http.Request) string {
	// Check X-Forwarded-For first (for proxied requests)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to RemoteAddr
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		// Could be ip:port or [ipv6]:port
		if strings.HasPrefix(addr, "[") {
			// IPv6: [::1]:port
			if bracketIdx := strings.Index(addr, "]"); bracketIdx != -1 {
				return addr[1:bracketIdx]
			}
		}
		return addr[:idx]
	}
	return addr
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
