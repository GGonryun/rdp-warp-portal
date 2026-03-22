package credential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// testSecrets maps secret names to passwords for testing.
var testSecrets = map[string]string{
	"secret/admin-pass":   "P@ssw0rd!",
	"secret/svc-rdp-pass": "Sup3rS3cret",
	"secret/rdpadmin":     "CHANGE_ME_BEFORE_DEPLOY",
	"secret/testpass":     "testpass123",
	"secret/adminpass":    "adminpass",
	"secret/newpass":      "newpass",
	"secret/concurrent":   "pass",
}

func testResolver(ctx context.Context, secretName string) (string, error) {
	if v, ok := testSecrets[secretName]; ok {
		return v, nil
	}
	return "", fmt.Errorf("test secret not found: %s", secretName)
}

// newTestProvider creates a GSMProvider with hardcoded targets and a test resolver.
func newTestProvider() *GSMProvider {
	return &GSMProvider{
		resolveSecret: testResolver,
		targets: map[string]gsmTarget{
			"dc-01": {
				Info: TargetInfo{
					ID:       "dc-01",
					Hostname: "dc-01",
					IP:       "10.0.1.10",
				},
				Port:   3389,
				Domain: "CORP",
				Users: []TargetUser{
					{Username: "Administrator", Secret: "secret/admin-pass"},
				},
			},
			"ws-05": {
				Info: TargetInfo{
					ID:       "ws-05",
					Hostname: "ws-05",
					IP:       "10.0.1.50",
				},
				Port:   3389,
				Domain: "CORP",
				Users: []TargetUser{
					{Username: "svc-rdp", Secret: "secret/svc-rdp-pass"},
				},
			},
			"win-vm-1": {
				Info: TargetInfo{
					ID:       "win-vm-1",
					Hostname: "win-vm-1",
					IP:       "20.64.171.136",
				},
				Port:   3389,
				Domain: "",
				Users: []TargetUser{
					{Username: "rdpadmin", Secret: "secret/rdpadmin"},
				},
			},
		},
	}
}

func TestNewTestProvider(t *testing.T) {
	provider := newTestProvider()
	if provider == nil {
		t.Fatal("newTestProvider returned nil")
	}

	count := provider.TargetCount()
	if count != 3 {
		t.Errorf("expected 3 default targets, got %d", count)
	}
}

func TestGetTargetCredentials_Success(t *testing.T) {
	provider := newTestProvider()
	ctx := context.Background()

	tests := []struct {
		targetID         string
		username         string
		expectedIP       string
		expectedPort     int
		expectedUsername string
		expectedDomain   string
	}{
		{
			targetID:         "dc-01",
			username:         "Administrator",
			expectedIP:       "10.0.1.10",
			expectedPort:     3389,
			expectedUsername: "Administrator",
			expectedDomain:   "CORP",
		},
		{
			targetID:         "ws-05",
			username:         "svc-rdp",
			expectedIP:       "10.0.1.50",
			expectedPort:     3389,
			expectedUsername: "svc-rdp",
			expectedDomain:   "CORP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.targetID, func(t *testing.T) {
			creds, err := provider.GetTargetCredentials(ctx, tt.targetID, tt.username)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if creds == nil {
				t.Fatal("credentials are nil")
			}

			if creds.IP != tt.expectedIP {
				t.Errorf("ip: got %q, want %q", creds.IP, tt.expectedIP)
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
			if creds.Password == "" {
				t.Error("password is empty")
			}
		})
	}
}

func TestGetTargetCredentials_NotFound(t *testing.T) {
	provider := newTestProvider()
	ctx := context.Background()

	creds, err := provider.GetTargetCredentials(ctx, "nonexistent-target", "admin")
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

func TestGetTargetCredentials_UserNotFound(t *testing.T) {
	provider := newTestProvider()
	ctx := context.Background()

	creds, err := provider.GetTargetCredentials(ctx, "dc-01", "nonexistent-user")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected ErrUserNotFound, got: %v", err)
	}
	if creds != nil {
		t.Error("credentials should be nil for nonexistent user")
	}
}

func TestGetTargetCredentials_NoResolver(t *testing.T) {
	provider := &GSMProvider{
		targets: map[string]gsmTarget{
			"test": {
				Info:  TargetInfo{ID: "test", Hostname: "test", IP: "1.2.3.4"},
				Port:  3389,
				Users: []TargetUser{{Username: "user", Secret: "secret/test"}},
			},
		},
	}
	ctx := context.Background()

	_, err := provider.GetTargetCredentials(ctx, "test", "user")
	if !errors.Is(err, ErrSecretResolverNotConfigured) {
		t.Errorf("expected ErrSecretResolverNotConfigured, got: %v", err)
	}
}

