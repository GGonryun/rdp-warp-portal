package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/p0-security/rdp-broker/internal/acl"
	"github.com/p0-security/rdp-broker/internal/session"
)

// AccessHandler handles grant and revoke access endpoints.
type AccessHandler struct {
	aclStore acl.Store
	manager  *session.Manager
}

// NewAccessHandler creates a new AccessHandler.
func NewAccessHandler(store acl.Store, manager *session.Manager) *AccessHandler {
	return &AccessHandler{aclStore: store, manager: manager}
}

// RegisterRoutes registers the grant/revoke routes.
func (h *AccessHandler) RegisterRoutes(router *Router) {
	router.HandleFunc("GET /api/access", h.listAccess, false)
	router.HandleFunc("POST /api/grant", h.grantAccess, false)
	router.HandleFunc("POST /api/revoke", h.revokeAccess, false)
}

func (h *AccessHandler) listAccess(w http.ResponseWriter, r *http.Request) {
	grants, err := h.aclStore.ListAll(r.Context())
	if err != nil {
		slog.Error("failed to list access", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list access")
		return
	}
	if grants == nil {
		grants = []acl.Grant{}
	}
	writeJSON(w, http.StatusOK, grants)
}

func (h *AccessHandler) grantAccess(w http.ResponseWriter, r *http.Request) {
	var grant acl.Grant
	if err := json.NewDecoder(r.Body).Decode(&grant); err != nil {
		slog.Warn("grant: invalid request body", "error", err, "remote_addr", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate required fields.
	if grant.Host.Hostname == "" {
		slog.Warn("grant: missing hostname", "remote_addr", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, "host.hostname is required")
		return
	}
	if grant.Host.IP == "" {
		slog.Warn("grant: missing ip", "remote_addr", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, "host.ip is required")
		return
	}
	if !strings.Contains(grant.User.Email, "@") || grant.User.Email == "" {
		slog.Warn("grant: missing or invalid email", "email", grant.User.Email, "remote_addr", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, "user.email is required")
		return
	}
	if grant.User.Username == "" {
		slog.Warn("grant: missing username", "email", grant.User.Email, "remote_addr", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, "user.username is required")
		return
	}
	if grant.User.Secret == "" {
		slog.Warn("grant: missing secret", "email", grant.User.Email, "remote_addr", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, "user.secret is required")
		return
	}

	// Default port.
	if grant.Host.Port == 0 {
		grant.Host.Port = 3389
	}

	if err := h.aclStore.GrantAccess(r.Context(), grant); err != nil {
		slog.Error("failed to grant access", "error", err, "email", grant.User.Email, "hostname", grant.Host.Hostname)
		writeError(w, http.StatusInternalServerError, "failed to grant access")
		return
	}

	slog.Info("access granted", "email", grant.User.Email, "hostname", grant.Host.Hostname)
	writeJSON(w, http.StatusOK, map[string]string{"status": "granted"})
}

type revokeRequest struct {
	Email    string `json:"email"`
	Hostname string `json:"hostname"`
}

func (h *AccessHandler) revokeAccess(w http.ResponseWriter, r *http.Request) {
	var req revokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("revoke: invalid request body", "error", err, "remote_addr", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !strings.Contains(req.Email, "@") || req.Email == "" {
		slog.Warn("revoke: missing or invalid email", "email", req.Email, "remote_addr", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if req.Hostname == "" {
		slog.Warn("revoke: missing hostname", "email", req.Email, "remote_addr", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, "hostname is required")
		return
	}

	exists, err := h.aclStore.HasAccess(r.Context(), req.Email, req.Hostname)
	if err != nil {
		slog.Error("failed to check access", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to check access")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "access does not exist")
		return
	}

	if err := h.aclStore.RevokeAccess(r.Context(), req.Email, req.Hostname); err != nil {
		slog.Error("failed to revoke access", "error", err, "email", req.Email, "hostname", req.Hostname)
		writeError(w, http.StatusInternalServerError, "failed to revoke access")
		return
	}

	slog.Info("access revoked", "email", req.Email, "hostname", req.Hostname)

	// Terminate sessions matching this user.
	sessions := h.manager.ListSessions(req.Email)
	for _, s := range sessions {
		if s.State == session.StateTerminating || s.State == session.StateTerminated {
			continue
		}
		slog.Info("terminating session due to access revocation",
			"session_id", s.ID,
			"email", req.Email,
			"hostname", req.Hostname,
		)
		if err := h.manager.TerminateSession(r.Context(), s.ID); err != nil {
			slog.Error("failed to terminate session", "session_id", s.ID, "error", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}
