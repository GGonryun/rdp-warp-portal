package api

import (
	"net/http"

	"github.com/p0-security/rdp-broker/internal/credential"
)

// TargetsHandler handles target-related API endpoints.
type TargetsHandler struct {
	provider credential.CredentialProvider
}

// NewTargetsHandler creates a new targets handler.
func NewTargetsHandler(provider credential.CredentialProvider) *TargetsHandler {
	return &TargetsHandler{
		provider: provider,
	}
}

// RegisterRoutes registers the target routes on the router.
func (h *TargetsHandler) RegisterRoutes(router *Router) {
	router.HandleFunc("GET /api/targets", h.ListTargets, false)
}

// ListTargets handles GET /api/targets.
// Returns target machines without user accounts or credentials.
func (h *TargetsHandler) ListTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := h.provider.ListTargets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list targets")
		return
	}

	writeJSON(w, http.StatusOK, targets)
}
