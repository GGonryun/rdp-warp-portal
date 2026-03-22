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

// TargetResponse is the response body for a single target with its available users.
type TargetResponse struct {
	ID       string       `json:"id"`
	Hostname string       `json:"hostname"`
	IP       string       `json:"ip"`
	Users    []TargetUser `json:"users"`
}

// TargetUser represents a user account available on a target.
type TargetUser struct {
	Username string `json:"username"`
}

// RegisterRoutes registers the target routes on the router.
func (h *TargetsHandler) RegisterRoutes(router *Router) {
	router.HandleFunc("GET /api/targets", h.ListTargets, false)
}

// ListTargets handles GET /api/targets.
func (h *TargetsHandler) ListTargets(w http.ResponseWriter, r *http.Request) {
	destinations, err := h.provider.ListDestinations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list targets")
		return
	}

	resp := make([]TargetResponse, 0, len(destinations))
	for _, d := range destinations {
		users := make([]TargetUser, 0, len(d.Users))
		for _, u := range d.Users {
			users = append(users, TargetUser{
				Username: u.Username,
			})
		}
		resp = append(resp, TargetResponse{
			ID:       d.ID,
			Hostname: d.Hostname,
			IP:       d.IP,
			Users: users,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}
