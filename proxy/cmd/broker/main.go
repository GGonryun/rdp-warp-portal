// Package main is the entrypoint for the RDP broker service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/p0-security/rdp-broker/internal/acl"
	"github.com/p0-security/rdp-broker/internal/api"
	"github.com/p0-security/rdp-broker/internal/certs"
	"github.com/p0-security/rdp-broker/internal/config"
	"github.com/p0-security/rdp-broker/internal/credential"
	"github.com/p0-security/rdp-broker/internal/recording"
	"github.com/p0-security/rdp-broker/internal/secrets"
	"github.com/p0-security/rdp-broker/internal/session"
)

func main() {
	// Initialize logger
	logger := initLogger()
	slog.SetDefault(logger)

	logger.Info("starting RDP broker")

	// Cleanup orphaned processes from previous crashes
	cleanupOrphanedProcesses(logger)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	logger.Info("configuration loaded",
		"api_port", cfg.APIPort,
		"broker_host", cfg.BrokerHost,
		"proxy_port_range", fmt.Sprintf("%d-%d", cfg.ProxyPortStart, cfg.ProxyPortEnd),
		"credential_provider", cfg.CredentialProvider,
		"max_sessions", cfg.MaxConcurrentSessions,
	)

	// Initialize secrets client (optional — used for resolving secret-backed credentials)
	var secretClient *secrets.Client
	if cfg.WIFConfigured() {
		tokenProvider := secrets.NewTokenProvider(
			cfg.AzureCredentialURL,
			cfg.GCPWIFAudience,
			cfg.GCPServiceAccount,
			logger,
		)
		secretClient = secrets.NewClient(tokenProvider, logger)
		logger.Info("secrets client initialized (WIF configured)",
			"azure_credential_url", cfg.AzureCredentialURL,
			"gcp_service_account", cfg.GCPServiceAccount,
		)
	}

	// Initialize credential provider
	var secretResolver credential.SecretResolver
	if secretClient != nil {
		secretResolver = secretClient.AccessSecret
	}
	provider, err := newCredentialProvider(cfg, logger, secretResolver)
	if err != nil {
		logger.Error("failed to initialize credential provider", "error", err)
		os.Exit(1)
	}
	defer provider.Close()

	logger.Info("credential provider initialized",
		"type", cfg.CredentialProvider,
	)

	// Ensure TLS certificates exist
	certGen := certs.NewGenerator(cfg.CertDir)
	certPath, keyPath, err := certGen.EnsureCertificates()
	if err != nil {
		logger.Error("failed to ensure certificates", "error", err)
		os.Exit(1)
	}

	logger.Info("TLS certificates ready",
		"cert_path", certPath,
		"key_path", keyPath,
	)

	// Create port pool
	portPool := session.NewPortPool(
		cfg.ProxyPortStart,
		cfg.ProxyPortEnd,
		cfg.ProxyInternalOffset,
	)

	logger.Info("port pool initialized",
		"total_ports", portPool.Total(),
	)

	// Create session manager
	managerConfig := session.ManagerConfig{
		BrokerHost:            cfg.BrokerHost,
		BrokerDomain:          cfg.BrokerDomain,
		CertDir:               cfg.CertDir,
		SessionDir:            cfg.SessionDir,
		FreerdpProxyBin:       cfg.FreerdpProxyBin,
		MaxConcurrentSessions: cfg.MaxConcurrentSessions,
		SessionMaxDuration:    cfg.SessionMaxDuration,
		TokenTTL:              cfg.TokenTTL,
		Logger:                logger,
		LogOutput:             os.Stdout,
	}

	manager, err := session.NewManager(provider, portPool, managerConfig)
	if err != nil {
		logger.Error("failed to create session manager", "error", err)
		os.Exit(1)
	}

	logger.Info("session manager initialized")

	// Initialize ACL store.
	aclStore := acl.NewMemoryStore()
	logger.Info("ACL store initialized (in-memory)")

	// Create API router
	router := api.NewRouter(cfg.APIKey, logger)

	// Register handlers
	sessionsHandler := api.NewSessionsHandler(manager, cfg.BrokerHost, aclStore)
	sessionsHandler.RegisterRoutes(router)

	accessHandler := api.NewAccessHandler(aclStore, manager)
	accessHandler.RegisterRoutes(router)

	targetsHandler := api.NewTargetsHandler(provider)
	targetsHandler.RegisterRoutes(router)

	healthHandler := api.NewHealthHandler(manager)
	healthHandler.RegisterRoutes(router)

	recordingStore := recording.NewStore(cfg.RecordingsDir)
	recordingsHandler := api.NewRecordingsHandler(recordingStore)
	recordingsHandler.RegisterRoutes(router)

	// Register secrets handler (reuses secretClient created earlier)
	var secretsHandler *api.SecretsHandler
	if secretClient != nil {
		secretsHandler = api.NewSecretsHandler(secretClient, cfg.AzureCredentialURL, cfg.GCPServiceAccount)
	} else {
		secretsHandler = api.NewSecretsHandler(nil, cfg.AzureCredentialURL, cfg.GCPServiceAccount)
	}
	secretsHandler.RegisterRoutes(router)

	// Serve the web dashboard at /
	if cfg.WebDir != "" {
		router.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join(cfg.WebDir, "index.html"))
		}, false)
	}

	// Create HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.APIPort),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in background
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server",
			"addr", server.Addr,
		)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		logger.Info("received shutdown signal",
			"signal", sig.String(),
		)
	case err := <-serverErr:
		logger.Error("server error",
			"error", err,
		)
	}

	// Graceful shutdown
	logger.Info("initiating graceful shutdown")

	// Create shutdown context with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Shutdown session manager first (terminates all sessions)
	logger.Info("shutting down session manager")
	if err := manager.Shutdown(shutdownCtx); err != nil {
		logger.Error("session manager shutdown error",
			"error", err,
		)
	}

	// Shutdown HTTP server
	logger.Info("shutting down HTTP server")
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error",
			"error", err,
		)
	}

	logger.Info("shutdown complete")
}

