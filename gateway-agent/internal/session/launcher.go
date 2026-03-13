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
// JSON, sets the user's initial program to the session launch script, and
// marks the session as ready.
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
	if err := setUserInitialProgram(sess.GatewayUser, script); err != nil {
		return fmt.Errorf("set initial program: %w", err)
	}

	sess.AlternateShell = script
	sess.Status = StatusReady
	return nil
}

// setUserInitialProgram configures the RDS initial program for a specific
// local user via the WMI Win32_TSEnvironmentSetting class. This ensures
// the given program launches instead of the desktop when the user connects.
func setUserInitialProgram(username, program string) error {
	// Use ADSI to set the TerminalServicesInitialProgram property on the
	// local user account. This is the standard per-user mechanism that
	// Windows RDS actually reads.
	psCommand := fmt.Sprintf(
		`$user = [ADSI]"WinNT://localhost/%s,user"; `+
			`$user.PSBase.InvokeSet("TerminalServicesInitialProgram", "%s"); `+
			`$user.PSBase.InvokeSet("TerminalServicesWorkDirectory", "C:\Gateway"); `+
			`$user.SetInfo()`,
		username, program,
	)

	cmd := exec.Command("powershell", "-Command", psCommand)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set initial program via ADSI: %w: %s", err, string(output))
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
