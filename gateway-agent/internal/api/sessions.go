package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/p0rtal-4/gateway-agent/internal/session"
)

// handleCreateSession decodes a CreateSessionRequest, creates a new session
// via the manager, and returns the full CreateSessionResponse including
// gateway connection details.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req session.CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	sess, err := s.mgr.CreateSession(&req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	hostname, _ := os.Hostname()
	baseURL := fmt.Sprintf("http://%s:%s", hostname, s.cfg.ListenPort())

	resp := session.CreateSessionResponse{
		SessionID:   sess.ID,
		Status:      sess.Status,
		TargetID:    sess.TargetID,
		TargetHost:  sess.TargetHost,
		TargetName:  sess.TargetName,
		Token:       sess.GatewayPass,
		GatewayHost: hostname,
		GatewayPort: 3389,
		GatewayUser: sess.GatewayUser,
		GatewayPass: sess.GatewayPass,
		ExpiresAt:   sess.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		ConnectURL:  fmt.Sprintf("%s/api/v1/sessions/%s/connect", baseURL, sess.ID),
		RDPFileURL:  fmt.Sprintf("%s/api/v1/sessions/%s/rdp-file", baseURL, sess.ID),
		MonitorURL:  fmt.Sprintf("%s/api/v1/sessions/%s/monitor", baseURL, sess.ID),
	}

	respondJSON(w, http.StatusCreated, resp)
}

// handleListSessions returns all sessions, optionally filtered by the
// "status" and "requested_by" query parameters.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	requestedBy := r.URL.Query().Get("requested_by")

	sessions := s.mgr.ListSessions(status, requestedBy)

	summaries := make([]session.SessionSummary, 0, len(sessions))
	for _, sess := range sessions {
		summaries = append(summaries, session.SessionSummary{
			SessionID:   sess.ID,
			Status:      sess.Status,
			TargetID:    sess.TargetID,
			TargetHost:  sess.TargetHost,
			TargetName:  sess.TargetName,
			RequestedBy: sess.RequestedBy,
			StartedAt:   sess.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
			ExpiresAt:   sess.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	respondJSON(w, http.StatusOK, session.SessionListResponse{Sessions: summaries})
}

// sessionDetail is a response struct that mirrors Session. The Token field
// is only populated while the session is pending/ready (before the user
// connects), after which the gateway password is rotated and the token
// becomes empty.
type sessionDetail struct {
	ID             string            `json:"id"`
	Status         string            `json:"status"`
	TargetID       string            `json:"target_id"`
	TargetHost     string            `json:"target_host"`
	TargetName     string            `json:"target_name"`
	TargetUser     string            `json:"target_user"`
	RequestedBy    string            `json:"requested_by"`
	GatewayUser    string            `json:"gateway_user"`
	Token          string            `json:"token,omitempty"`
	RDSSessionID   int               `json:"rds_session_id,omitempty"`
	RecordingDir   string            `json:"recording_dir"`
	StartedAt      string            `json:"started_at"`
	ConnectedAt    *string           `json:"connected_at,omitempty"`
	DisconnectedAt *string           `json:"disconnected_at,omitempty"`
	EndedAt        *string           `json:"ended_at,omitempty"`
	ExpiresAt      string            `json:"expires_at"`
	RecordingPath  string            `json:"recording_path,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// toSessionDetail converts a Session to a sessionDetail. The Token field is
// populated only while the session is awaiting connection (pending/ready).
func toSessionDetail(sess *session.Session) sessionDetail {
	d := sessionDetail{
		ID:            sess.ID,
		Status:        sess.Status,
		TargetID:      sess.TargetID,
		TargetHost:    sess.TargetHost,
		TargetName:    sess.TargetName,
		TargetUser:    sess.TargetUser,
		RequestedBy:   sess.RequestedBy,
		GatewayUser:   sess.GatewayUser,
		RDSSessionID:  sess.RDSSessionID,
		RecordingDir:  sess.RecordingDir,
		StartedAt:     sess.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
		ExpiresAt:     sess.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		RecordingPath: sess.RecordingPath,
		Metadata:      sess.Metadata,
	}
	// Expose the session token only while the session is waiting for a
	// connection. Once the user connects, the password is rotated and
	// GatewayPass is cleared.
	if sess.GatewayPass != "" {
		d.Token = sess.GatewayPass
	}
	if sess.ConnectedAt != nil {
		t := sess.ConnectedAt.Format("2006-01-02T15:04:05Z07:00")
		d.ConnectedAt = &t
	}
	if sess.DisconnectedAt != nil {
		t := sess.DisconnectedAt.Format("2006-01-02T15:04:05Z07:00")
		d.DisconnectedAt = &t
	}
	if sess.EndedAt != nil {
		t := sess.EndedAt.Format("2006-01-02T15:04:05Z07:00")
		d.EndedAt = &t
	}
	return d
}

// handleGetSession returns the full details of a single session, with the
// gateway password omitted.
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")

	sess, err := s.mgr.GetSession(sessionID)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, toSessionDetail(sess))
}

// handleDeleteSession terminates a session with a default reason (DELETE
// semantics).
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")

	if err := s.mgr.TerminateSession(sessionID, "session deleted via API", false); err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "terminated"})
}

// handleTerminateSession decodes a TerminateRequest and terminates the
// session with the supplied reason and notification preference.
func (s *Server) handleTerminateSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")

	var req session.TerminateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	reason := req.Reason
	if reason == "" {
		reason = "terminated via API"
	}

	if err := s.mgr.TerminateSession(sessionID, reason, req.NotifyUser); err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "terminated"})
}

// handleInternalStatus processes status callbacks from the PowerShell launch
// script and updates the session accordingly.
func (s *Server) handleInternalStatus(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")

	var cb session.StatusCallback
	if err := json.NewDecoder(r.Body).Decode(&cb); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if err := s.mgr.UpdateSessionStatus(sessionID, &cb); err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
