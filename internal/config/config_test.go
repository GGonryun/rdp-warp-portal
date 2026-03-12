package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad_Success(t *testing.T) {
	os.Setenv("BROKER_HOST", "test.example.com")
	defer os.Unsetenv("BROKER_HOST")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.BrokerHost != "test.example.com" {
		t.Errorf("BrokerHost: got %q, want %q", cfg.BrokerHost, "test.example.com")
	}

	// Check hardcoded values
	if cfg.APIPort != 8080 {
		t.Errorf("APIPort: got %d, want %d", cfg.APIPort, 8080)
	}
	if cfg.ProxyPortStart != 33400 {
		t.Errorf("ProxyPortStart: got %d, want %d", cfg.ProxyPortStart, 33400)
	}
	if cfg.ProxyPortEnd != 33500 {
		t.Errorf("ProxyPortEnd: got %d, want %d", cfg.ProxyPortEnd, 33500)
	}
	if cfg.MaxConcurrentSessions != 100 {
		t.Errorf("MaxConcurrentSessions: got %d, want %d", cfg.MaxConcurrentSessions, 100)
	}
	if cfg.SessionMaxDuration != 8*time.Hour {
		t.Errorf("SessionMaxDuration: got %v, want %v", cfg.SessionMaxDuration, 8*time.Hour)
	}
	if cfg.TokenTTL != 5*time.Minute {
		t.Errorf("TokenTTL: got %v, want %v", cfg.TokenTTL, 5*time.Minute)
	}
}

func TestLoad_MissingBrokerHost(t *testing.T) {
	os.Unsetenv("BROKER_HOST")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing BROKER_HOST")
	}
}

func TestLoad_LogLevel(t *testing.T) {
	os.Setenv("BROKER_HOST", "test.example.com")
	os.Setenv("LOG_LEVEL", "debug")
	defer os.Unsetenv("BROKER_HOST")
	defer os.Unsetenv("LOG_LEVEL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel: got %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestLoad_LogLevelDefault(t *testing.T) {
	os.Setenv("BROKER_HOST", "test.example.com")
	os.Unsetenv("LOG_LEVEL")
	defer os.Unsetenv("BROKER_HOST")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel default: got %q, want %q", cfg.LogLevel, "info")
	}
}

func TestPortPoolSize(t *testing.T) {
	cfg := &Config{
		ProxyPortStart: 33400,
		ProxyPortEnd:   33500,
	}

	if cfg.PortPoolSize() != 101 {
		t.Errorf("PortPoolSize: got %d, want %d", cfg.PortPoolSize(), 101)
	}
}
