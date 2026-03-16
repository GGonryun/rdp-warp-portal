package session

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// Token-related errors.
var (
	ErrTokenExpired       = errors.New("token has expired")
	ErrTokenAlreadyUsed   = errors.New("token has already been used")
	ErrTokenInvalid       = errors.New("invalid token")
	ErrTokenMismatch      = errors.New("token does not match session")
)

// TokenSize is the number of random bytes used for token generation.
// 32 bytes = 256 bits of entropy.
const TokenSize = 32

// Token represents a one-time-use session token with expiration.
type Token struct {
	mu       sync.Mutex
	value    string
	expiry   time.Time
	consumed bool
}

// NewToken creates a new token with the given TTL.
// The token is generated using crypto/rand and encoded as base64url.
func NewToken(ttl time.Duration) (*Token, error) {
	value, err := GenerateToken()
	if err != nil {
		return nil, err
	}

	return &Token{
		value:    value,
		expiry:   time.Now().Add(ttl),
		consumed: false,
	}, nil
}

// GenerateToken generates a cryptographically secure random token.
// Returns a base64url-encoded string (43 characters for 32 bytes).
func GenerateToken() (string, error) {
	bytes := make([]byte, TokenSize)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// Value returns the token string value.
func (t *Token) Value() string {
	return t.value
}

// Expiry returns when the token expires.
func (t *Token) Expiry() time.Time {
	return t.expiry
}

// IsExpired returns true if the token has expired.
func (t *Token) IsExpired() bool {
	return time.Now().After(t.expiry)
}

// IsConsumed returns true if the token has been used.
func (t *Token) IsConsumed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.consumed
}

// Validate checks if the provided token value is valid and marks it as consumed.
// This method is safe for concurrent use.
//
// Returns nil if the token is valid, otherwise returns one of:
//   - ErrTokenMismatch: the provided value doesn't match
//   - ErrTokenExpired: the token has expired
//   - ErrTokenAlreadyUsed: the token was already consumed
func (t *Token) Validate(value string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check token value matches
	if t.value != value {
		return ErrTokenMismatch
	}

	// Check if already consumed (one-time use)
	if t.consumed {
		return ErrTokenAlreadyUsed
	}

	// Check expiration
	if time.Now().After(t.expiry) {
		return ErrTokenExpired
	}

	// Mark as consumed (one-time use)
	t.consumed = true
	return nil
}

// ValidateWithoutConsume checks if the token is valid without marking it as consumed.
// This is useful for checking token status without affecting its state.
func (t *Token) ValidateWithoutConsume(value string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.value != value {
		return ErrTokenMismatch
	}

	if t.consumed {
		return ErrTokenAlreadyUsed
	}

	if time.Now().After(t.expiry) {
		return ErrTokenExpired
	}

	return nil
}

// TTLRemaining returns the time remaining until the token expires.
// Returns a negative duration if already expired.
func (t *Token) TTLRemaining() time.Duration {
	return time.Until(t.expiry)
}
