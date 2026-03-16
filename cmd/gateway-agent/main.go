package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/p0rtal-4/p0rtal/credprov"
	"github.com/p0rtal-4/p0rtal/internal/service"
	"github.com/p0rtal-4/p0rtal/portal"
	"github.com/p0rtal-4/p0rtal/scripts"
)

func main() {
	configPath := flag.String("config", `C:\Gateway\config\agent.json`, "Path to config file")
	install := flag.Bool("install", false, "Install and configure the bastion server (run as Administrator)")
	uninstall := flag.Bool("uninstall", false, "Uninstall the bastion configuration")
	upgrade := flag.Bool("upgrade", false, "Stop service, update binary + scripts, restart service")
	start := flag.Bool("start", false, "Start the GatewayAgent service")
	stop := flag.Bool("stop", false, "Stop the GatewayAgent service")
	status := flag.Bool("status", false, "Show GatewayAgent service status")
	test := flag.Bool("test", false, "Launch portal.exe with a static test session config")
	clean := flag.Bool("clean", false, "Delete all recordings and log files")
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
	case *start:
		startService()
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
	case *test:
		runTestPortal(*installDir)
		waitForKeypress()
	case *clean:
		runClean(*installDir)
		waitForKeypress()
	case noFlagsProvided():
		showUsage()
		waitForKeypress()
	default:
		runInteractive(*configPath)
	}
}

func noFlagsProvided() bool {
	return len(os.Args) == 1
}

func showUsage() {
	fmt.Println("========================================")
	fmt.Println("  P0rtal Gateway Agent")
	fmt.Println("========================================")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  gateway-agent.exe --install       Install and configure the bastion (run as Admin)")
	fmt.Println("  gateway-agent.exe --upgrade       Stop service, update files, restart")
	fmt.Println("  gateway-agent.exe --status        Show service status")
	fmt.Println("  gateway-agent.exe --start         Start the GatewayAgent service")
	fmt.Println("  gateway-agent.exe --stop          Stop the GatewayAgent service")
	fmt.Println("  gateway-agent.exe --uninstall     Remove bastion configuration")
	fmt.Println("  gateway-agent.exe --test          Launch portal.exe with a test session")
	fmt.Println("  gateway-agent.exe --clean         Delete all recordings and log files")
	fmt.Println("  gateway-agent.exe --config PATH   Run interactively with a config file")
	fmt.Println()
	fmt.Println("First time? Run:  gateway-agent.exe --install")
	fmt.Println()
}

func waitForKeypress() {
	fmt.Println()
	fmt.Print("Press Enter to close...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

// ---------------------------------------------------------------------------
// Service management helpers
// ---------------------------------------------------------------------------

func showStatus() {
	cmd := exec.Command("sc.exe", "query", "GatewayAgent")
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Println("GatewayAgent service is not installed.")
		return
	}
	fmt.Println(string(output))

	cfg := exec.Command("sc.exe", "qc", "GatewayAgent")
	cfgOutput, err := cfg.CombinedOutput()
	if err == nil {
		fmt.Println(string(cfgOutput))
	}
}

func startService() {
	fmt.Println("Starting GatewayAgent service...")
	cmd := exec.Command("sc.exe", "start", "GatewayAgent")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Failed to start service: %v\n", err)
	}
}

func stopService() {
	fmt.Println("Stopping GatewayAgent service...")
	cmd := exec.Command("sc.exe", "stop", "GatewayAgent")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run() // ignore error if not running

	fmt.Println("Waiting for service to stop...")
	exec.Command("powershell", "-Command", "Start-Sleep -Seconds 2").Run()
}

// ---------------------------------------------------------------------------
// Install / upgrade
// ---------------------------------------------------------------------------

