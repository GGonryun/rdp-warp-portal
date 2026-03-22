package acl

import (
	"context"
	"strings"
	"sync"
)

// Grant represents a single access grant: an email may use a specific username on a target.
type Grant struct {
	TargetID string
	Username string
}

// ACLEntry is a flattened representation of a grant for API responses.
type ACLEntry struct {
	Email    string `json:"email"`
	TargetID string `json:"target_id"`
	Username string `json:"username"`
}

// Store defines the interface for an access control list.
// Implementations may be backed by an in-memory map, Redis, a database, etc.
type Store interface {
	GrantAccess(ctx context.Context, email, targetID, username string) error
	RevokeAccess(ctx context.Context, email, targetID, username string) error
	HasAccess(ctx context.Context, email, targetID, username string) (bool, error)
	ListAll(ctx context.Context) ([]ACLEntry, error)
}

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu      sync.RWMutex
	entries map[string]map[Grant]struct{} // normalised email → set of grants
}

// NewMemoryStore returns a ready-to-use in-memory ACL store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		entries: make(map[string]map[Grant]struct{}),
	}
}

func (s *MemoryStore) GrantAccess(_ context.Context, email, targetID, username string) error {
	key := normalizeEmail(email)
	grant := Grant{TargetID: targetID, Username: strings.ToLower(username)}

	s.mu.Lock()
	defer s.mu.Unlock()

	grants, ok := s.entries[key]
	if !ok {
		grants = make(map[Grant]struct{})
		s.entries[key] = grants
	}
	grants[grant] = struct{}{}
	return nil
}

func (s *MemoryStore) RevokeAccess(_ context.Context, email, targetID, username string) error {
	key := normalizeEmail(email)
	grant := Grant{TargetID: targetID, Username: strings.ToLower(username)}

	s.mu.Lock()
	defer s.mu.Unlock()

	if grants, ok := s.entries[key]; ok {
		delete(grants, grant)
		if len(grants) == 0 {
			delete(s.entries, key)
		}
	}
	return nil
}

func (s *MemoryStore) HasAccess(_ context.Context, email, targetID, username string) (bool, error) {
	key := normalizeEmail(email)
	grant := Grant{TargetID: targetID, Username: strings.ToLower(username)}

	s.mu.RLock()
	defer s.mu.RUnlock()

	grants, ok := s.entries[key]
	if !ok {
		return false, nil
	}
	_, has := grants[grant]
	return has, nil
}

func (s *MemoryStore) ListAll(_ context.Context) ([]ACLEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var entries []ACLEntry
	for email, grants := range s.entries {
		for g := range grants {
			entries = append(entries, ACLEntry{
				Email:    email,
				TargetID: g.TargetID,
				Username: g.Username,
			})
		}
	}
	return entries, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(email)
}
