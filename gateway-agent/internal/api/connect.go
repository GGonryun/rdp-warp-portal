package api

import (
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

//go:embed templates/connect.html
var connectHTML string

var connectPageTemplate = template.Must(template.New("connect").Parse(connectHTML))

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

// handleConnect serves a polished HTML connect page with the session token,
// download button, and status polling.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")

	sess, err := s.mgr.GetSession(sessionID)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	data := map[string]interface{}{
		"SessionID":    sess.ID,
		"TargetName":   sess.TargetName,
		"TargetHost":   sess.TargetHost,
		"Token":        sess.GatewayPass,
		"Status":       sess.Status,
		"ExpiresAt":    sess.ExpiresAt.Format("3:04 PM MST"),
		"ExpiresAtISO": sess.ExpiresAt.Format(time.RFC3339),
		"RDPFileURL":   fmt.Sprintf("/api/v1/sessions/%s/rdp-file", sess.ID),
		"StatusURL":    fmt.Sprintf("/api/v1/sessions/%s", sess.ID),
		"MonitorURL":   fmt.Sprintf("/api/v1/sessions/%s/monitor", sess.ID),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	connectPageTemplate.Execute(w, data)
}

// handleRDPFile serves a downloadable .rdp file for the given session.
// The file uses direct RDP (no gateway) with the username pre-filled.
// SecurityLayer=0 on the server ensures the user sees a standard Windows
// login prompt where they paste the 6-character session token.
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
		"full address:s:%s:3389\r\n"+
			"username:s:%s\r\n"+
			"authentication level:i:0\r\n"+
			"prompt for credentials:i:0\r\n"+
			"enablecredsspdelegation:i:1\r\n"+
			"negotiate security layer:i:0\r\n"+
			"redirectclipboards:i:1\r\n"+
			"redirectdrives:i:0\r\n"+
			"audiomode:i:0\r\n"+
			"audiocapturemode:i:0\r\n"+
			"screen mode id:i:2\r\n"+
			"desktopwidth:i:1920\r\n"+
			"desktopheight:i:1080\r\n"+
			"use multimon:i:0\r\n"+
			"autoreconnection enabled:i:1\r\n"+
			"connection type:i:7\r\n"+
			"networkautodetect:i:1\r\n"+
			"bandwidthautodetect:i:1\r\n",
		hostname, sess.GatewayUser,
	)

	w.Header().Set("Content-Type", "application/x-rdp")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="connect-%s.rdp"`, sessionID))
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
			"echo gatewaycredentialssource:i:4\r\n"+
			"echo gatewayprofileusagemethod:i:1\r\n"+
			"echo promptcredentialonce:i:1\r\n"+
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
