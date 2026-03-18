package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/p0-security/p0rtal-agent/internal/capture"
	"github.com/p0-security/p0rtal-agent/internal/client"
	"github.com/p0-security/p0rtal-agent/internal/config"
	"github.com/p0-security/p0rtal-agent/internal/ffmpeg"
	"github.com/p0-security/p0rtal-agent/internal/session"
)

const serviceName = "p0rtal"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			configPath := "config.json"
			if len(os.Args) > 2 {
				configPath = os.Args[2]
			}
			if err := installService(configPath); err != nil {
				fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "uninstall":
			if err := uninstallService(); err != nil {
				fmt.Fprintf(os.Stderr, "uninstall failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "reinstall":
			configPath := "config.json"
			if len(os.Args) > 2 {
				configPath = os.Args[2]
			}
			if err := reinstallService(configPath); err != nil {
				fmt.Fprintf(os.Stderr, "reinstall failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "start":
			if err := startService(); err != nil {
				fmt.Fprintf(os.Stderr, "start failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "stop":
			if err := stopService(); err != nil {
				fmt.Fprintf(os.Stderr, "stop failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "status":
			if err := queryService(); err != nil {
				fmt.Fprintf(os.Stderr, "status failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "log", "logs":
			if err := tailLogs(); err != nil {
				fmt.Fprintf(os.Stderr, "log failed: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	// Detect if running as a Windows service or interactively.
	if isWindowsService() {
		runAsService()
		return
	}

	// Interactive mode.
	configPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := runAgent(ctx, *configPath); err != nil {
		slog.Error("agent failed", "error", err)
		os.Exit(1)
	}
}

// runAgent is the core agent logic shared by both interactive and service mode.
func runAgent(ctx context.Context, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Warn("failed to load config file, falling back to env", "path", configPath, "error", err)
		cfg = config.LoadFromEnv()
	}

	if cfg.ProxyURL == "" {
		return fmt.Errorf("proxy_url is required (set in config file or PROXY_URL env var)")
	}

	ffmpegPath, err := ffmpeg.EnsureInstalled(cfg.FfmpegPath)
	if err != nil {
		return fmt.Errorf("ffmpeg is required but could not be found or installed: %w", err)
	}
	cfg.FfmpegPath = ffmpegPath

	hostname, _ := os.Hostname()
	slog.Info("p0rtal agent starting",
		"hostname", hostname,
		"proxy_url", cfg.ProxyURL,
		"framerate", cfg.Framerate,
		"chunk_secs", cfg.ChunkSecs,
		"poll_interval", cfg.PollInterval,
	)

	apiClient := client.New(cfg.ProxyURL, cfg.APIKey)

	// Verify broker connectivity.
	slog.Info("connecting to broker...", "proxy_url", cfg.ProxyURL)
	{
		delays := []time.Duration{
			1 * time.Second,
			2 * time.Second,
			4 * time.Second,
			8 * time.Second,
			15 * time.Second,
			30 * time.Second,
		}
		var lastErr error
		for attempt := 0; ; attempt++ {
			hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			lastErr = apiClient.HealthCheck(hctx)
			cancel()
			if lastErr == nil {
				slog.Info("broker connection established")
				break
			}
			if attempt >= len(delays) {
				return fmt.Errorf("failed to connect to broker after retries: %w", lastErr)
			}
			// Check if we were asked to stop.
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			slog.Warn("broker not reachable, retrying...",
				"error", lastErr,
				"retry_in", delays[attempt].String(),
			)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delays[attempt]):
			}
		}
	}

	var mu sync.Mutex
	recorders := make(map[uint32]*capture.Recorder)

	onLogon := func(info session.SessionInfo) {
		slog.Info("starting recording for session",
			"session_id", info.ID,
			"user", info.User,
			"client_ip", info.ClientIP,
		)

		rec := capture.NewRecorder(apiClient, cfg)
		if err := rec.Start(ctx, info); err != nil {
			slog.Error("failed to start recording",
				"session_id", info.ID,
				"error", err,
			)
			return
		}

		mu.Lock()
		recorders[info.ID] = rec
		mu.Unlock()
	}

	onLogoff := func(info session.SessionInfo) {
		slog.Info("stopping recording for session",
			"session_id", info.ID,
			"user", info.User,
		)

		mu.Lock()
		rec, ok := recorders[info.ID]
		if ok {
			delete(recorders, info.ID)
		}
		mu.Unlock()

		if ok {
			if err := rec.Stop(); err != nil {
				slog.Error("failed to stop recording",
					"session_id", info.ID,
					"error", err,
				)
			}
		}
	}

	pollInterval := time.Duration(cfg.PollInterval) * time.Second
	detector := session.NewDetector(pollInterval, onLogon, onLogoff)

	slog.Info("session detector running, waiting for RDP sessions...")
	detector.Run(ctx)

	// Shutdown: stop all active recorders.
	slog.Info("shutting down, stopping all active recorders...")

	mu.Lock()
	activeRecorders := make(map[uint32]*capture.Recorder, len(recorders))
	for k, v := range recorders {
		activeRecorders[k] = v
	}
	mu.Unlock()

	for id, rec := range activeRecorders {
		slog.Info("stopping recorder", "session_id", id)
		if err := rec.Stop(); err != nil {
			slog.Error("failed to stop recorder", "session_id", id, "error", err)
		}
	}

	slog.Info("p0rtal agent stopped")
	return nil
}
