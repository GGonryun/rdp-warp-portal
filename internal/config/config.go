// Package config provides configuration management for the RDP broker.
//
// Configuration is loaded from environment variables with sensible defaults.
// Use Load() to read all configuration at application startup.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all configuration for the RDP broker.
type Config struct {
	// API settings
	APIPort      int    // Port for the REST API (default: 8080)
	APIJWTSecret string // Secret key for JWT validation

	// Broker settings
	BrokerHost   string // Public hostname for RDP connections (e.g., "broker.example.com")
	BrokerDomain string // Domain shown in RDP file (cosmetic, e.g., "YOURORG")

	// Proxy port pool settings
	ProxyPortStart      int // First port in the pool (default: 33400)
	ProxyPortEnd        int // Last port in the pool (default: 33500)
	ProxyInternalOffset int // Offset for internal proxy ports (default: 11000)

	// Credential provider settings
	CredentialProvider       string // Provider type: "mock", "vault", etc. (default: "mock")
	CredentialProviderConfig string // Path to provider-specific config file (optional)

	// Paths
	CertDir         string // Directory for TLS certificates (default: "/etc/rdp-broker/certs")
	SessionDir      string // Directory for session data on tmpfs (default: "/tmp/sessions")
	FreerdpProxyBin string // Path to freerdp-proxy3 binary (default: "freerdp-proxy3")

	// Session policy
	MaxConcurrentSessions int           // Maximum number of concurrent sessions (default: 100)
	SessionMaxDuration    time.Duration // Maximum session duration (default: 8h)
	TokenTTL              time.Duration // Token validity period (default: 60s)

	// Logging
	LogLevel    string // Log level: "debug", "info", "warn", "error" (default: "info")
	LogFormat   string // Log format: "json", "text" (default: "json")
	AuditLogFile string // Path to audit log file (default: "")
}

// Load reads configuration from environment variables with defaults.
func Load() (*Config, error) {
	cfg := &Config{
		// API defaults
		APIPort:      getEnvInt("API_PORT", 8080),
		APIJWTSecret: getEnvString("API_JWT_SECRET", ""),

		// Broker defaults
		BrokerHost:   getEnvString("BROKER_HOST", "localhost"),
		BrokerDomain: getEnvString("BROKER_DOMAIN", "YOURORG"),

		// Port pool defaults
		ProxyPortStart:      getEnvInt("PROXY_PORT_START", 33400),
		ProxyPortEnd:        getEnvInt("PROXY_PORT_END", 33500),
		ProxyInternalOffset: getEnvInt("PROXY_INTERNAL_OFFSET", 11000),

		// Credential provider defaults
		CredentialProvider:       getEnvString("CREDENTIAL_PROVIDER", "mock"),
		CredentialProviderConfig: getEnvString("CREDENTIAL_PROVIDER_CONFIG", ""),

		// Path defaults
		CertDir:         getEnvString("CERT_DIR", "/etc/rdp-broker/certs"),
		SessionDir:      getEnvString("SESSION_DIR", "/tmp/sessions"),
		FreerdpProxyBin: getEnvString("FREERDP_PROXY_BIN", "freerdp-proxy"),

		// Session policy defaults
		MaxConcurrentSessions: getEnvInt("MAX_CONCURRENT_SESSIONS", 100),
		SessionMaxDuration:    getEnvDuration("SESSION_MAX_DURATION", 8*time.Hour),
		TokenTTL:              getEnvDuration("TOKEN_TTL", 60*time.Second),

		// Logging defaults
		LogLevel:     getEnvString("LOG_LEVEL", "info"),
		LogFormat:    getEnvString("LOG_FORMAT", "json"),
		AuditLogFile: getEnvString("AUDIT_LOG_FILE", ""),
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if c.APIPort < 1 || c.APIPort > 65535 {
		return fmt.Errorf("invalid API_PORT: %d (must be 1-65535)", c.APIPort)
	}

	if c.ProxyPortStart < 1024 || c.ProxyPortStart > 65535 {
		return fmt.Errorf("invalid PROXY_PORT_START: %d (must be 1024-65535)", c.ProxyPortStart)
	}

	if c.ProxyPortEnd < c.ProxyPortStart || c.ProxyPortEnd > 65535 {
		return fmt.Errorf("invalid PROXY_PORT_END: %d (must be >= %d and <= 65535)", c.ProxyPortEnd, c.ProxyPortStart)
	}

	if c.ProxyInternalOffset < 0 {
		return fmt.Errorf("invalid PROXY_INTERNAL_OFFSET: %d (must be >= 0)", c.ProxyInternalOffset)
	}

	// Check that internal ports don't exceed valid range
	maxInternalPort := c.ProxyPortEnd + c.ProxyInternalOffset
	if maxInternalPort > 65535 {
		return fmt.Errorf("internal port range exceeds 65535: PROXY_PORT_END(%d) + PROXY_INTERNAL_OFFSET(%d) = %d",
			c.ProxyPortEnd, c.ProxyInternalOffset, maxInternalPort)
	}

	if c.BrokerHost == "" {
		return fmt.Errorf("BROKER_HOST cannot be empty")
	}

	if c.MaxConcurrentSessions < 1 {
		return fmt.Errorf("invalid MAX_CONCURRENT_SESSIONS: %d (must be >= 1)", c.MaxConcurrentSessions)
	}

	if c.SessionMaxDuration < time.Minute {
		return fmt.Errorf("invalid SESSION_MAX_DURATION: %s (must be >= 1m)", c.SessionMaxDuration)
	}

	if c.TokenTTL < time.Second {
		return fmt.Errorf("invalid TOKEN_TTL: %s (must be >= 1s)", c.TokenTTL)
	}

	return nil
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

// getEnvInt returns the environment variable as an int or the default.
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// getEnvDuration returns the environment variable as a duration or the default.
func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}
