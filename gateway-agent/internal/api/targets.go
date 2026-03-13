package api

import (
	"net/http"

	"github.com/p0rtal-4/gateway-agent/internal/credentials"
)

// targetListResponse wraps the list of safe targets.
type targetListResponse struct {
	Targets []credentials.TargetSafe `json:"targets"`
}

// handleListTargets returns all configured targets with sensitive fields
// (passwords, usernames) stripped.
func (s *Server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	targets := s.credStore.ListSafe()

	if targets == nil {
		targets = []credentials.TargetSafe{}
	}

	respondJSON(w, http.StatusOK, targetListResponse{Targets: targets})
}
