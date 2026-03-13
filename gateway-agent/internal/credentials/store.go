package credentials

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/p0rtal-4/gateway-agent/internal/config"
)

// Store provides thread-safe access to target credentials loaded from a JSON
// file on disk. It watches the file for changes and automatically reloads.
type Store struct {
	mu      sync.RWMutex
	targets map[string]*Target
	path    string
	watcher *fsnotify.Watcher
}

// New creates a Store by loading the credentials file specified in cfg and
// starting a background goroutine that reloads on file changes.
func New(cfg *config.Config) (*Store, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("credentials: create watcher: %w", err)
	}

	s := &Store{
		targets: make(map[string]*Target),
		path:    cfg.CredentialsFile,
		watcher: watcher,
	}

	if err := s.load(); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("credentials: initial load: %w", err)
	}

	if err := watcher.Add(s.path); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("credentials: watch file: %w", err)
	}

	go s.watch()

	return s, nil
}

// Get returns the Target for the given ID or an error if it does not exist.
func (s *Store) Get(targetID string) (*Target, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.targets[targetID]
	if !ok {
		return nil, fmt.Errorf("credentials: target %q not found", targetID)
	}

	// Return a copy so the caller cannot mutate internal state.
	cp := *t
	return &cp, nil
}

// List returns a copy of every target in the store.
func (s *Store) List() []Target {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Target, 0, len(s.targets))
	for _, t := range s.targets {
		out = append(out, *t)
	}
	return out
}

// ListSafe returns every target with sensitive fields stripped.
func (s *Store) ListSafe() []TargetSafe {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]TargetSafe, 0, len(s.targets))
	for _, t := range s.targets {
		out = append(out, t.Safe())
	}
	return out
}

// Reload re-reads the credentials file from disk. If the file is malformed
// the existing targets are preserved and the error is returned.
func (s *Store) Reload() error {
	return s.load()
}

// Close stops the fsnotify watcher.
func (s *Store) Close() {
	s.watcher.Close()
}

// load reads the credentials file, parses it, and replaces the in-memory map.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("read credentials file: %w", err)
	}

	var cf CredentialsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return fmt.Errorf("parse credentials file: %w", err)
	}

	m := make(map[string]*Target, len(cf.Targets))
	for i := range cf.Targets {
		t := cf.Targets[i]
		m[t.ID] = &t
	}

	s.mu.Lock()
	s.targets = m
	s.mu.Unlock()

	return nil
}

// watch listens for fsnotify events and reloads the credentials file on writes.
func (s *Store) watch() {
	for {
		select {
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) {
				if err := s.Reload(); err != nil {
					log.Printf("credentials: reload failed: %v (keeping existing targets)", err)
				} else {
					log.Printf("credentials: reloaded successfully")
				}
			}
		case err, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("credentials: watcher error: %v", err)
		}
	}
}
