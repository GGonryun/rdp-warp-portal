package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/p0rtal-4/gateway-agent/internal/service"
	"github.com/p0rtal-4/gateway-agent/scripts"
)

func main() {
	configPath := flag.String("config", `C:\Gateway\config\agent.json`, "Path to config file")
	install := flag.Bool("install", false, "Install and configure the bastion server (run as Administrator)")
	uninstall := flag.Bool("uninstall", false, "Uninstall the bastion configuration")
	upgrade := flag.Bool("upgrade", false, "Stop service, update binary + scripts, restart service")
	stop := flag.Bool("stop", false, "Stop the GatewayAgent service")
	status := flag.Bool("status", false, "Show GatewayAgent service status")
	installDir := flag.String("install-dir", `C:\Gateway`, "Installation directory")
	flag.Parse()

	// When run as a Windows service, just start the service loop.
	if service.IsWindowsService() {
		if err := service.RunService(*configPath); err != nil {
			log.Fatalf("service error: %v", err)
		}
		return
	}

	switch {
	case *status:
		showStatus()
		waitForKeypress()
	case *stop:
		stopService()
		waitForKeypress()
	case *upgrade:
		runUpgrade(*installDir)
		waitForKeypress()
	case *install:
		stopService()
		runInstaller(*installDir, false)
		waitForKeypress()
	case *uninstall:
		stopService()
		runInstaller(*installDir, true)
		waitForKeypress()
	case noFlagsProvided():
		showUsage()
		waitForKeypress()
	default:
		runInteractive(*configPath)
	}
}

// noFlagsProvided returns true when the binary was launched with no arguments
// (e.g. double-clicked from Explorer).
func noFlagsProvided() bool {
	return len(os.Args) == 1
}

// showUsage prints help text when the user double-clicks the exe.
func showUsage() {
	fmt.Println("========================================")
	fmt.Println("  RDP Bastion Gateway Agent")
	fmt.Println("========================================")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  gateway-agent.exe --install       Install and configure the bastion (run as Admin)")
	fmt.Println("  gateway-agent.exe --upgrade       Stop service, update binary + scripts, restart")
	fmt.Println("  gateway-agent.exe --status        Show service status")
	fmt.Println("  gateway-agent.exe --stop          Stop the GatewayAgent service")
	fmt.Println("  gateway-agent.exe --uninstall     Remove bastion configuration")
	fmt.Println("  gateway-agent.exe --config PATH   Run interactively with a config file")
	fmt.Println()
	fmt.Println("First time? Run:  gateway-agent.exe --install")
	fmt.Println()
}

