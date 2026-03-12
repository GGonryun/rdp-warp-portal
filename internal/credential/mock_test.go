package credential

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewMockProvider(t *testing.T) {
	provider := NewMockProvider()
	if provider == nil {
		t.Fatal("NewMockProvider returned nil")
	}

	// Verify default targets are present (dc-01, ws-05, win-vm-1)
	count := provider.TargetCount()
	if count != 3 {
		t.Errorf("expected 3 default targets, got %d", count)
	}
}

func TestGetTargetCredentials_Success(t *testing.T) {
	provider := NewMockProvider()
	ctx := context.Background()

	tests := []struct {
		targetID         string
		expectedHostname string
		expectedPort     int
		expectedUsername string
		expectedDomain   string
	}{
		{
			targetID:         "dc-01",
			expectedHostname: "10.0.1.10",
			expectedPort:     3389,
			expectedUsername: "Administrator",
			expectedDomain:   "CORP",
		},
		{
			targetID:         "ws-05",
			expectedHostname: "10.0.1.50",
			expectedPort:     3389,
			expectedUsername: "svc-rdp",
			expectedDomain:   "CORP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.targetID, func(t *testing.T) {
			creds, err := provider.GetTargetCredentials(ctx, tt.targetID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if creds == nil {
				t.Fatal("credentials are nil")
			}

			if creds.Hostname != tt.expectedHostname {
				t.Errorf("hostname: got %q, want %q", creds.Hostname, tt.expectedHostname)
			}
			if creds.Port != tt.expectedPort {
				t.Errorf("port: got %d, want %d", creds.Port, tt.expectedPort)
			}
			if creds.Username != tt.expectedUsername {
				t.Errorf("username: got %q, want %q", creds.Username, tt.expectedUsername)
			}
			if creds.Domain != tt.expectedDomain {
				t.Errorf("domain: got %q, want %q", creds.Domain, tt.expectedDomain)
			}
			// Verify password is not empty (don't log actual password in tests)
			if creds.Password == "" {
				t.Error("password is empty")
			}
		})
	}
}

func TestGetTargetCredentials_NotFound(t *testing.T) {
	provider := NewMockProvider()
	ctx := context.Background()

	creds, err := provider.GetTargetCredentials(ctx, "nonexistent-target")
	if err == nil {
		t.Fatal("expected error for nonexistent target")
	}
	if !errors.Is(err, ErrTargetNotFound) {
		t.Errorf("expected ErrTargetNotFound, got: %v", err)
	}
	if creds != nil {
		t.Error("credentials should be nil for nonexistent target")
	}
}

func TestGetTargetCredentials_ContextCanceled(t *testing.T) {
	provider := NewMockProvider()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	creds, err := provider.GetTargetCredentials(ctx, "dc-01")
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	if creds != nil {
		t.Error("credentials should be nil for canceled context")
	}
}

func TestGetTargetCredentials_ContextTimeout(t *testing.T) {
	provider := NewMockProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	// Let the timeout expire
	time.Sleep(time.Millisecond)

	creds, err := provider.GetTargetCredentials(ctx, "dc-01")
	if err == nil {
		t.Fatal("expected error for timed out context")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got: %v", err)
	}
	if creds != nil {
		t.Error("credentials should be nil for timed out context")
	}
}

func TestListTargets_Success(t *testing.T) {
	provider := NewMockProvider()
	ctx := context.Background()

	targets, err := provider.ListTargets(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(targets))
	}

	// Build a map for easier lookup
	targetMap := make(map[string]TargetInfo)
	for _, target := range targets {
		targetMap[target.ID] = target
	}

	// Verify dc-01
	dc01, ok := targetMap["dc-01"]
	if !ok {
		t.Fatal("dc-01 not found in targets")
	}
	if dc01.Name != "Domain Controller 01" {
		t.Errorf("dc-01 name: got %q, want %q", dc01.Name, "Domain Controller 01")
	}
	if dc01.Hostname != "10.0.1.10" {
		t.Errorf("dc-01 hostname: got %q, want %q", dc01.Hostname, "10.0.1.10")
	}

	// Verify ws-05
	ws05, ok := targetMap["ws-05"]
	if !ok {
		t.Fatal("ws-05 not found in targets")
	}
	if ws05.Name != "Workstation 05" {
		t.Errorf("ws-05 name: got %q, want %q", ws05.Name, "Workstation 05")
	}
	if ws05.Hostname != "10.0.1.50" {
		t.Errorf("ws-05 hostname: got %q, want %q", ws05.Hostname, "10.0.1.50")
	}

	// Verify win-vm-1
	winvm1, ok := targetMap["win-vm-1"]
	if !ok {
		t.Fatal("win-vm-1 not found in targets")
	}
	if winvm1.Name != "Azure Windows VM" {
		t.Errorf("win-vm-1 name: got %q, want %q", winvm1.Name, "Azure Windows VM")
	}
	if winvm1.Hostname != "20.64.171.136" {
		t.Errorf("win-vm-1 hostname: got %q, want %q", winvm1.Hostname, "20.64.171.136")
	}
}