func TestGetTargetCredentials_ContextCanceled(t *testing.T) {
	provider := newTestProvider()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	creds, err := provider.GetTargetCredentials(ctx, "dc-01", "Administrator")
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
	provider := newTestProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	time.Sleep(time.Millisecond)

	creds, err := provider.GetTargetCredentials(ctx, "dc-01", "Administrator")
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
	provider := newTestProvider()
	ctx := context.Background()

	targets, err := provider.ListTargets(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(targets))
	}

	targetMap := make(map[string]TargetInfo)
	for _, target := range targets {
		targetMap[target.ID] = target
	}

	dc01, ok := targetMap["dc-01"]
	if !ok {
		t.Fatal("dc-01 not found in targets")
	}
	if dc01.Hostname != "dc-01" {
		t.Errorf("dc-01 hostname: got %q, want %q", dc01.Hostname, "dc-01")
	}
	if dc01.IP != "10.0.1.10" {
		t.Errorf("dc-01 ip: got %q, want %q", dc01.IP, "10.0.1.10")
	}
}

func TestListDestinations_Success(t *testing.T) {
	provider := newTestProvider()
	ctx := context.Background()

	destinations, err := provider.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(destinations) != 3 {
		t.Fatalf("expected 3 destinations, got %d", len(destinations))
	}

	destMap := make(map[string]TargetDestination)
	for _, d := range destinations {
		destMap[d.ID] = d
	}

	dc01, ok := destMap["dc-01"]
	if !ok {
		t.Fatal("dc-01 not found in destinations")
	}
	if len(dc01.Users) != 1 {
		t.Fatalf("expected 1 user for dc-01, got %d", len(dc01.Users))
	}
	if dc01.Users[0].Username != "Administrator" {
		t.Errorf("expected username 'Administrator', got %q", dc01.Users[0].Username)
	}
}

