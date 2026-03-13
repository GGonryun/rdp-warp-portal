//go:build windows

package service

import (
	"fmt"

	"golang.org/x/sys/windows/svc"
)

// GatewayService implements the svc.Handler interface so the agent can run as
// a Windows service managed by the Service Control Manager.
type GatewayService struct {
	configPath string
}

// Execute is the entry point called by the Windows SCM. It manages the service
// lifecycle: start, run, and stop/shutdown.
func (s *GatewayService) Execute(args []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	agent, err := StartAgent(s.configPath)
	if err != nil {
		// Return a non-zero exit code to signal failure to the SCM.
		return true, 1
	}

	status <- svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown,
	}

	for {
		select {
		case cr := <-r:
			switch cr.Cmd {
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				agent.Shutdown()
				return false, 0
			case svc.Interrogate:
				status <- cr.CurrentStatus
			}
		}
	}
}

// RunService starts the agent as a Windows service under the given service
// name. This function blocks until the service is stopped.
func RunService(configPath string) error {
	err := svc.Run("GatewayAgent", &GatewayService{configPath: configPath})
	if err != nil {
		return fmt.Errorf("windows service: %w", err)
	}
	return nil
}

// IsWindowsService reports whether the process is running as a Windows service.
func IsWindowsService() bool {
	is, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return is
}
