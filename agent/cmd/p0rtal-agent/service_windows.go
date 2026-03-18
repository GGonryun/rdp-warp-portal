//go:build windows

package main

import (
	"context"
	"fmt"
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

// runAsService runs the agent as a Windows service.
func runAsService() {
	// Set up logging to Windows Event Log.
	elog, err := eventlog.Open(serviceName)
	if err == nil {
		defer elog.Close()
		elog.Info(1, "p0rtal agent service starting")
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	if err := svc.Run(serviceName, &agentService{}); err != nil {
		if elog != nil {
			elog.Error(1, fmt.Sprintf("service failed: %v", err))
		}
		slog.Error("service failed", "error", err)
	}
}

// installService installs the agent as a Windows service.
func installService(configPath string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	// Resolve to absolute path.
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("resolve absolute path: %w", err)
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

	s, err = m.CreateService(serviceName, exePath, mgr.Config{
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
		// Non-fatal — logging will still work via stderr.
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

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}

	_ = eventlog.Remove(serviceName)

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

// tailLogs streams the service logs in real time using PowerShell.
func tailLogs() error {
	fmt.Printf("Tailing logs for service %q (Ctrl+C to stop)...\n\n", serviceName)

	script := fmt.Sprintf(`
$svcName = '%s'
$lastTime = (Get-Date).AddSeconds(-10)
while ($true) {
    $events = Get-WinEvent -FilterHashtable @{LogName='Application';ProviderName=$svcName;StartTime=$lastTime} -ErrorAction SilentlyContinue
    if ($events) {
        $events | Sort-Object TimeCreated | ForEach-Object {
            $ts = $_.TimeCreated.ToString('HH:mm:ss')
            Write-Host "[$ts] $($_.Message)"
        }
        $lastTime = ($events | Sort-Object TimeCreated | Select-Object -Last 1).TimeCreated.AddMilliseconds(1)
    }
    Start-Sleep 2
}
`, serviceName)

	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
