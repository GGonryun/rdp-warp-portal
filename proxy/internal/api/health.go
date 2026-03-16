package api

import (
	"net/http"
)

// HealthManager defines the interface for health check operations.
type HealthManager interface {
	ActiveSessionCount() int
	AvailablePorts() int
}

// HealthHandler handles health check endpoints.
type HealthHandler struct {
	manager HealthManager
}

// NewHealthHandler creates a new health handler.
func NewHealthHandler(manager HealthManager) *HealthHandler {
	return &HealthHandler{
		manager: manager,
	}
}

// HealthResponse is the response body for health checks.
type HealthResponse struct {
	Status          string `json:"status"`
	ActiveSessions  int    `json:"active_sessions"`
	AvailablePorts  int    `json:"available_ports"`
}

// RegisterRoutes registers the health routes on the router.
func (h *HealthHandler) RegisterRoutes(router *Router) {
	// Health endpoint does not require authentication
	router.HandleFunc("GET /health", h.Health, false)
	router.HandleFunc("GET /healthz", h.Health, false)
	router.HandleFunc("GET /ready", h.Ready, false)
}

// Health handles GET /health.
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:         "ok",
		ActiveSessions: h.manager.ActiveSessionCount(),
		AvailablePorts: h.manager.AvailablePorts(),
	}

	writeJSON(w, http.StatusOK, resp)
}

// Ready handles GET /ready (readiness probe).
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	// Check if the manager is functional
	if h.manager.AvailablePorts() <= 0 {
		writeError(w, http.StatusServiceUnavailable, "no ports available")
		return
	}

	resp := HealthResponse{
		Status:         "ready",
		ActiveSessions: h.manager.ActiveSessionCount(),
		AvailablePorts: h.manager.AvailablePorts(),
	}

	writeJSON(w, http.StatusOK, resp)
}
