// Package config provides configuration management for the RDP broker.
package config

import (
	"fmt"
	"os"
	"time"
)

// Config holds all configuration for the RDP broker.
type Config struct {
	// Broker settings (from environment)
	BrokerHost string // Public hostname for RDP connections
	APIKey     string // API key for authenticating requests (empty = no auth)
	LogLevel   string // Log level: "debug", "info", "warn", "error"

	// Hardcoded settings
	APIPort                  int
	BrokerDomain             string
	ProxyPortStart           int
	ProxyPortEnd             int
	ProxyInternalOffset      int
	CredentialProvider       string
	CredentialProviderConfig string
	CertDir                  string
	SessionDir               string
	RecordingsDir            string
	WebDir                   string
	FreerdpProxyBin          string
	MaxConcurrentSessions    int
	SessionMaxDuration       time.Duration
	TokenTTL                 time.Duration
	LogFormat                string
}

// Load reads configuration from environment variables with defaults.
func Load() (*Config, error) {
	cfg := &Config{
		// From environment
		BrokerHost: os.Getenv("BROKER_HOST"),
		APIKey:     os.Getenv("API_KEY"),
		LogLevel:   getEnvString("LOG_LEVEL", "info"),

		// Hardcoded
		APIPort:                  8080,
		BrokerDomain:             "P0RTAL",
		ProxyPortStart:           33400,
		ProxyPortEnd:             33500,
		ProxyInternalOffset:      11000,
		CredentialProvider:       "mock",
		CredentialProviderConfig: "/etc/rdp-broker/targets.json",
		CertDir:                  "/etc/rdp-broker/certs",
		SessionDir:               "/tmp/sessions",
		RecordingsDir:            getEnvString("RECORDINGS_DIR", "/var/lib/p0rtal/recordings"),
		WebDir:                   getEnvString("WEB_DIR", "/usr/share/p0rtal/web"),
		FreerdpProxyBin:          "freerdp-proxy",
		MaxConcurrentSessions:    100,
		SessionMaxDuration:       8 * time.Hour,
		TokenTTL:                 5 * time.Minute,
		LogFormat:                "json",
	}

	if cfg.BrokerHost == "" {
		return nil, fmt.Errorf("BROKER_HOST environment variable is required")
	}

	return cfg, nil
}

// PortPoolSize returns the number of ports available in the pool.
func (c *Config) PortPoolSize() int {
	return c.ProxyPortEnd - c.ProxyPortStart + 1
}

// getEnvString returns the environment variable value or the default.
func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
