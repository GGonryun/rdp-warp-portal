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
	entries, err := h.aclStore.ListAll(r.Context())
	if err != nil {
		slog.Error("failed to list access", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list access")
		return
	}
	if entries == nil {
		entries = []acl.ACLEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
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

	// Check if access already exists (idempotent).
	already, err := h.aclStore.HasAccess(r.Context(), req.Email, req.TargetID, req.Username)
	if err != nil {
		slog.Error("failed to check access", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to check access")
		return
	}
	if already {
		writeError(w, http.StatusConflict, "access already exists")
		return
	}

	if err := h.aclStore.GrantAccess(r.Context(), req.Email, req.TargetID, req.Username); err != nil {
		slog.Error("failed to grant access", "error", err, "email", req.Email, "target_id", req.TargetID, "username", req.Username)
		writeError(w, http.StatusInternalServerError, "failed to grant access")
		return
	}

	slog.Info("access granted", "email", req.Email, "target_id", req.TargetID, "username", req.Username)
	writeJSON(w, http.StatusOK, map[string]string{"status": "granted"})
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

	// Check if access exists before revoking (idempotent).
	exists, err := h.aclStore.HasAccess(r.Context(), req.Email, req.TargetID, req.Username)
	if err != nil {
		slog.Error("failed to check access", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to check access")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "access does not exist")
		return
	}

	if err := h.aclStore.RevokeAccess(r.Context(), req.Email, req.TargetID, req.Username); err != nil {
		slog.Error("failed to revoke access", "error", err, "email", req.Email, "target_id", req.TargetID, "username", req.Username)
		writeError(w, http.StatusInternalServerError, "failed to revoke access")
		return
	}

	slog.Info("access revoked", "email", req.Email, "target_id", req.TargetID, "username", req.Username)

	// Terminate only sessions matching the exact (email, target_id, username) tuple.
	sessions := h.manager.ListSessions(req.Email)
	for _, s := range sessions {
		if s.TargetID != req.TargetID {
			continue
		}
		if !strings.EqualFold(s.Username, req.Username) {
			continue
		}
		if s.State == session.StateTerminating || s.State == session.StateTerminated {
			continue
		}
		slog.Info("terminating session due to access revocation",
			"session_id", s.ID,
			"email", req.Email,
			"target_id", req.TargetID,
			"username", req.Username,
		)
		if err := h.manager.TerminateSession(r.Context(), s.ID); err != nil {
			slog.Error("failed to terminate session", "session_id", s.ID, "error", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func validEmail(email string) bool {
	return strings.Contains(email, "@")
}