// newCredentialProvider creates a credential provider based on configuration.
func newCredentialProvider(cfg *config.Config, logger *slog.Logger, resolver credential.SecretResolver) (credential.CredentialProvider, error) {
	logger.Info("using GSM credential provider")
	return credential.NewGSMProvider(cfg.CredentialProviderConfig, resolver)
}

// initLogger initializes the structured logger.
func initLogger() *slog.Logger {
	logLevel := os.Getenv("LOG_LEVEL")
	logFormat := os.Getenv("LOG_FORMAT")

	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if logFormat == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

// cleanupOrphanedProcesses kills any freerdp-proxy3 processes left over from previous broker crashes.
// This prevents port exhaustion and zombie processes on restart.
func cleanupOrphanedProcesses(logger *slog.Logger) {
	// Find freerdp-proxy3 processes using pgrep
	cmd := exec.Command("pgrep", "-f", "freerdp-proxy3")
	output, err := cmd.Output()
	if err != nil {
		// pgrep returns exit code 1 when no processes found - this is expected
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			logger.Debug("no orphaned freerdp-proxy3 processes found")
			return
		}
		// pgrep not available or other error - log and continue
		logger.Debug("could not check for orphaned processes", "error", err)
		return
	}

	// Parse PIDs and kill them
	pids := strings.Fields(strings.TrimSpace(string(output)))
	if len(pids) == 0 {
		return
	}

	logger.Warn("found orphaned freerdp-proxy3 processes from previous crash",
		"count", len(pids),
		"pids", pids,
	)

	killed := 0
	for _, pidStr := range pids {
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}

		// Send SIGTERM first for graceful shutdown
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			logger.Debug("failed to send SIGTERM to orphaned process",
				"pid", pid,
				"error", err,
			)
			continue
		}

		// Give it a moment to exit gracefully
		time.Sleep(100 * time.Millisecond)

		// Check if still running and send SIGKILL if needed
		if err := proc.Signal(syscall.Signal(0)); err == nil {
			// Process still exists, force kill
			_ = proc.Signal(syscall.SIGKILL)
		}

		killed++
	}

	if killed > 0 {
		logger.Info("cleaned up orphaned freerdp-proxy3 processes",
			"killed", killed,
		)
	}
}

