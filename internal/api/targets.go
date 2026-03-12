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

// TargetResponse is the response body for a single target.
type TargetResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
}

// RegisterRoutes registers the target routes on the router.
func (h *TargetsHandler) RegisterRoutes(router *Router) {
	router.HandleFunc("GET /api/targets", h.ListTargets, true)
}

// ListTargets handles GET /api/targets.
func (h *TargetsHandler) ListTargets(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "user not authenticated")
		return
	}

	targets, err := h.provider.ListTargets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list targets")
		return
	}

	resp := make([]TargetResponse, 0, len(targets))
	for _, target := range targets {
		resp = append(resp, TargetResponse{
			ID:       target.ID,
			Name:     target.Name,
			Hostname: target.Hostname,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}
