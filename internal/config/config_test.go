package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear any environment variables that might affect the test
	envVars := []string{
		"API_PORT", "API_JWT_SECRET", "BROKER_HOST", "BROKER_DOMAIN",
		"PROXY_PORT_START", "PROXY_PORT_END", "PROXY_INTERNAL_OFFSET",
		"CREDENTIAL_PROVIDER", "CREDENTIAL_PROVIDER_CONFIG",
		"CERT_DIR", "SESSION_DIR", "FREERDP_PROXY_BIN",
		"MAX_CONCURRENT_SESSIONS", "SESSION_MAX_DURATION", "TOKEN_TTL",
		"LOG_LEVEL", "LOG_FORMAT", "AUDIT_LOG_FILE",
	}
	for _, key := range envVars {
		os.Unsetenv(key)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Check defaults
	tests := []struct {
		name     string
		got      interface{}
		expected interface{}
	}{
		{"APIPort", cfg.APIPort, 8080},
		{"APIJWTSecret", cfg.APIJWTSecret, ""},
		{"BrokerHost", cfg.BrokerHost, "localhost"},
		{"BrokerDomain", cfg.BrokerDomain, "YOURORG"},
		{"ProxyPortStart", cfg.ProxyPortStart, 33400},
		{"ProxyPortEnd", cfg.ProxyPortEnd, 33500},
		{"ProxyInternalOffset", cfg.ProxyInternalOffset, 11000},
		{"CredentialProvider", cfg.CredentialProvider, "mock"},
		{"CredentialProviderConfig", cfg.CredentialProviderConfig, ""},
		{"CertDir", cfg.CertDir, "/etc/rdp-broker/certs"},
		{"SessionDir", cfg.SessionDir, "/tmp/sessions"},
		{"FreerdpProxyBin", cfg.FreerdpProxyBin, "freerdp-proxy"},
		{"MaxConcurrentSessions", cfg.MaxConcurrentSessions, 100},
		{"SessionMaxDuration", cfg.SessionMaxDuration, 8 * time.Hour},
		{"TokenTTL", cfg.TokenTTL, 60 * time.Second},
		{"LogLevel", cfg.LogLevel, "info"},
		{"LogFormat", cfg.LogFormat, "json"},
		{"AuditLogFile", cfg.AuditLogFile, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %v, want %v", tt.got, tt.expected)
			}
		})
	}
}

func TestLoad_FromEnv(t *testing.T) {
	// Set custom environment variables
	os.Setenv("API_PORT", "9090")
	os.Setenv("API_JWT_SECRET", "my-secret-key")
	os.Setenv("BROKER_HOST", "rdp.example.com")
	os.Setenv("BROKER_DOMAIN", "EXAMPLE")
	os.Setenv("PROXY_PORT_START", "40000")
	os.Setenv("PROXY_PORT_END", "40100")
	os.Setenv("PROXY_INTERNAL_OFFSET", "5000")
	os.Setenv("CREDENTIAL_PROVIDER", "vault")
	os.Setenv("CREDENTIAL_PROVIDER_CONFIG", "/etc/vault.json")
	os.Setenv("MAX_CONCURRENT_SESSIONS", "50")
	os.Setenv("SESSION_MAX_DURATION", "4h")
	os.Setenv("TOKEN_TTL", "30s")
	os.Setenv("LOG_LEVEL", "debug")

	// Clean up after test
	defer func() {
		os.Unsetenv("API_PORT")
		os.Unsetenv("API_JWT_SECRET")
		os.Unsetenv("BROKER_HOST")
		os.Unsetenv("BROKER_DOMAIN")
		os.Unsetenv("PROXY_PORT_START")
		os.Unsetenv("PROXY_PORT_END")
		os.Unsetenv("PROXY_INTERNAL_OFFSET")
		os.Unsetenv("CREDENTIAL_PROVIDER")
		os.Unsetenv("CREDENTIAL_PROVIDER_CONFIG")
		os.Unsetenv("MAX_CONCURRENT_SESSIONS")
		os.Unsetenv("SESSION_MAX_DURATION")
		os.Unsetenv("TOKEN_TTL")
		os.Unsetenv("LOG_LEVEL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	tests := []struct {
		name     string
		got      interface{}
		expected interface{}
	}{
		{"APIPort", cfg.APIPort, 9090},
		{"APIJWTSecret", cfg.APIJWTSecret, "my-secret-key"},
		{"BrokerHost", cfg.BrokerHost, "rdp.example.com"},
		{"BrokerDomain", cfg.BrokerDomain, "EXAMPLE"},
		{"ProxyPortStart", cfg.ProxyPortStart, 40000},
		{"ProxyPortEnd", cfg.ProxyPortEnd, 40100},
		{"ProxyInternalOffset", cfg.ProxyInternalOffset, 5000},
		{"CredentialProvider", cfg.CredentialProvider, "vault"},
		{"CredentialProviderConfig", cfg.CredentialProviderConfig, "/etc/vault.json"},
		{"MaxConcurrentSessions", cfg.MaxConcurrentSessions, 50},
		{"SessionMaxDuration", cfg.SessionMaxDuration, 4 * time.Hour},
		{"TokenTTL", cfg.TokenTTL, 30 * time.Second},
		{"LogLevel", cfg.LogLevel, "debug"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %v, want %v", tt.got, tt.expected)
			}
		})
	}
}