// runInstaller extracts all embedded files and runs install-bastion.ps1.
func runInstaller(installDir string, uninstallMode bool) {
	// 1. Create directory structure
	binDir := filepath.Join(installDir, "bin")
	scriptsDir := filepath.Join(installDir, "scripts")
	credprovDir := filepath.Join(installDir, "credprov")
	portalDir := filepath.Join(installDir, "portal")
	for _, d := range []string{binDir, scriptsDir, credprovDir, portalDir} {
		os.MkdirAll(d, 0755)
	}

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

	// 4. Extract embedded portal source
	extractEmbeddedFS(portal.Source, portalDir)
	log.Printf("Extracted portal source to %s", portalDir)

	// 5. Extract embedded credprov source
	extractEmbeddedFS(credprov.Source, credprovDir)
	log.Printf("Extracted credprov source to %s", credprovDir)

	// 6. Run the installer script
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

// runUpgrade stops the service, updates all deployed files, and restarts.
func runUpgrade(installDir string) {
	binDir := filepath.Join(installDir, "bin")
	scriptsDir := filepath.Join(installDir, "scripts")
	credprovDir := filepath.Join(installDir, "credprov")
	portalDir := filepath.Join(installDir, "portal")

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

	// 2. Update embedded scripts
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

	// 3. Update embedded portal and credprov source
	extractEmbeddedFS(portal.Source, portalDir)
	fmt.Printf("Updated portal source in %s\n", portalDir)
	extractEmbeddedFS(credprov.Source, credprovDir)
	fmt.Printf("Updated credprov source in %s\n", credprovDir)

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

// ---------------------------------------------------------------------------
// Clean — delete recordings and logs
// ---------------------------------------------------------------------------

func runClean(installDir string) {
	dirs := []struct {
		path string
		name string
	}{
		{filepath.Join(installDir, "recordings"), "recordings"},
		{filepath.Join(installDir, "logs"), "logs"},
	}

	for _, d := range dirs {
		entries, err := os.ReadDir(d.path)
		if os.IsNotExist(err) {
			fmt.Printf("%s: directory does not exist, skipping\n", d.name)
			continue
		}
		if err != nil {
			fmt.Printf("%s: error reading directory: %v\n", d.name, err)
			continue
		}
		if len(entries) == 0 {
			fmt.Printf("%s: already empty\n", d.name)
			continue
		}

		count := len(entries)
		fmt.Printf("%s: found %d items, deleting...\n", d.name, count)
		for _, entry := range entries {
			p := filepath.Join(d.path, entry.Name())
			if err := os.RemoveAll(p); err != nil {
				fmt.Printf("  failed to delete %s: %v\n", entry.Name(), err)
			}
		}
		fmt.Printf("%s: cleaned (%d items removed)\n", d.name, count)
	}

	fmt.Println()
	fmt.Println("Cleanup complete.")
}

// ---------------------------------------------------------------------------
// Test mode — launch portal.exe with a static session config
// ---------------------------------------------------------------------------

func runTestPortal(installDir string) {
	portalExe := filepath.Join(installDir, "bin", "portal.exe")
	if _, err := os.Stat(portalExe); os.IsNotExist(err) {
		fmt.Println("ERROR: portal.exe not found. Run --install or --upgrade first.")
		return
	}

	// Load first target from credentials.json
	credsPath := filepath.Join(installDir, "config", "credentials.json")
	credsData, err := os.ReadFile(credsPath)
	if err != nil {
		log.Fatalf("failed to read credentials file %s: %v", credsPath, err)
	}

	var credsFile struct {
		Targets []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Username string `json:"username"`
			Password string `json:"password"`
			Domain   string `json:"domain"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(credsData, &credsFile); err != nil {
		log.Fatalf("failed to parse credentials file: %v", err)
	}
	if len(credsFile.Targets) == 0 {
		log.Fatalf("no targets found in %s", credsPath)
	}

	target := credsFile.Targets[0]
	fmt.Printf("Using target: %s (%s:%d as %s)\n", target.Name, target.Host, target.Port, target.Username)

	recordingDir := filepath.Join(installDir, "recordings", "test_static_001")
	os.MkdirAll(recordingDir, 0755)

	testCfg := map[string]interface{}{
		"session_id":    "test_static_001",
		"target_host":   target.Host,
		"target_port":   target.Port,
		"target_user":   target.Username,
		"target_pass":   target.Password,
		"target_domain": target.Domain,
		"recording_dir": recordingDir,
		"ffmpeg_path":   filepath.Join(installDir, "bin", "ffmpeg.exe"),
		"callback_url":  "http://localhost:8080",
	}
	config, err := json.MarshalIndent(testCfg, "", "  ")
	if err != nil {
		log.Fatalf("failed to marshal test config: %v", err)
	}

	configPath := filepath.Join(installDir, "test-session-config.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		log.Fatalf("failed to write test config: %v", err)
	}
	fmt.Printf("Test config written to %s\n", configPath)

	// Launch portal.exe
	fmt.Println("Launching portal.exe...")
	cmd := exec.Command(portalExe, "-ConfigPath", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("portal.exe exited with error: %v\n", err)
	} else {
		fmt.Println("portal.exe exited normally")
	}
}

// ---------------------------------------------------------------------------
// Interactive (foreground) mode
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// extractEmbeddedFS writes all files from an embed.FS to the given directory,
// preserving the directory structure.
func extractEmbeddedFS(fsys embed.FS, destDir string) {
	fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(destDir, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(dest, 0755)
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			log.Fatalf("failed to read embedded file %s: %v", path, err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			log.Fatalf("failed to write %s: %v", dest, err)
		}
		return nil
	})
}

func normPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(abs)
}
