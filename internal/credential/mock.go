package credential

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// mockTarget holds both public info and private credentials for a target.
type mockTarget struct {
	Info        TargetInfo
	Credentials TargetCredentials
}

// MockProvider is an in-memory credential provider for development and testing.
//
// It stores a fixed set of targets with their credentials and returns them
// on demand. All methods are safe for concurrent use.
//
// Use NewMockProvider() for default hardcoded targets, or
// NewMockProviderFromConfig() to load targets from a JSON file.
type MockProvider struct {
	mu      sync.RWMutex
	targets map[string]mockTarget
}

// Compile-time check that MockProvider implements CredentialProvider.
var _ CredentialProvider = (*MockProvider)(nil)

// NewMockProvider creates a MockProvider with default hardcoded targets.
//
// The default targets are:
//   - dc-01: Domain Controller 01 (10.0.1.10)
//   - ws-05: Workstation 05 (10.0.1.50)
//   - win-vm-1: Azure Windows VM (20.64.171.136)
//
// This is useful for quick development and testing without configuration files.
func NewMockProvider() *MockProvider {
	return &MockProvider{
		targets: map[string]mockTarget{
			"dc-01": {
				Info: TargetInfo{
					ID:       "dc-01",
					Name:     "Domain Controller 01",
					Hostname: "10.0.1.10",
				},
				Credentials: TargetCredentials{
					Hostname: "10.0.1.10",
					Port:     3389,
					Username: "Administrator",
					Password: "P@ssw0rd!",
					Domain:   "CORP",
				},
			},
			"ws-05": {
				Info: TargetInfo{
					ID:       "ws-05",
					Name:     "Workstation 05",
					Hostname: "10.0.1.50",
				},
				Credentials: TargetCredentials{
					Hostname: "10.0.1.50",
					Port:     3389,
					Username: "svc-rdp",
					Password: "Sup3rS3cret",
					Domain:   "CORP",
				},
			},
			"win-vm-1": {
				Info: TargetInfo{
					ID:       "win-vm-1",
					Name:     "Azure Windows VM",
					Hostname: "20.64.171.136",
				},
				Credentials: TargetCredentials{
					Hostname: "20.64.171.136",
					Port:     3389,
					Username: "rdpadmin",
					Password: "CHANGE_ME_BEFORE_DEPLOY",
					Domain:   "",
				},
			},
		},
	}
}

// mockConfigFile represents the JSON structure of the config file.
type mockConfigFile struct {
	Targets map[string]mockConfigTarget `json:"targets"`
}

// mockConfigTarget represents a single target in the config file.
type mockConfigTarget struct {
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Domain   string `json:"domain"`
}

// NewMockProviderFromConfig creates a MockProvider from a JSON configuration file.
//
// The configuration file should have the following format:
//
//	{
//	  "targets": {
//	    "dc-01": {
//	      "name": "Domain Controller 01",
//	      "hostname": "10.0.1.10",
//	      "port": 3389,
//	      "username": "Administrator",
//	      "password": "P@ssw0rd!",
//	      "domain": "CORP"
//	    }
//	  }
//	}
//
// Returns an error if the file cannot be read or parsed.
func NewMockProviderFromConfig(path string) (*MockProvider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config mockConfigFile
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	targets := make(map[string]mockTarget, len(config.Targets))
	for id, t := range config.Targets {
		// Default port to 3389 if not specified
		port := t.Port
		if port == 0 {
			port = 3389
		}

		targets[id] = mockTarget{
			Info: TargetInfo{
				ID:       id,
				Name:     t.Name,
				Hostname: t.Hostname,
			},
			Credentials: TargetCredentials{
				Hostname: t.Hostname,
				Port:     port,
				Username: t.Username,
				Password: t.Password,
				Domain:   t.Domain,
			},
		}
	}

	return &MockProvider{
		targets: targets,
	}, nil
}

// GetTargetCredentials returns the credentials for the specified target.
//
// Returns ErrTargetNotFound if the target ID is not known.
// This method is safe for concurrent use.
func (p *MockProvider) GetTargetCredentials(ctx context.Context, targetID string) (*TargetCredentials, error) {
	// Check context cancellation first
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	target, ok := p.targets[targetID]
	if !ok {
		return nil, ErrTargetNotFound
	}

	// Return a copy to prevent callers from modifying the stored credentials
	creds := target.Credentials
	return &creds, nil
}

// ListTargets returns metadata for all available targets.
//
// The returned TargetInfo structs do NOT include credentials.
// This method is safe for concurrent use.
func (p *MockProvider) ListTargets(ctx context.Context) ([]TargetInfo, error) {
	// Check context cancellation first
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	targets := make([]TargetInfo, 0, len(p.targets))
	for _, t := range p.targets {
		targets = append(targets, t.Info)
	}

	return targets, nil
}

// Close releases resources held by the provider.
//
// For MockProvider, this is a no-op since there are no external connections.
// The method is provided to satisfy the CredentialProvider interface.
func (p *MockProvider) Close() error {
	// No resources to clean up for the mock provider
	return nil
}

// AddTarget adds or updates a target in the provider.
//
// This method is primarily useful for testing scenarios where you need
// to add targets dynamically. It is safe for concurrent use.
func (p *MockProvider) AddTarget(id string, info TargetInfo, creds TargetCredentials) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.targets[id] = mockTarget{
		Info:        info,
		Credentials: creds,
	}
}

// RemoveTarget removes a target from the provider.
//
// This method is primarily useful for testing scenarios. It is safe for
// concurrent use. Returns true if the target existed and was removed.
func (p *MockProvider) RemoveTarget(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	_, existed := p.targets[id]
	delete(p.targets, id)
	return existed
}

// TargetCount returns the number of targets in the provider.
//
// This method is primarily useful for testing. It is safe for concurrent use.
func (p *MockProvider) TargetCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.targets)
}
