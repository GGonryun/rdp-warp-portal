package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/p0rtal-4/gateway-agent/internal/config"
	"github.com/p0rtal-4/gateway-agent/internal/credentials"
	"github.com/p0rtal-4/gateway-agent/internal/session"
)

// Server holds the dependencies for all HTTP handlers.
type Server struct {
	mgr       *session.Manager
	credStore *credentials.Store
	cfg       *config.Config
}

// NewRouter creates a chi router with all routes mounted and returns it as an
// http.Handler.
func NewRouter(mgr *session.Manager, credStore *credentials.Store, cfg *config.Config) http.Handler {
	s := &Server{
		mgr:       mgr,
		credStore: credStore,
		cfg:       cfg,
	}

	r := chi.NewRouter()

	// Set JSON content-type on every response.
	r.Use(jsonContentType)

	// Log every request.
	r.Use(requestLogger)

	// --- Public API routes ---
	r.Post("/api/v1/sessions", s.handleCreateSession)
	r.Get("/api/v1/sessions", s.handleListSessions)
	r.Get("/api/v1/sessions/{session_id}", s.handleGetSession)
	r.Delete("/api/v1/sessions/{session_id}", s.handleDeleteSession)
	r.Post("/api/v1/sessions/{session_id}/terminate", s.handleTerminateSession)
	r.Get("/api/v1/sessions/{session_id}/connect", s.handleConnect)
	r.Get("/api/v1/sessions/{session_id}/rdp-file", s.handleRDPFile)
	r.Get("/api/v1/sessions/{session_id}/stream/{filename}", s.handleStreamFile)
	r.Get("/api/v1/sessions/{session_id}/monitor", s.handleMonitor)
	r.Get("/api/v1/sessions/{session_id}/recording", s.handleRecording)
	r.Get("/api/v1/recordings", s.handleListRecordings)
	r.Get("/api/v1/targets", s.handleListTargets)

	// --- Health ---
	r.Get("/health", s.handleHealth)

	// --- Internal callback routes ---
	r.Post("/internal/sessions/{session_id}/status", s.handleInternalStatus)

	return r
}

// jsonContentType is middleware that sets the Content-Type header to
// application/json for every response.
func jsonContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// responseRecorder wraps http.ResponseWriter to capture the status code.
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.statusCode = code
	rr.ResponseWriter.WriteHeader(code)
}

// requestLogger is middleware that logs every request's method, path, response
// status, and duration.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rr := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rr, r)
		log.Printf("http: %s %s %d %s", r.Method, r.URL.Path, rr.statusCode, time.Since(start))
	})
}

// respondJSON marshals data as JSON and writes it with the given HTTP status.
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	body, err := json.Marshal(data)
	if err != nil {
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	w.Write(body)
}

// respondError writes a JSON error response with the given HTTP status and
// message.
func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

// ---------- Stub handlers (implemented in other files) ----------
//
// handleConnect       — see connect.go
// handleRDPFile       — see connect.go
// handleHealth        — see connect.go
// handleStreamFile    — see streaming.go
// handleMonitor       — see streaming.go
// handleRecording     — see recordings.go
// handleListRecordings — see recordings.go
// handleListTargets   — see targets.go