func TestLoad_InvalidEnvValues(t *testing.T) {
	// Test with invalid integer - should use default
	os.Setenv("API_PORT", "not-a-number")
	defer os.Unsetenv("API_PORT")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.APIPort != 8080 {
		t.Errorf("expected default API_PORT 8080, got %d", cfg.APIPort)
	}
}

func TestValidate_InvalidAPIPort(t *testing.T) {
	cfg := &Config{
		APIPort:               0, // Invalid
		BrokerHost:            "localhost",
		ProxyPortStart:        33400,
		ProxyPortEnd:          33500,
		ProxyInternalOffset:   11000,
		MaxConcurrentSessions: 100,
		SessionMaxDuration:    8 * time.Hour,
		TokenTTL:              60 * time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for invalid API_PORT")
	}
}

func TestValidate_InvalidProxyPortStart(t *testing.T) {
	cfg := &Config{
		APIPort:               8080,
		BrokerHost:            "localhost",
		ProxyPortStart:        100, // Invalid - below 1024
		ProxyPortEnd:          33500,
		ProxyInternalOffset:   11000,
		MaxConcurrentSessions: 100,
		SessionMaxDuration:    8 * time.Hour,
		TokenTTL:              60 * time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for invalid PROXY_PORT_START")
	}
}

func TestValidate_InvalidProxyPortEnd(t *testing.T) {
	cfg := &Config{
		APIPort:               8080,
		BrokerHost:            "localhost",
		ProxyPortStart:        33500,
		ProxyPortEnd:          33400, // Invalid - less than start
		ProxyInternalOffset:   11000,
		MaxConcurrentSessions: 100,
		SessionMaxDuration:    8 * time.Hour,
		TokenTTL:              60 * time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for invalid PROXY_PORT_END")
	}
}

func TestValidate_InternalPortOverflow(t *testing.T) {
	cfg := &Config{
		APIPort:               8080,
		BrokerHost:            "localhost",
		ProxyPortStart:        60000,
		ProxyPortEnd:          60100,
		ProxyInternalOffset:   10000, // Would overflow to 70100
		MaxConcurrentSessions: 100,
		SessionMaxDuration:    8 * time.Hour,
		TokenTTL:              60 * time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for internal port overflow")
	}
}

func TestValidate_EmptyBrokerHost(t *testing.T) {
	cfg := &Config{
		APIPort:               8080,
		BrokerHost:            "", // Invalid
		ProxyPortStart:        33400,
		ProxyPortEnd:          33500,
		ProxyInternalOffset:   11000,
		MaxConcurrentSessions: 100,
		SessionMaxDuration:    8 * time.Hour,
		TokenTTL:              60 * time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for empty BROKER_HOST")
	}
}

func TestValidate_InvalidMaxConcurrentSessions(t *testing.T) {
	cfg := &Config{
		APIPort:               8080,
		BrokerHost:            "localhost",
		ProxyPortStart:        33400,
		ProxyPortEnd:          33500,
		ProxyInternalOffset:   11000,
		MaxConcurrentSessions: 0, // Invalid
		SessionMaxDuration:    8 * time.Hour,
		TokenTTL:              60 * time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for invalid MAX_CONCURRENT_SESSIONS")
	}
}

func TestValidate_InvalidSessionMaxDuration(t *testing.T) {
	cfg := &Config{
		APIPort:               8080,
		BrokerHost:            "localhost",
		ProxyPortStart:        33400,
		ProxyPortEnd:          33500,
		ProxyInternalOffset:   11000,
		MaxConcurrentSessions: 100,
		SessionMaxDuration:    30 * time.Second, // Invalid - less than 1 minute
		TokenTTL:              60 * time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for invalid SESSION_MAX_DURATION")
	}
}

func TestValidate_InvalidTokenTTL(t *testing.T) {
	cfg := &Config{
		APIPort:               8080,
		BrokerHost:            "localhost",
		ProxyPortStart:        33400,
		ProxyPortEnd:          33500,
		ProxyInternalOffset:   11000,
		MaxConcurrentSessions: 100,
		SessionMaxDuration:    8 * time.Hour,
		TokenTTL:              500 * time.Millisecond, // Invalid - less than 1 second
	}

	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for invalid TOKEN_TTL")
	}
}

func TestPortPoolSize(t *testing.T) {
	cfg := &Config{
		ProxyPortStart: 33400,
		ProxyPortEnd:   33500,
	}

	size := cfg.PortPoolSize()
	if size != 101 {
		t.Errorf("expected port pool size 101, got %d", size)
	}
}

func TestPortPoolSize_Single(t *testing.T) {
	cfg := &Config{
		ProxyPortStart: 33400,
		ProxyPortEnd:   33400,
	}

	size := cfg.PortPoolSize()
	if size != 1 {
		t.Errorf("expected port pool size 1, got %d", size)
	}
}
