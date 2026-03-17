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
	router.HandleFunc("POST /api/grant", h.grantAccess, false)
	router.HandleFunc("POST /api/revoke", h.revokeAccess, false)
}

type accessRequest struct {
	Email    string `json:"email"`
	TargetID string `json:"target_id"`
	Username string `json:"username"`
}

func (h *AccessHandler) grantAccess(w http.ResponseWriter, r *http.Request) {
	var req accessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validEmail(req.Email) {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if req.TargetID == "" {
		writeError(w, http.StatusBadRequest, "target_id is required")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	if err := h.aclStore.GrantAccess(r.Context(), req.Email, req.TargetID, req.Username); err != nil {
		slog.Error("failed to grant access", "error", err, "email", req.Email, "target_id", req.TargetID, "username", req.Username)
		writeError(w, http.StatusInternalServerError, "failed to grant access")
		return
	}

	slog.Info("access granted", "email", req.Email, "target_id", req.TargetID, "username", req.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (h *AccessHandler) revokeAccess(w http.ResponseWriter, r *http.Request) {
	var req accessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validEmail(req.Email) {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if req.TargetID == "" {
		writeError(w, http.StatusBadRequest, "target_id is required")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	if err := h.aclStore.RevokeAccess(r.Context(), req.Email, req.TargetID, req.Username); err != nil {
		slog.Error("failed to revoke access", "error", err, "email", req.Email, "target_id", req.TargetID, "username", req.Username)
		writeError(w, http.StatusInternalServerError, "failed to revoke access")
		return
	}

	slog.Info("access revoked", "email", req.Email, "target_id", req.TargetID, "username", req.Username)

	// Terminate any active sessions for this user on this target.
	sessions := h.manager.ListSessions(req.Email)
	for _, s := range sessions {
		if s.TargetID != req.TargetID {
			continue
		}
		if s.State == session.StateTerminating || s.State == session.StateTerminated {
			continue
		}
		slog.Info("terminating session due to access revocation",
			"session_id", s.ID,
			"email", req.Email,
			"target_id", req.TargetID,
		)
		if err := h.manager.TerminateSession(r.Context(), s.ID); err != nil {
			slog.Error("failed to terminate session", "session_id", s.ID, "error", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func validEmail(email string) bool {
	return strings.Contains(email, "@")
}
