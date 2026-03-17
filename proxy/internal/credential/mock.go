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
	Info   TargetInfo
	Port   int
	Domain string
	Users  []TargetUser
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
func NewMockProvider() *MockProvider {
	return &MockProvider{
		targets: map[string]mockTarget{
			"dc-01": {
				Info: TargetInfo{
					ID:       "dc-01",
					Hostname: "dc-01",
					IP: "10.0.1.10",
				},
				Port:   3389,
				Domain: "CORP",
				Users: []TargetUser{
					{Username: "Administrator", Password: "P@ssw0rd!"},
				},
			},
			"ws-05": {
				Info: TargetInfo{
					ID:       "ws-05",
					Hostname: "ws-05",
					IP: "10.0.1.50",
				},
				Port:   3389,
				Domain: "CORP",
				Users: []TargetUser{
					{Username: "svc-rdp", Password: "Sup3rS3cret"},
				},
			},
			"win-vm-1": {
				Info: TargetInfo{
					ID:       "win-vm-1",
					Hostname: "win-vm-1",
					IP: "20.64.171.136",
				},
				Port:   3389,
				Domain: "",
				Users: []TargetUser{
					{Username: "rdpadmin", Password: "CHANGE_ME_BEFORE_DEPLOY"},
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
	Hostname string       `json:"hostname"`
	IP       string       `json:"ip"`
	Port     int          `json:"port"`
	Domain   string       `json:"domain"`
	Users    []TargetUser `json:"users"`
}

// NewMockProviderFromConfig creates a MockProvider from a JSON configuration file.
//
// The configuration file should have the following format:
//
//	{
//	  "targets": {
//	    "mike-rdp": {
//	      "hostname": "mike-rdp",
//	      "ip": "10.1.0.7",
//	      "port": 3389,
//	      "domain": "",
//	      "users": [
//	        {"username": "rdpadmin", "password": "Password123abc"}
//	      ]
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
		port := t.Port
		if port == 0 {
			port = 3389
		}

		targets[id] = mockTarget{
			Info: TargetInfo{
				ID:       id,
				Hostname: t.Hostname,
				IP: t.IP,
			},
			Port:   port,
			Domain: t.Domain,
			Users:  t.Users,
		}
	}

	return &MockProvider{
		targets: targets,
	}, nil
}

// GetTargetCredentials returns the credentials for a specific user on the specified target.
//
// Returns ErrTargetNotFound if the target ID is not known.
// Returns ErrUserNotFound if the username is not available on the target.
// This method is safe for concurrent use.
func (p *MockProvider) GetTargetCredentials(ctx context.Context, targetID, username string) (*TargetCredentials, error) {
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

	for _, u := range target.Users {
		if u.Username == username {
			return &TargetCredentials{
				IP:       target.Info.IP,
				Port:     target.Port,
				Username: u.Username,
				Password: u.Password,
				Domain:   target.Domain,
			}, nil
		}
	}

	return nil, ErrUserNotFound
}

// ListTargets returns metadata for all available targets.
//
// The returned TargetInfo structs do NOT include credentials.
// This method is safe for concurrent use.
func (p *MockProvider) ListTargets(ctx context.Context) ([]TargetInfo, error) {
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

// ListDestinations returns all targets with their full user credentials.
// This method is safe for concurrent use.
func (p *MockProvider) ListDestinations(ctx context.Context) ([]TargetDestination, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	destinations := make([]TargetDestination, 0, len(p.targets))
	for _, t := range p.targets {
		users := make([]TargetUser, len(t.Users))
		copy(users, t.Users)
		destinations = append(destinations, TargetDestination{
			ID:       t.Info.ID,
			Hostname: t.Info.Hostname,
			IP:       t.Info.IP,
			Users:    users,
		})
	}

	return destinations, nil
}

// Close releases resources held by the provider.
func (p *MockProvider) Close() error {
	return nil
}

// AddTarget adds or updates a target in the provider.
// This method is primarily useful for testing scenarios.
func (p *MockProvider) AddTarget(id string, info TargetInfo, port int, domain string, users []TargetUser) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.targets[id] = mockTarget{
		Info:   info,
		Port:   port,
		Domain: domain,
		Users:  users,
	}
}

// RemoveTarget removes a target from the provider.
func (p *MockProvider) RemoveTarget(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	_, existed := p.targets[id]
	delete(p.targets, id)
	return existed
}

// TargetCount returns the number of targets in the provider.
func (p *MockProvider) TargetCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.targets)
}
