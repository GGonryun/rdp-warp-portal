package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/p0rtal-4/gateway-agent/internal/config"
	"github.com/p0rtal-4/gateway-agent/internal/credentials"
)

// PrepareSession creates the recording directory, writes the session config
// JSON, writes the per-user launcher script, and marks the session as ready.
func PrepareSession(sess *Session, cfg *config.Config, target *credentials.Target) error {
	recordingDir := filepath.Join(cfg.RecordingsDir, sess.ID)
	if err := os.MkdirAll(recordingDir, 0755); err != nil {
		return fmt.Errorf("create recording directory: %w", err)
	}
	sess.RecordingDir = recordingDir

	sessionCfg := SessionConfig{
		SessionID:    sess.ID,
		TargetHost:   target.Host,
		TargetPort:   target.Port,
		TargetUser:   target.Username,
		TargetPass:   target.Password,
		TargetDomain: target.Domain,
		RecordingDir: recordingDir,
		FFmpegPath:   cfg.FFmpegPath,
		CallbackURL:  fmt.Sprintf("http://localhost:%s", cfg.ListenPort()),
	}

	configData, err := json.MarshalIndent(sessionCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session config: %w", err)
	}

	configPath := filepath.Join(recordingDir, "session-config.json")
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		return fmt.Errorf("write session config: %w", err)
	}

	script := fmt.Sprintf(
		`powershell.exe -ExecutionPolicy Bypass -File "%s" -ConfigPath "%s"`,
		cfg.SessionScript, configPath,
	)
	if err := writeUserLauncher(sess.GatewayUser, script); err != nil {
		return fmt.Errorf("write user launcher: %w", err)
	}

	sess.Status = StatusReady
	return nil
}

// writeUserLauncher writes a per-user .ps1 launcher at
// C:\Gateway\scripts\launch-<username>.ps1. The HKLM Run key triggers
// session-router.ps1 at logon, which checks for this file and runs it.
func writeUserLauncher(username, program string) error {
	launcherDir := `C:\Gateway\scripts`
	launcherPath := filepath.Join(launcherDir, fmt.Sprintf("launch-%s.ps1", username))
	launcherContent := fmt.Sprintf("# Session launcher for %s\r\n%s\r\n", username, program)

	if err := os.MkdirAll(launcherDir, 0755); err != nil {
		return fmt.Errorf("create scripts dir: %w", err)
	}
	if err := os.WriteFile(launcherPath, []byte(launcherContent), 0755); err != nil {
		return fmt.Errorf("write launcher ps1: %w", err)
	}
	return nil
}

// TerminateRDSSession logs off the RDS session associated with the given
// session, if one is active.
func TerminateRDSSession(sess *Session) error {
	if sess.RDSSessionID <= 0 {
		return nil
	}

	cmd := exec.Command("logoff", strconv.Itoa(sess.RDSSessionID))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("logoff RDS session %d: %w: %s", sess.RDSSessionID, err, string(output))
	}
	return nil
}

// SendMessage sends a pop-up message to the specified RDS session using
// msg.exe with a 10-second display timeout.
func SendMessage(rdsSessionID int, message string) error {
	cmd := exec.Command("msg.exe", strconv.Itoa(rdsSessionID), "/time:10", message)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("send message to RDS session %d: %w: %s", rdsSessionID, err, string(output))
	}
	return nil
}
