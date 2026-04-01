package auth

import (
	"context"
	"crypto"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// KeyLookup retrieves all public keys for a given identity (email).
// The ACL store satisfies this interface.
type KeyLookup interface {
	GetPublicKeys(ctx context.Context, email string) ([]crypto.PublicKey, error)
}

// Verifier validates JWTs by looking up the signer's public keys.
type Verifier struct {
	keys KeyLookup
}

// NewVerifier creates a new JWT verifier backed by the given key lookup.
func NewVerifier(keys KeyLookup) *Verifier {
	return &Verifier{keys: keys}
}

// Verify parses and verifies a JWT token string. It extracts the subject (sub)
// claim, looks up all registered public keys for that email, and tries each
// until one successfully verifies the signature.
// Returns the validated claims or an error.
func (v *Verifier) Verify(ctx context.Context, tokenString string) (*Claims, error) {
	// Parse unverified to extract sub claim for key lookup.
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	unverified := &Claims{}
	if _, _, err := parser.ParseUnverified(tokenString, unverified); err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	sub, err := unverified.GetSubject()
	if err != nil || sub == "" {
		return nil, errors.New("missing or empty sub claim")
	}

	// Look up all public keys for this identity.
	pubKeys, err := v.keys.GetPublicKeys(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("key lookup for %q: %w", sub, err)
	}

	// Try each registered key until one verifies the signature.
	var lastErr error
	for _, pubKey := range pubKeys {
		claims := &Claims{}
		_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
			return pubKey, nil
		})
		if err == nil {
			return claims, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return nil, fmt.Errorf("token verification failed: %w", lastErr)
	}
	return nil, errors.New("no keys matched")
}
