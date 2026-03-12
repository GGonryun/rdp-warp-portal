// Package credential provides the pluggable credential provider system for the RDP broker.
//
// The CredentialProvider interface abstracts credential storage and retrieval.
// This allows the broker to work with any secret management system without
// modification to the core logic.
//
// # Implementing a Custom Provider
//
// To integrate with your organization's secret management system, implement the
// CredentialProvider interface:
//
//	type MyProvider struct {
//	    client *secretsClient
//	    mu     sync.RWMutex // Required: all methods must be safe for concurrent use
//	}
//
//	func (p *MyProvider) GetTargetCredentials(ctx context.Context, targetID string) (*TargetCredentials, error) {
//	    // Fetch credentials from your secrets backend
//	    // IMPORTANT: Never log credentials or include them in error messages
//	    // Use context for cancellation and timeouts
//	    // Return ErrTargetNotFound if the target doesn't exist
//	}
//
//	func (p *MyProvider) ListTargets(ctx context.Context) ([]TargetInfo, error) {
//	    // Return target metadata WITHOUT credentials
//	    // This is called by the API to show available targets
//	}
//
//	func (p *MyProvider) Close() error {
//	    // Clean up connections, goroutines, etc.
//	}
//
// # Thread Safety
//
// All methods MUST be safe for concurrent use from multiple goroutines.
// The broker may call GetTargetCredentials simultaneously for different sessions.
// Use sync.RWMutex or similar synchronization primitives.
//
// # Error Handling
//
// - Return ErrTargetNotFound when a requested target doesn't exist
// - Wrap other errors with context (e.g., "vault connection failed: ...")
// - Never include credentials in error messages
// - Consider implementing retry logic for transient failures
//
// # Credential Caching
//
// Providers MAY cache credentials, but should consider:
// - Credential rotation: cached credentials may become stale
// - Security: cached credentials increase exposure window
// - Recommended: short TTLs (30s-5m) or no caching for highly sensitive targets
// - The broker itself does NOT cache credentials between sessions
//
// # Provider Availability
//
// If the backing store is unavailable:
// - Return an error from GetTargetCredentials (session creation will fail)
// - The broker does not retry; the caller can retry at the API level
// - Consider implementing health checks in your provider
// - Log connectivity issues for monitoring/alerting
package credential

import (
	"context"
	"errors"
)

// ErrTargetNotFound is returned when a requested target does not exist
// in the credential provider's backing store.
var ErrTargetNotFound = errors.New("target not found")

// CredentialProvider abstracts credential storage and retrieval.
// Implementations must be safe for concurrent use from multiple goroutines.
type CredentialProvider interface {
	// GetTargetCredentials retrieves the RDP credentials for a target machine.
	//
	// The targetID is a unique identifier for the target (e.g., "dc-01", "ws-05").
	// The context should be used for cancellation and timeout handling.
	//
	// Returns:
	// - (*TargetCredentials, nil) on success
	// - (nil, ErrTargetNotFound) if the target doesn't exist
	// - (nil, error) for other failures (network, auth, etc.)
	//
	// Implementations MUST NOT log or expose the returned credentials.
	GetTargetCredentials(ctx context.Context, targetID string) (*TargetCredentials, error)

	// ListTargets returns metadata about all available targets.
	//
	// IMPORTANT: The returned TargetInfo structs MUST NOT contain credentials.
	// This method is exposed via the API and credentials must never be leaked.
	//
	// Returns:
	// - ([]TargetInfo, nil) on success (may be empty slice)
	// - (nil, error) on failure
	ListTargets(ctx context.Context) ([]TargetInfo, error)

	// Close releases any resources held by the provider.
	//
	// Called during broker shutdown. After Close returns, no other methods
	// will be called on the provider.
	//
	// Implementations should:
	// - Close network connections to backing stores
	// - Stop background goroutines (token renewal, health checks)
	// - Release any other resources
	Close() error
}

// TargetCredentials contains the full credentials needed to connect to a
// target machine via RDP. This struct is NEVER exposed via the API.
type TargetCredentials struct {
	// Hostname is the target machine's IP address or DNS name.
	Hostname string `json:"hostname"`

	// Port is the RDP port on the target machine (typically 3389).
	Port int `json:"port"`

	// Username is the account name to authenticate as.
	Username string `json:"username"`

	// Password is the account password. Handle with care.
	Password string `json:"password"`

	// Domain is the Windows domain for the account (e.g., "CORP").
	// May be empty for local accounts.
	Domain string `json:"domain"`
}

// TargetInfo contains public metadata about a target machine.
// This struct is intentionally designed to exclude credentials
// and is safe to expose via the API.
type TargetInfo struct {
	// ID is the unique identifier for the target (e.g., "dc-01").
	// Used when requesting a session.
	ID string `json:"id"`

	// Name is a human-readable display name (e.g., "Domain Controller 01").
	Name string `json:"name"`

	// Hostname is the target machine's IP address or DNS name.
	// Included for informational purposes; does not expose security info.
	Hostname string `json:"hostname"`
}