func TestListTargets_DoesNotExposeCredentials(t *testing.T) {
	provider := NewMockProvider()
	ctx := context.Background()

	targets, err := provider.ListTargets(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// TargetInfo struct should only have ID, Name, Hostname
	// This is a compile-time check - if TargetInfo gains credential fields,
	// we want to ensure they're not populated
	for _, target := range targets {
		// These fields should exist and be populated
		if target.ID == "" {
			t.Error("target ID is empty")
		}
		if target.Name == "" {
			t.Error("target Name is empty")
		}
		if target.Hostname == "" {
			t.Error("target Hostname is empty")
		}
		// TargetInfo has no credential fields by design
		// This test documents that design decision
	}
}

func TestListTargets_ContextCanceled(t *testing.T) {
	provider := NewMockProvider()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	targets, err := provider.ListTargets(ctx)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	if targets != nil {
		t.Error("targets should be nil for canceled context")
	}
}

func TestNewMockProviderFromConfig(t *testing.T) {
	// Create a temporary config file
	configContent := `{
  "targets": {
    "test-srv-01": {
      "name": "Test Server 01",
      "hostname": "192.168.1.100",
      "port": 3390,
      "username": "testuser",
      "password": "testpass123",
      "domain": "TESTDOMAIN"
    },
    "test-srv-02": {
      "name": "Test Server 02",
      "hostname": "192.168.1.101",
      "username": "admin",
      "password": "adminpass",
      "domain": "TESTDOMAIN"
    }
  }
}`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-targets.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	// Load the provider from config
	provider, err := NewMockProviderFromConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load provider from config: %v", err)
	}

	// Verify target count
	if provider.TargetCount() != 2 {
		t.Errorf("expected 2 targets, got %d", provider.TargetCount())
	}

	ctx := context.Background()

	// Test first target with explicit port
	creds1, err := provider.GetTargetCredentials(ctx, "test-srv-01")
	if err != nil {
		t.Fatalf("failed to get credentials for test-srv-01: %v", err)
	}
	if creds1.Hostname != "192.168.1.100" {
		t.Errorf("hostname: got %q, want %q", creds1.Hostname, "192.168.1.100")
	}
	if creds1.Port != 3390 {
		t.Errorf("port: got %d, want %d", creds1.Port, 3390)
	}
	if creds1.Username != "testuser" {
		t.Errorf("username: got %q, want %q", creds1.Username, "testuser")
	}
	if creds1.Password != "testpass123" {
		t.Errorf("password mismatch")
	}
	if creds1.Domain != "TESTDOMAIN" {
		t.Errorf("domain: got %q, want %q", creds1.Domain, "TESTDOMAIN")
	}

	// Test second target with default port
	creds2, err := provider.GetTargetCredentials(ctx, "test-srv-02")
	if err != nil {
		t.Fatalf("failed to get credentials for test-srv-02: %v", err)
	}
	if creds2.Port != 3389 {
		t.Errorf("default port: got %d, want %d", creds2.Port, 3389)
	}

	// Verify ListTargets works
	targets, err := provider.ListTargets(ctx)
	if err != nil {
		t.Fatalf("failed to list targets: %v", err)
	}
	if len(targets) != 2 {
		t.Errorf("expected 2 targets in list, got %d", len(targets))
	}
}

func TestNewMockProviderFromConfig_FileNotFound(t *testing.T) {
	provider, err := NewMockProviderFromConfig("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if provider != nil {
		t.Error("provider should be nil for nonexistent file")
	}
}

func TestNewMockProviderFromConfig_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.json")
	if err := os.WriteFile(configPath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	provider, err := NewMockProviderFromConfig(configPath)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if provider != nil {
		t.Error("provider should be nil for invalid JSON")
	}
}

func TestNewMockProviderFromConfig_EmptyTargets(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "empty.json")
	if err := os.WriteFile(configPath, []byte(`{"targets": {}}`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	provider, err := NewMockProviderFromConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error for empty targets: %v", err)
	}
	if provider.TargetCount() != 0 {
		t.Errorf("expected 0 targets, got %d", provider.TargetCount())
	}
}

