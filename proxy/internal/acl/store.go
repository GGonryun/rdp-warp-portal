package acl

import (
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// HostConfig holds the connection details for a target machine.
type HostConfig struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Domain   string `json:"domain,omitempty"`
}

// UserConfig holds the user identity and credentials for accessing a target.
type UserConfig struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Secret   string `json:"secret"`
}

// Grant is a fully self-contained config: this user can access this host.
type Grant struct {
	Host HostConfig `json:"host"`
	User UserConfig `json:"user"`
}

// PublicKeyEntry holds a parsed public key with its serialized form and fingerprint.
type PublicKeyEntry struct {
	Key         crypto.PublicKey `json:"-"`
	Raw         string          `json:"public_key"`
	Fingerprint string          `json:"fingerprint"`
	Email       string          `json:"email"`
}

// ErrKeyNotFound is returned when no public key is registered for the given identity.
var ErrKeyNotFound = errors.New("public key not found")

// ErrGrantNotFound is returned when no grant exists for the given (email, hostname).
var ErrGrantNotFound = errors.New("grant not found")

// Store defines the interface for access control and key management.
type Store interface {
	GrantAccess(ctx context.Context, grant Grant) error
	RevokeAccess(ctx context.Context, email, hostname string) error
	HasAccess(ctx context.Context, email, hostname string) (bool, error)
	FindGrant(ctx context.Context, email, hostname string) (*Grant, error)
	ListAll(ctx context.Context) ([]Grant, error)

	AddPublicKey(ctx context.Context, email string, publicKey crypto.PublicKey) (string, error)
	RemovePublicKey(ctx context.Context, email, fingerprint string) error
	GetPublicKeys(ctx context.Context, email string) ([]crypto.PublicKey, error)
	ListPublicKeys(ctx context.Context) ([]PublicKeyEntry, error)
}

// grantKey is the composite key for storing grants.
type grantKey struct {
	Email    string
	Hostname string
}

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu         sync.RWMutex
	grants     map[grantKey]Grant
	publicKeys map[string][]PublicKeyEntry // normalized email → list of keys
}

// NewMemoryStore returns a ready-to-use in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		grants:     make(map[grantKey]Grant),
		publicKeys: make(map[string][]PublicKeyEntry),
	}
}

func (s *MemoryStore) GrantAccess(_ context.Context, grant Grant) error {
	key := grantKey{
		Email:    normalizeEmail(grant.User.Email),
		Hostname: strings.ToLower(grant.Host.Hostname),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Normalize the grant's email before storing.
	grant.User.Email = normalizeEmail(grant.User.Email)
	s.grants[key] = grant
	return nil
}

func (s *MemoryStore) RevokeAccess(_ context.Context, email, hostname string) error {
	key := grantKey{
		Email:    normalizeEmail(email),
		Hostname: strings.ToLower(hostname),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.grants, key)
	return nil
}

func (s *MemoryStore) HasAccess(_ context.Context, email, hostname string) (bool, error) {
	key := grantKey{
		Email:    normalizeEmail(email),
		Hostname: strings.ToLower(hostname),
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.grants[key]
	return ok, nil
}

func (s *MemoryStore) FindGrant(_ context.Context, email, hostname string) (*Grant, error) {
	key := grantKey{
		Email:    normalizeEmail(email),
		Hostname: strings.ToLower(hostname),
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	grant, ok := s.grants[key]
	if !ok {
		return nil, ErrGrantNotFound
	}
	return &grant, nil
}

func (s *MemoryStore) ListAll(_ context.Context) ([]Grant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	grants := make([]Grant, 0, len(s.grants))
	for _, g := range s.grants {
		grants = append(grants, g)
	}
	return grants, nil
}

// AddPublicKey adds a public key for the given email. Returns the key's fingerprint.
// Idempotent — adding the same key again is a no-op.
func (s *MemoryStore) AddPublicKey(_ context.Context, email string, publicKey crypto.PublicKey) (string, error) {
	fp, err := Fingerprint(publicKey)
	if err != nil {
		return "", fmt.Errorf("compute fingerprint: %w", err)
	}

	raw, err := marshalPublicKeyPEM(publicKey)
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}

	normEmail := normalizeEmail(email)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for duplicate.
	for _, entry := range s.publicKeys[normEmail] {
		if entry.Fingerprint == fp {
			return fp, nil // already registered
		}
	}

	s.publicKeys[normEmail] = append(s.publicKeys[normEmail], PublicKeyEntry{
		Key:         publicKey,
		Raw:         raw,
		Fingerprint: fp,
		Email:       normEmail,
	})
	return fp, nil
}

func (s *MemoryStore) RemovePublicKey(_ context.Context, email, fingerprint string) error {
	normEmail := normalizeEmail(email)

	s.mu.Lock()
	defer s.mu.Unlock()

	entries := s.publicKeys[normEmail]
	for i, entry := range entries {
		if entry.Fingerprint == fingerprint {
			s.publicKeys[normEmail] = append(entries[:i], entries[i+1:]...)
			if len(s.publicKeys[normEmail]) == 0 {
				delete(s.publicKeys, normEmail)
			}
			return nil
		}
	}
	return ErrKeyNotFound
}

func (s *MemoryStore) GetPublicKeys(_ context.Context, email string) ([]crypto.PublicKey, error) {
	normEmail := normalizeEmail(email)

	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := s.publicKeys[normEmail]
	if len(entries) == 0 {
		return nil, ErrKeyNotFound
	}

	keys := make([]crypto.PublicKey, len(entries))
	for i, e := range entries {
		keys[i] = e.Key
	}
	return keys, nil
}

func (s *MemoryStore) ListPublicKeys(_ context.Context) ([]PublicKeyEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var all []PublicKeyEntry
	for _, entries := range s.publicKeys {
		all = append(all, entries...)
	}
	return all, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(email)
}

// Fingerprint computes a SHA256 fingerprint of a public key's DER encoding.
func Fingerprint(pub crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(der)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(hash[:]), nil
}

// ParsePublicKey parses a public key from PEM, SSH authorized key, or raw base64 DER.
func ParsePublicKey(data string) (crypto.PublicKey, error) {
	trimmed := strings.TrimSpace(data)

	// Try PEM.
	block, _ := pem.Decode([]byte(trimmed))
	if block != nil {
		return x509.ParsePKIXPublicKey(block.Bytes)
	}

	// Try SSH authorized key format.
	if strings.HasPrefix(trimmed, "ssh-") || strings.HasPrefix(trimmed, "ecdsa-") {
		sshPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(trimmed))
		if err != nil {
			return nil, errors.New("invalid SSH public key: " + err.Error())
		}
		cryptoPub, ok := sshPub.(ssh.CryptoPublicKey)
		if !ok {
			return nil, errors.New("SSH key type does not expose crypto.PublicKey")
		}
		return cryptoPub.CryptoPublicKey(), nil
	}

	// Try raw base64 DER.
	der, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		der, err = base64.RawURLEncoding.DecodeString(trimmed)
		if err != nil {
			return nil, errors.New("invalid public key: not PEM, SSH, or base64-encoded DER")
		}
	}
	return x509.ParsePKIXPublicKey(der)
}

// marshalPublicKeyPEM encodes a public key to PEM format.
func marshalPublicKeyPEM(pub crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	block := &pem.Block{Type: "PUBLIC KEY", Bytes: der}
	return string(pem.EncodeToMemory(block)), nil
}
