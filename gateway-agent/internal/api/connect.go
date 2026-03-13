package api

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// getDiskFreeGB returns the free disk space in GB for the drive containing
// the given path. Uses wmic on Windows; returns 0 on error.
func getDiskFreeGB(path string) float64 {
	if len(path) < 2 || path[1] != ':' {
		return 0
	}
	drive := strings.ToUpper(path[:2])

	cmd := exec.Command("wmic", "logicaldisk", "where", fmt.Sprintf("DeviceID='%s'", drive), "get", "FreeSpace", "/value")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "FreeSpace=") {
			val := strings.TrimPrefix(line, "FreeSpace=")
			val = strings.TrimSpace(val)
			bytes, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return 0
			}
			return bytes / (1024 * 1024 * 1024)
		}
	}
	return 0
}

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

	gatewayHost := s.cfg.GatewayHost()

	switch platform {
	case "windows":
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"platform": "windows",
			"launch_command": fmt.Sprintf(
				"cmdkey /generic:%s /user:%s /pass:%s & cmdkey /generic:TERMSRV/%s /user:%s /pass:%s & mstsc /v:%s & timeout /t 5 & cmdkey /delete:%s & cmdkey /delete:TERMSRV/%s",
				gatewayHost, sess.GatewayUser, sess.GatewayPass,
				hostname, sess.GatewayUser, sess.GatewayPass,
				hostname,
				gatewayHost, hostname,
			),
			"rdp_file_url": rdpFileURL,
			"note":         "Use the RDP file or .bat launcher for gateway connections.",
		})

	case "macos":
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"platform":     "macos",
			"rdp_file_url": rdpFileURL,
			"note":         "Download the RDP file and open with Microsoft Remote Desktop. Gateway settings are embedded in the file.",
			"gateway_host": gatewayHost,
		})

	default:
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"platform": platform,
			"host":     hostname,
			"port":     443,
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

	gatewayHost := s.cfg.GatewayHost()
	rdpContent := fmt.Sprintf(
		"full address:s:%s\r\n"+
			"username:s:.\\%s\r\n"+
			"prompt for credentials:i:0\r\n"+
			"authentication level:i:0\r\n"+
			"redirectclipboards:i:1\r\n"+
			"redirectdrives:i:0\r\n"+
			"alternate shell:s:%s\r\n"+
			"shell working directory:s:C:\\Gateway\r\n"+
			"gatewayhostname:s:%s\r\n"+
			"gatewayusagemethod:i:2\r\n"+
			"gatewaycredentialssource:i:0\r\n"+
			"gatewayprofileusagemethod:i:1\r\n",
		hostname, sess.GatewayUser, sess.AlternateShell, gatewayHost,
	)

	w.Header().Set("Content-Type", "application/x-rdp")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="session-%s.rdp"`, sessionID))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(rdpContent))
}

// handleLauncher serves a downloadable .bat file that injects gateway
// credentials via cmdkey, launches mstsc, and cleans up afterward. This
// provides a one-click connection experience without manual password entry.
func (s *Server) handleLauncher(w http.ResponseWriter, r *http.Request) {
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

	gatewayHost := s.cfg.GatewayHost()
	bat := fmt.Sprintf(
		"@echo off\r\n"+
			"echo Connecting to bastion session %s...\r\n"+
			"echo.\r\n"+
			"REM Store credentials for RD Gateway authentication\r\n"+
			"cmdkey /generic:%s /user:.\\%s /pass:%s\r\n"+
			"REM Store credentials for RDP server authentication\r\n"+
			"cmdkey /generic:TERMSRV/%s /user:.\\%s /pass:%s\r\n"+
			"REM Write temporary RDP file with gateway settings\r\n"+
			"set RDPFILE=%%TEMP%%\\gateway-session-%s.rdp\r\n"+
			"(\r\n"+
			"echo full address:s:%s\r\n"+
			"echo username:s:.\\%s\r\n"+
			"echo prompt for credentials:i:0\r\n"+
			"echo authentication level:i:0\r\n"+
			"echo redirectclipboards:i:1\r\n"+
			"echo redirectdrives:i:0\r\n"+
			"echo gatewayhostname:s:%s\r\n"+
			"echo gatewayusagemethod:i:2\r\n"+
			"echo gatewaycredentialssource:i:0\r\n"+
			"echo gatewayprofileusagemethod:i:1\r\n"+
			") > %%RDPFILE%%\r\n"+
			"mstsc %%RDPFILE%% /f\r\n"+
			"echo Cleaning up credentials...\r\n"+
			"timeout /t 3 /nobreak >nul\r\n"+
			"cmdkey /delete:%s\r\n"+
			"cmdkey /delete:TERMSRV/%s\r\n"+
			"del /q %%RDPFILE%% 2>nul\r\n"+
			"echo Done.\r\n",
		sessionID,
		gatewayHost, sess.GatewayUser, sess.GatewayPass,
		hostname, sess.GatewayUser, sess.GatewayPass,
		sessionID,
		hostname, sess.GatewayUser,
		gatewayHost,
		gatewayHost, hostname,
	)

	w.Header().Set("Content-Type", "application/bat")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="connect-%s.bat"`, sessionID))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(bat))
}

// handleHealth returns the agent's health status including active session
// count, available user pool slots, uptime, and disk free space.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	uptimeSeconds := time.Since(s.mgr.StartTime()).Seconds()

	recordingsDirFreeGB := getDiskFreeGB(s.cfg.RecordingsDir)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":                 "ok",
		"active_sessions":        s.mgr.ActiveCount(),
		"available_users":        s.mgr.AvailableUsers(),
		"uptime_seconds":         uptimeSeconds,
		"recordings_dir_free_gb": recordingsDirFreeGB,
	})
}
