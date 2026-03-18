//go:build windows

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

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
	fmt.Println("Place config.json next to agent.exe, then start with:")
	fmt.Println("  sc start p0rtal-agent")
	fmt.Println()
	fmt.Println("Or start from Services (services.msc).")
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

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}

	_ = eventlog.Remove(serviceName)

	fmt.Printf("Service %q uninstalled successfully.\n", serviceName)
	return nil
}
