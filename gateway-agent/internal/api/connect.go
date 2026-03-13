package api

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
)

// handleConnect returns platform-specific RDP connection instructions for a
// session. The optional "platform" query parameter (default "windows")
// determines which format is returned.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")

	sess, err := s.mgr.GetSession(sessionID)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	platform := r.URL.Query().Get("platform")
	if platform == "" {
		platform = "windows"
	}

	hostname, err := os.Hostname()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to determine hostname")
		return
	}

	rdpFileURL := fmt.Sprintf("/api/v1/sessions/%s/rdp-file", sessionID)

	switch platform {
	case "windows":
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"platform": "windows",
			"launch_command": fmt.Sprintf(
				"cmdkey /generic:TERMSRV/%s /user:%s /pass:%s; mstsc /v:%s; timeout /t 5; cmdkey /delete:TERMSRV/%s",
				hostname, sess.GatewayUser, sess.GatewayPass, hostname, hostname,
			),
			"rdp_file_url": rdpFileURL,
		})

	case "macos":
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"platform": "macos",
			"launch_command": fmt.Sprintf(
				"open rdp://full%%20address=s:%s:3389&username=s:%s",
				hostname, sess.GatewayUser,
			),
			"rdp_file_url": rdpFileURL,
		})

	default:
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"platform": platform,
			"host":     hostname,
			"port":     3389,
			"username": sess.GatewayUser,
			"password": sess.GatewayPass,
		})
	}
}

// handleRDPFile serves a downloadable .rdp file for the given session.
func (s *Server) handleRDPFile(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")

	sess, err := s.mgr.GetSession(sessionID)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	hostname, err := os.Hostname()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to determine hostname")
		return
	}

	rdpContent := fmt.Sprintf(
		"full address:s:%s:3389\r\nusername:s:%s\r\nprompt for credentials:i:0\r\nauthentication level:i:0\r\nredirectclipboards:i:1\r\nredirectdrives:i:0\r\nalternate shell:s:%s\r\nshell working directory:s:C:\\Gateway\r\n",
		hostname, sess.GatewayUser, sess.AlternateShell,
	)

	w.Header().Set("Content-Type", "application/x-rdp")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="session-%s.rdp"`, sessionID))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(rdpContent))
}

// handleHealth returns the agent's health status including active session
// count, available user pool slots, uptime, and disk free space.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	uptimeSeconds := time.Since(s.mgr.StartTime()).Seconds()

	// TODO: Implement cross-platform disk free space check for the
	// recordings directory. For now, return 0 to avoid platform-specific
	// imports (e.g. golang.org/x/sys/windows).
	var recordingsDirFreeGB float64

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":                 "ok",
		"active_sessions":        s.mgr.ActiveCount(),
		"available_users":        s.mgr.AvailableUsers(),
		"uptime_seconds":         uptimeSeconds,
		"recordings_dir_free_gb": recordingsDirFreeGB,
	})
}