func TestClose(t *testing.T) {
	provider := NewMockProvider()
	err := provider.Close()
	if err != nil {
		t.Errorf("Close returned unexpected error: %v", err)
	}
}

func TestAddTarget(t *testing.T) {
	provider := NewMockProvider()
	initialCount := provider.TargetCount()

	info := TargetInfo{
		ID:       "new-target",
		Name:     "New Target",
		Hostname: "192.168.2.100",
	}
	creds := TargetCredentials{
		Hostname: "192.168.2.100",
		Port:     3389,
		Username: "newuser",
		Password: "newpass",
		Domain:   "NEWDOMAIN",
	}

	provider.AddTarget("new-target", info, creds)

	// Verify target was added
	if provider.TargetCount() != initialCount+1 {
		t.Errorf("expected %d targets, got %d", initialCount+1, provider.TargetCount())
	}

	// Verify we can retrieve the new target
	ctx := context.Background()
	retrieved, err := provider.GetTargetCredentials(ctx, "new-target")
	if err != nil {
		t.Fatalf("failed to get new target: %v", err)
	}
	if retrieved.Username != "newuser" {
		t.Errorf("username: got %q, want %q", retrieved.Username, "newuser")
	}
}

func TestRemoveTarget(t *testing.T) {
	provider := NewMockProvider()
	initialCount := provider.TargetCount()

	// Remove existing target
	removed := provider.RemoveTarget("dc-01")
	if !removed {
		t.Error("RemoveTarget should return true for existing target")
	}
	if provider.TargetCount() != initialCount-1 {
		t.Errorf("expected %d targets, got %d", initialCount-1, provider.TargetCount())
	}

	// Verify target is gone
	ctx := context.Background()
	_, err := provider.GetTargetCredentials(ctx, "dc-01")
	if !errors.Is(err, ErrTargetNotFound) {
		t.Errorf("expected ErrTargetNotFound, got: %v", err)
	}

	// Try to remove nonexistent target
	removed = provider.RemoveTarget("nonexistent")
	if removed {
		t.Error("RemoveTarget should return false for nonexistent target")
	}
}

func TestConcurrentAccess(t *testing.T) {
	provider := NewMockProvider()
	ctx := context.Background()

	const numGoroutines = 100
	const numOperations = 100

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*numOperations)

	// Start multiple goroutines performing various operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				switch j % 4 {
				case 0:
					// Read credentials
					_, err := provider.GetTargetCredentials(ctx, "dc-01")
					if err != nil && !errors.Is(err, ErrTargetNotFound) {
						errChan <- err
					}
				case 1:
					// List targets
					_, err := provider.ListTargets(ctx)
					if err != nil {
						errChan <- err
					}
				case 2:
					// Add a unique target
					targetID := "concurrent-target"
					info := TargetInfo{ID: targetID, Name: "Test", Hostname: "1.2.3.4"}
					creds := TargetCredentials{Hostname: "1.2.3.4", Port: 3389}
					provider.AddTarget(targetID, info, creds)
				case 3:
					// Remove the target we added
					provider.RemoveTarget("concurrent-target")
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for any errors
	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}
	if len(errors) > 0 {
		t.Errorf("concurrent access produced %d errors, first: %v", len(errors), errors[0])
	}
}

func TestConcurrentReads(t *testing.T) {
	provider := NewMockProvider()
	ctx := context.Background()

	const numReaders = 50
	const numReads = 1000

	var wg sync.WaitGroup
	errChan := make(chan error, numReaders)

	// Start many readers simultaneously
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numReads; j++ {
				creds, err := provider.GetTargetCredentials(ctx, "dc-01")
				if err != nil {
					errChan <- err
					return
				}
				// Verify credentials are consistent
				if creds.Hostname != "10.0.1.10" {
					errChan <- errors.New("hostname mismatch during concurrent read")
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		t.Errorf("concurrent read error: %v", err)
	}
}

func TestGetTargetCredentials_ReturnsCopy(t *testing.T) {
	provider := NewMockProvider()
	ctx := context.Background()

	// Get credentials twice
	creds1, err := provider.GetTargetCredentials(ctx, "dc-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	creds2, err := provider.GetTargetCredentials(ctx, "dc-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Modify the first result
	originalHostname := creds1.Hostname
	creds1.Hostname = "modified"

	// Second result should not be affected
	if creds2.Hostname != originalHostname {
		t.Errorf("modifying returned credentials affected other calls")
	}

	// Getting credentials again should return original value
	creds3, _ := provider.GetTargetCredentials(ctx, "dc-01")
	if creds3.Hostname != originalHostname {
		t.Errorf("modifying returned credentials affected provider storage")
	}
}