func TestListTargets_ContextCanceled(t *testing.T) {
	provider := newTestProvider()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

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

func TestNewGSMProvider(t *testing.T) {
	configContent := `{
  "targets": {
    "test-srv-01": {
      "hostname": "test-srv-01",
      "ip": "192.168.1.100",
      "port": 3390,
      "domain": "TESTDOMAIN",
      "users": [
        {"username": "testuser", "secret": "secret/testpass"},
        {"username": "admin", "secret": "secret/adminpass"}
      ]
    },
    "test-srv-02": {
      "hostname": "test-srv-02",
      "ip": "192.168.1.101",
      "domain": "TESTDOMAIN",
      "users": [
        {"username": "admin", "secret": "secret/adminpass"}
      ]
    }
  }
}`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-targets.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	provider, err := NewGSMProvider(configPath, testResolver)
	if err != nil {
		t.Fatalf("failed to load provider from config: %v", err)
	}

	if provider.TargetCount() != 2 {
		t.Errorf("expected 2 targets, got %d", provider.TargetCount())
	}

	ctx := context.Background()

	creds1, err := provider.GetTargetCredentials(ctx, "test-srv-01", "testuser")
	if err != nil {
		t.Fatalf("failed to get credentials for test-srv-01: %v", err)
	}
	if creds1.IP != "192.168.1.100" {
		t.Errorf("ip: got %q, want %q", creds1.IP, "192.168.1.100")
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

	creds2, err := provider.GetTargetCredentials(ctx, "test-srv-02", "admin")
	if err != nil {
		t.Fatalf("failed to get credentials for test-srv-02: %v", err)
	}
	if creds2.Port != 3389 {
		t.Errorf("default port: got %d, want %d", creds2.Port, 3389)
	}

	targets, err := provider.ListTargets(ctx)
	if err != nil {
		t.Fatalf("failed to list targets: %v", err)
	}
	if len(targets) != 2 {
		t.Errorf("expected 2 targets in list, got %d", len(targets))
	}

	destinations, err := provider.ListDestinations(ctx)
	if err != nil {
		t.Fatalf("failed to list destinations: %v", err)
	}
	if len(destinations) != 2 {
		t.Errorf("expected 2 destinations, got %d", len(destinations))
	}
}

func TestNewGSMProvider_FileNotFound(t *testing.T) {
	provider, err := NewGSMProvider("/nonexistent/path/config.json", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if provider != nil {
		t.Error("provider should be nil for nonexistent file")
	}
}

func TestNewGSMProvider_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.json")
	if err := os.WriteFile(configPath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	provider, err := NewGSMProvider(configPath, nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if provider != nil {
		t.Error("provider should be nil for invalid JSON")
	}
}

func TestNewGSMProvider_EmptyTargets(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "empty.json")
	if err := os.WriteFile(configPath, []byte(`{"targets": {}}`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	provider, err := NewGSMProvider(configPath, nil)
	if err != nil {
		t.Fatalf("unexpected error for empty targets: %v", err)
	}
	if provider.TargetCount() != 0 {
		t.Errorf("expected 0 targets, got %d", provider.TargetCount())
	}
}

func TestClose(t *testing.T) {
	provider := newTestProvider()
	err := provider.Close()
	if err != nil {
		t.Errorf("Close returned unexpected error: %v", err)
	}
}

func TestAddTarget(t *testing.T) {
	provider := newTestProvider()
	initialCount := provider.TargetCount()

	info := TargetInfo{
		ID:       "new-target",
		Hostname: "new-target",
		IP:       "192.168.2.100",
	}
	users := []TargetUser{
		{Username: "newuser", Secret: "secret/newpass"},
	}

	provider.AddTarget("new-target", info, 3389, "NEWDOMAIN", users)

	if provider.TargetCount() != initialCount+1 {
		t.Errorf("expected %d targets, got %d", initialCount+1, provider.TargetCount())
	}

	ctx := context.Background()
	retrieved, err := provider.GetTargetCredentials(ctx, "new-target", "newuser")
	if err != nil {
		t.Fatalf("failed to get new target: %v", err)
	}
	if retrieved.Username != "newuser" {
		t.Errorf("username: got %q, want %q", retrieved.Username, "newuser")
	}
}

func TestRemoveTarget(t *testing.T) {
	provider := newTestProvider()
	initialCount := provider.TargetCount()

	removed := provider.RemoveTarget("dc-01")
	if !removed {
		t.Error("RemoveTarget should return true for existing target")
	}
	if provider.TargetCount() != initialCount-1 {
		t.Errorf("expected %d targets, got %d", initialCount-1, provider.TargetCount())
	}

	ctx := context.Background()
	_, err := provider.GetTargetCredentials(ctx, "dc-01", "Administrator")
	if !errors.Is(err, ErrTargetNotFound) {
		t.Errorf("expected ErrTargetNotFound, got: %v", err)
	}

	removed = provider.RemoveTarget("nonexistent")
	if removed {
		t.Error("RemoveTarget should return false for nonexistent target")
	}
}

func TestConcurrentAccess(t *testing.T) {
	provider := newTestProvider()
	ctx := context.Background()

	const numGoroutines = 100
	const numOperations = 100

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*numOperations)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				switch j % 4 {
				case 0:
					_, err := provider.GetTargetCredentials(ctx, "dc-01", "Administrator")
					if err != nil && !errors.Is(err, ErrTargetNotFound) {
						errChan <- err
					}
				case 1:
					_, err := provider.ListTargets(ctx)
					if err != nil {
						errChan <- err
					}
				case 2:
					targetID := "concurrent-target"
					info := TargetInfo{ID: targetID, Hostname: "test", IP: "1.2.3.4"}
					users := []TargetUser{{Username: "test", Secret: "secret/concurrent"}}
					provider.AddTarget(targetID, info, 3389, "", users)
				case 3:
					provider.RemoveTarget("concurrent-target")
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		t.Errorf("concurrent access produced %d errors, first: %v", len(errs), errs[0])
	}
}

func TestConcurrentReads(t *testing.T) {
	provider := newTestProvider()
	ctx := context.Background()

	const numReaders = 50
	const numReads = 1000

	var wg sync.WaitGroup
	errChan := make(chan error, numReaders)

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numReads; j++ {
				creds, err := provider.GetTargetCredentials(ctx, "dc-01", "Administrator")
				if err != nil {
					errChan <- err
					return
				}
				if creds.IP != "10.0.1.10" {
					errChan <- errors.New("ip mismatch during concurrent read")
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("concurrent read error: %v", err)
	}
}

func TestGetTargetCredentials_ReturnsCopy(t *testing.T) {
	provider := newTestProvider()
	ctx := context.Background()

	creds1, err := provider.GetTargetCredentials(ctx, "dc-01", "Administrator")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	creds2, err := provider.GetTargetCredentials(ctx, "dc-01", "Administrator")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	originalIP := creds1.IP
	creds1.IP = "modified"

	if creds2.IP != originalIP {
		t.Errorf("modifying returned credentials affected other calls")
	}

	creds3, _ := provider.GetTargetCredentials(ctx, "dc-01", "Administrator")
	if creds3.IP != originalIP {
		t.Errorf("modifying returned credentials affected provider storage")
	}
}
