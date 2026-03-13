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
	case *install:
		runInstaller(*installDir, false)
		waitForKeypress()
	case *uninstall:
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
