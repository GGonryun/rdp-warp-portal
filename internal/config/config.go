package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// GatewayHost returns the hostname to use in RDP file gateway settings.
// If GatewayHostname is configured, it is returned; otherwise os.Hostname() is used.
func (c *Config) GatewayHost() string {
	if c.GatewayHostname != "" {
		return c.GatewayHostname
	}
	h, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	return h
}

// Config represents the agent.json configuration file.
type Config struct {
	ListenAddr            string `json:"listen_addr"`
	RecordingsDir         string `json:"recordings_dir"`
	InstallDir            string `json:"install_dir"`
	CredentialsFile       string `json:"credentials_file"`
	UserPoolFile          string `json:"user_pool_file"`
	SessionScript         string `json:"session_script"`
	PortalExe             string `json:"portal_exe"`
	FFmpegPath            string `json:"ffmpeg_path"`
	MaxSessions           int    `json:"max_sessions"`
	SessionTimeoutMinutes int    `json:"session_timeout_minutes"`
	ReconnectGraceMinutes int    `json:"reconnect_grace_minutes"`
	LogFile               string `json:"log_file"`
	GatewayHostname       string `json:"gateway_hostname"`
}

// Load reads and parses the config from a JSON file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Strip UTF-8 BOM and \r characters that PowerShell's Out-File adds.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	data = bytes.ReplaceAll(data, []byte{'\r'}, nil)

	cfg := &Config{
		ListenAddr:            "0.0.0.0:8080",
		MaxSessions:           20,
		SessionTimeoutMinutes: 60,
		ReconnectGraceMinutes: 5,
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

// ListenPort extracts the port number from the ListenAddr.
func (c *Config) ListenPort() string {
	for i := len(c.ListenAddr) - 1; i >= 0; i-- {
		if c.ListenAddr[i] == ':' {
			return c.ListenAddr[i+1:]
		}
	}
	return "8080"
}
