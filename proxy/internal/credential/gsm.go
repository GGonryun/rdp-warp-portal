package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// gsmTarget holds both public info and private credentials for a target.
type gsmTarget struct {
	Info   TargetInfo
	Port   int
	Domain string
	Users  []TargetUser
}

// SecretResolver resolves a secret name to its value.
// Used to fetch passwords from Google Secret Manager.
type SecretResolver func(ctx context.Context, secretName string) (string, error)

// ErrSecretResolverNotConfigured is returned when a user requires secret
// resolution but no resolver has been configured.
var ErrSecretResolverNotConfigured = errors.New("secret resolver not configured")

// GSMProvider is a credential provider that loads targets from a JSON
// configuration file and resolves passwords from Google Secret Manager.
//
// All methods are safe for concurrent use.
type GSMProvider struct {
	mu            sync.RWMutex
	targets       map[string]gsmTarget
	resolveSecret SecretResolver
}

// Compile-time check that GSMProvider implements CredentialProvider.
var _ CredentialProvider = (*GSMProvider)(nil)

// NewTestProvider creates a GSMProvider with hardcoded targets for testing.
// The resolver returns fixed passwords for known secret names.
func NewTestProvider() *GSMProvider {
	secrets := map[string]string{
		"secret/admin-pass":   "P@ssw0rd!",
		"secret/svc-rdp-pass": "Sup3rS3cret",
		"secret/rdpadmin":     "CHANGE_ME_BEFORE_DEPLOY",
	}
	resolver := func(_ context.Context, name string) (string, error) {
		if v, ok := secrets[name]; ok {
			return v, nil
		}
		return "", fmt.Errorf("test secret not found: %s", name)
	}

	return &GSMProvider{
		resolveSecret: resolver,
		targets: map[string]gsmTarget{
			"dc-01": {
				Info:   TargetInfo{ID: "dc-01", Hostname: "dc-01", IP: "10.0.1.10"},
				Port:   3389,
				Domain: "CORP",
				Users:  []TargetUser{{Username: "Administrator", Secret: "secret/admin-pass"}},
			},
			"ws-05": {
				Info:   TargetInfo{ID: "ws-05", Hostname: "ws-05", IP: "10.0.1.50"},
				Port:   3389,
				Domain: "CORP",
				Users:  []TargetUser{{Username: "svc-rdp", Secret: "secret/svc-rdp-pass"}},
			},
			"win-vm-1": {
				Info:   TargetInfo{ID: "win-vm-1", Hostname: "win-vm-1", IP: "20.64.171.136"},
				Port:   3389,
				Domain: "",
				Users:  []TargetUser{{Username: "rdpadmin", Secret: "secret/rdpadmin"}},
			},
		},
	}
}

// gsmConfigFile represents the JSON structure of the config file.
type gsmConfigFile struct {
	Targets map[string]gsmConfigTarget `json:"targets"`
}

// gsmConfigTarget represents a single target in the config file.
type gsmConfigTarget struct {
	Hostname string       `json:"hostname"`
	IP       string       `json:"ip"`
	Port     int          `json:"port"`
	Domain   string       `json:"domain"`
	Users    []TargetUser `json:"users"`
}

// NewGSMProvider creates a GSMProvider from a JSON configuration file.
//
// The configuration file should have the following format:
//
//	{
//	  "targets": {
//	    "domain-controller": {
//	      "hostname": "domain-controll",
//	      "ip": "10.1.0.5",
//	      "port": 3389,
//	      "domain": "P0LAB",
//	      "users": [
//	        {
//	          "username": "golden.marmot@p0lab1.internal",
//	          "secret": "projects/233195464130/secrets/my-secret"
//	        }
//	      ]
//	    }
//	  }
//	}
//
// Returns an error if the file cannot be read or parsed.
func NewGSMProvider(path string, resolver SecretResolver) (*GSMProvider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config gsmConfigFile
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	targets := make(map[string]gsmTarget, len(config.Targets))
	for id, t := range config.Targets {
		port := t.Port
		if port == 0 {
			port = 3389
		}

		targets[id] = gsmTarget{
			Info: TargetInfo{
				ID:       id,
				Hostname: t.Hostname,
				IP:       t.IP,
			},
			Port:   port,
			Domain: t.Domain,
			Users:  t.Users,
		}
	}

	return &GSMProvider{
		targets:       targets,
		resolveSecret: resolver,
	}, nil
}

// GetTargetCredentials returns the credentials for a specific user on the specified target.
// Passwords are resolved from Google Secret Manager at call time.
//
// Returns ErrTargetNotFound if the target ID is not known.
// Returns ErrUserNotFound if the username is not available on the target.
// This method is safe for concurrent use.
func (p *GSMProvider) GetTargetCredentials(ctx context.Context, targetID, username string) (*TargetCredentials, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Look up target and user under read lock, copy what we need.
	p.mu.RLock()
	target, ok := p.targets[targetID]
	if !ok {
		p.mu.RUnlock()
		return nil, ErrTargetNotFound
	}

	var matchedUser *TargetUser
	for i := range target.Users {
		if target.Users[i].Username == username {
			u := target.Users[i] // copy
			matchedUser = &u
			break
		}
	}
	// Copy target info before releasing lock.
	ip := target.Info.IP
	port := target.Port
	domain := target.Domain
	p.mu.RUnlock()

	if matchedUser == nil {
		return nil, ErrUserNotFound
	}

	// Resolve the secret (requires HTTP call, must be outside lock).
	if matchedUser.Secret == "" {
		return nil, fmt.Errorf("user %s has no secret configured", username)
	}
	if p.resolveSecret == nil {
		return nil, ErrSecretResolverNotConfigured
	}
	password, err := p.resolveSecret(ctx, matchedUser.Secret)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve secret for user %s: %w", username, err)
	}

	return &TargetCredentials{
		IP:       ip,
		Port:     port,
		Username: matchedUser.Username,
		Password: password,
		Domain:   domain,
	}, nil
}

// ListTargets returns metadata for all available targets.
//
// The returned TargetInfo structs do NOT include credentials.
// This method is safe for concurrent use.
func (p *GSMProvider) ListTargets(ctx context.Context) ([]TargetInfo, error) {
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

// ListDestinations returns all targets with their available users.
// Passwords are NOT included — only usernames are exposed.
// This method is safe for concurrent use.
func (p *GSMProvider) ListDestinations(ctx context.Context) ([]TargetDestination, error) {
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
		for i, u := range t.Users {
			users[i] = TargetUser{Username: u.Username}
		}
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
func (p *GSMProvider) Close() error {
	return nil
}

// AddTarget adds or updates a target in the provider.
// This method is primarily useful for testing scenarios.
func (p *GSMProvider) AddTarget(id string, info TargetInfo, port int, domain string, users []TargetUser) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.targets[id] = gsmTarget{
		Info:   info,
		Port:   port,
		Domain: domain,
		Users:  users,
	}
}

// RemoveTarget removes a target from the provider.
func (p *GSMProvider) RemoveTarget(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	_, existed := p.targets[id]
	delete(p.targets, id)
	return existed
}

// TargetCount returns the number of targets in the provider.
func (p *GSMProvider) TargetCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.targets)
}
