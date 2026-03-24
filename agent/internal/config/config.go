package config

import (
	"encoding/json"
	"os"
	"strconv"
)

// Config holds the agent configuration.
type Config struct {
	ProxyURL     string `json:"proxy_url"`     // e.g. "http://broker:8080"
	APIKey       string `json:"api_key"`       // Bearer token for broker API auth
	// TargetID is the proxy destination id (same as P0 permission resource.instanceId).
	// When set, recordings can be matched on the access request page even if the
	// Windows computer name differs from the configured target hostname.
	TargetID     string `json:"target_id"`
	FfmpegPath   string `json:"ffmpeg_path"`   // path to ffmpeg binary, default "ffmpeg"
	Framerate    int    `json:"framerate"`     // capture framerate, default 5
	ChunkSecs    int    `json:"chunk_secs"`    // segment duration seconds, default 30
	PollInterval    int `json:"poll_interval"`     // session poll interval seconds, default 5
	ResizePollMs    int `json:"resize_poll_ms"`    // resolution poll interval milliseconds, default 1000
}

func applyDefaults(cfg *Config) {
	if cfg.FfmpegPath == "" {
		cfg.FfmpegPath = "ffmpeg"
	}
	if cfg.Framerate <= 0 {
		cfg.Framerate = 5
	}
	if cfg.ChunkSecs <= 0 {
		cfg.ChunkSecs = 30
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5
	}
	if cfg.ResizePollMs <= 0 {
		cfg.ResizePollMs = 1000
	}
}

// Load reads configuration from a JSON file and applies defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// LoadFromEnv reads configuration from environment variables.
func LoadFromEnv() *Config {
	cfg := &Config{
		ProxyURL:   os.Getenv("PROXY_URL"),
		APIKey:     os.Getenv("API_KEY"),
		TargetID:   os.Getenv("TARGET_ID"),
		FfmpegPath: os.Getenv("FFMPEG_PATH"),
	}

	if v := os.Getenv("FRAMERATE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Framerate = n
		}
	}
	if v := os.Getenv("CHUNK_SECS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ChunkSecs = n
		}
	}
	if v := os.Getenv("POLL_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.PollInterval = n
		}
	}
	if v := os.Getenv("RESIZE_POLL_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ResizePollMs = n
		}
	}

	applyDefaults(cfg)
	return cfg
}
