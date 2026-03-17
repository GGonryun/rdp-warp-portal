package main

import (
	"context"
	"flag"
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

func main() {
	configPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	// Set up structured logging.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Load configuration.
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Warn("failed to load config file, falling back to env", "path", *configPath, "error", err)
		cfg = config.LoadFromEnv()
	}

	if cfg.ProxyURL == "" {
		slog.Error("proxy_url is required (set in config file or PROXY_URL env var)")
		os.Exit(1)
	}

	// Ensure ffmpeg is available (downloads if missing).
	ffmpegPath, err := ffmpeg.EnsureInstalled(cfg.FfmpegPath)
	if err != nil {
		slog.Error("ffmpeg is required but could not be found or installed", "error", err)
		os.Exit(1)
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

	// Create API client.
	apiClient := client.New(cfg.ProxyURL, cfg.APIKey)

	// Verify broker connectivity before starting.
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
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			lastErr = apiClient.HealthCheck(ctx)
			cancel()
			if lastErr == nil {
				slog.Info("broker connection established")
				break
			}
			if attempt >= len(delays) {
				slog.Error("failed to connect to broker after retries, exiting", "error", lastErr)
				os.Exit(1)
			}
			slog.Warn("broker not reachable, retrying...",
				"error", lastErr,
				"retry_in", delays[attempt].String(),
			)
			time.Sleep(delays[attempt])
		}
	}

	// Track active recorders by session ID.
	var mu sync.Mutex
	recorders := make(map[uint32]*capture.Recorder)

	// Set up context with signal handling.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Session logon callback.
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

	// Session logoff callback.
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

	// Create and run session detector.
	pollInterval := time.Duration(cfg.PollInterval) * time.Second
	detector := session.NewDetector(pollInterval, onLogon, onLogoff)

	slog.Info("session detector running, waiting for RDP sessions...")
	detector.Run(ctx)

	// Context was cancelled (signal received). Stop all active recorders.
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
}