// waitForKeypress keeps the console window open so the user can read output.
func waitForKeypress() {
	fmt.Println()
	fmt.Print("Press Enter to close...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

// showStatus queries the Windows Service Control Manager and prints the
// GatewayAgent service state and binary path.
func showStatus() {
	cmd := exec.Command("sc.exe", "query", "GatewayAgent")
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Println("GatewayAgent service is not installed.")
		return
	}
	fmt.Println(string(output))

	// Also show the registered binary path
	cfg := exec.Command("sc.exe", "qc", "GatewayAgent")
	cfgOutput, err := cfg.CombinedOutput()
	if err == nil {
		fmt.Println(string(cfgOutput))
	}
}

// stopService stops the GatewayAgent Windows service if it is running.
func stopService() {
	fmt.Println("Stopping GatewayAgent service...")
	cmd := exec.Command("sc.exe", "stop", "GatewayAgent")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run() // ignore error if not running

	// Brief pause to let the service release the binary
	fmt.Println("Waiting for service to stop...")
	exec.Command("powershell", "-Command", "Start-Sleep -Seconds 2").Run()
}

// runInstaller extracts the embedded PowerShell scripts and runs the installer.
func runInstaller(installDir string, uninstallMode bool) {
	// 1. Ensure install directory exists
	binDir := filepath.Join(installDir, "bin")
	scriptsDir := filepath.Join(installDir, "scripts")
	os.MkdirAll(binDir, 0755)
	os.MkdirAll(scriptsDir, 0755)

	// 2. Copy this binary to the install location
	self, err := os.Executable()
	if err != nil {
		log.Fatalf("failed to find own executable: %v", err)
	}
	destBin := filepath.Join(binDir, "gateway-agent.exe")
	if normPath(self) != normPath(destBin) {
		log.Printf("Copying binary to %s", destBin)
		data, err := os.ReadFile(self)
		if err != nil {
			log.Fatalf("failed to read own binary: %v", err)
		}
		if err := os.WriteFile(destBin, data, 0755); err != nil {
			log.Fatalf("failed to write binary: %v", err)
		}
	}

	// 3. Extract embedded scripts
	launchPath := filepath.Join(scriptsDir, "session-launch.ps1")
	if err := os.WriteFile(launchPath, scripts.SessionLaunchScript, 0644); err != nil {
		log.Fatalf("failed to write session-launch.ps1: %v", err)
	}
	log.Printf("Extracted session-launch.ps1 to %s", launchPath)

	installerPath := filepath.Join(scriptsDir, "install-bastion.ps1")
	if err := os.WriteFile(installerPath, scripts.InstallScript, 0644); err != nil {
		log.Fatalf("failed to write install-bastion.ps1: %v", err)
	}
	log.Printf("Extracted install-bastion.ps1 to %s", installerPath)

	// 4. Run the installer
	args := []string{
		"-ExecutionPolicy", "Bypass",
		"-File", installerPath,
		"-InstallDir", installDir,
	}
	if uninstallMode {
		args = append(args, "-Uninstall")
	}

	log.Printf("Running: powershell %v", args)
	cmd := exec.Command("powershell.exe", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		log.Fatalf("installer failed: %v", err)
	}

	if !uninstallMode {
		fmt.Println()
		fmt.Println("Installation complete. Next steps:")
		fmt.Println("  1. Edit credentials:  notepad " + filepath.Join(installDir, "config", "credentials.json"))
		fmt.Println("  2. Start the service:  sc start GatewayAgent")
		fmt.Println("  3. Verify:             curl http://localhost:8080/health")
	}
}

// runUpgrade stops the GatewayAgent service, copies the current binary and
// embedded scripts to the install directory, and restarts the service.
func runUpgrade(installDir string) {
	binDir := filepath.Join(installDir, "bin")
	scriptsDir := filepath.Join(installDir, "scripts")

	stopService()

	// 1. Copy this binary
	self, err := os.Executable()
	if err != nil {
		log.Fatalf("failed to find own executable: %v", err)
	}
	destBin := filepath.Join(binDir, "gateway-agent.exe")
	if normPath(self) != normPath(destBin) {
		fmt.Printf("Copying binary to %s\n", destBin)
		data, err := os.ReadFile(self)
		if err != nil {
			log.Fatalf("failed to read own binary: %v", err)
		}
		if err := os.WriteFile(destBin, data, 0755); err != nil {
			log.Fatalf("failed to write binary: %v", err)
		}
	} else {
		fmt.Println("Binary already in install location, skipping copy")
	}

	// 3. Update embedded scripts
	launchPath := filepath.Join(scriptsDir, "session-launch.ps1")
	if err := os.WriteFile(launchPath, scripts.SessionLaunchScript, 0644); err != nil {
		log.Fatalf("failed to write session-launch.ps1: %v", err)
	}
	fmt.Printf("Updated %s\n", launchPath)

	installerPath := filepath.Join(scriptsDir, "install-bastion.ps1")
	if err := os.WriteFile(installerPath, scripts.InstallScript, 0644); err != nil {
		log.Fatalf("failed to write install-bastion.ps1: %v", err)
	}
	fmt.Printf("Updated %s\n", installerPath)

	// 4. Start the service
	fmt.Println("Starting GatewayAgent service...")
	start := exec.Command("sc", "start", "GatewayAgent")
	start.Stdout = os.Stdout
	start.Stderr = os.Stderr
	if err := start.Run(); err != nil {
		log.Fatalf("failed to start service: %v", err)
	}

	fmt.Println()
	fmt.Println("Upgrade complete!")
}

// runInteractive starts the agent in the foreground and blocks until a
// SIGINT or SIGTERM signal is received, then performs a graceful shutdown.
func runInteractive(configPath string) {
	agent, err := service.StartAgent(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to start agent: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nHave you run 'gateway-agent.exe --install' first?\n")
		waitForKeypress()
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	agent.Shutdown()
}

// normPath returns a cleaned, lowercased absolute path for comparison.
func normPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(abs)
}
