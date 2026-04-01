package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/p0-security/rdp-broker/internal/acl"
)

// PublicKeysHandler handles public key management endpoints.
type PublicKeysHandler struct {
	aclStore acl.Store
}

// NewPublicKeysHandler creates a new public keys handler.
func NewPublicKeysHandler(store acl.Store) *PublicKeysHandler {
	return &PublicKeysHandler{aclStore: store}
}

// RegisterRoutes registers the public key routes on the router.
func (h *PublicKeysHandler) RegisterRoutes(router *Router) {
	router.HandleFunc("GET /api/public-keys", h.listPublicKeys, false)
	router.HandleFunc("POST /api/public-keys", h.addPublicKey, false)
	router.HandleFunc("DELETE /api/public-keys", h.removePublicKey, false)
}

type addPublicKeyRequest struct {
	Email     string `json:"email"`
	PublicKey string `json:"public_key"`
}

type removePublicKeyRequest struct {
	Email       string `json:"email"`
	Fingerprint string `json:"fingerprint"`
}

func (h *PublicKeysHandler) listPublicKeys(w http.ResponseWriter, r *http.Request) {
	entries, err := h.aclStore.ListPublicKeys(r.Context())
	if err != nil {
		slog.Error("failed to list public keys", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list public keys")
		return
	}
	if entries == nil {
		entries = []acl.PublicKeyEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (h *PublicKeysHandler) addPublicKey(w http.ResponseWriter, r *http.Request) {
	var req addPublicKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("public-keys: invalid request body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !strings.Contains(req.Email, "@") || req.Email == "" {
		slog.Warn("public-keys: missing or invalid email", "email", req.Email)
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if req.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "public_key is required")
		return
	}

	pubKey, err := acl.ParsePublicKey(req.PublicKey)
	if err != nil {
		slog.Warn("public-keys: invalid public_key", "error", err, "email", req.Email)
		writeError(w, http.StatusBadRequest, "invalid public_key: "+err.Error())
		return
	}

	fingerprint, err := h.aclStore.AddPublicKey(r.Context(), req.Email, pubKey)
	if err != nil {
		slog.Error("failed to add public key", "error", err, "email", req.Email)
		writeError(w, http.StatusInternalServerError, "failed to add public key")
		return
	}

	slog.Info("public key added", "email", req.Email, "fingerprint", fingerprint)
	writeJSON(w, http.StatusOK, map[string]string{"fingerprint": fingerprint})
}

func (h *PublicKeysHandler) removePublicKey(w http.ResponseWriter, r *http.Request) {
	var req removePublicKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("public-keys: invalid request body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !strings.Contains(req.Email, "@") || req.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if req.Fingerprint == "" {
		writeError(w, http.StatusBadRequest, "fingerprint is required")
		return
	}

	if err := h.aclStore.RemovePublicKey(r.Context(), req.Email, req.Fingerprint); err != nil {
		if err == acl.ErrKeyNotFound {
			writeError(w, http.StatusNotFound, "public key not found")
			return
		}
		slog.Error("failed to remove public key", "error", err, "email", req.Email)
		writeError(w, http.StatusInternalServerError, "failed to remove public key")
		return
	}

	slog.Info("public key removed", "email", req.Email, "fingerprint", req.Fingerprint)
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}
