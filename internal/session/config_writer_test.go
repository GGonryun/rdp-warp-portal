package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/p0-security/rdp-broker/internal/credential"
)

func TestNewConfigWriter(t *testing.T) {
	writer, err := NewConfigWriter("/etc/certs", "/tmp/sessions")
	if err != nil {
		t.Fatalf("NewConfigWriter failed: %v", err)
	}
	if writer == nil {
		t.Fatal("NewConfigWriter returned nil")
	}
}

func TestWriteConfig(t *testing.T) {
	tmpDir := t.TempDir()
	certDir := filepath.Join(tmpDir, "certs")
	sessionDir := filepath.Join(tmpDir, "sessions")

	os.MkdirAll(certDir, 0755)
	os.MkdirAll(sessionDir, 0755)

	writer, err := NewConfigWriter(certDir, sessionDir)
	if err != nil {
		t.Fatalf("NewConfigWriter failed: %v", err)
	}

	creds := &credential.TargetCredentials{
		Hostname: "10.0.1.10",
		Port:     3389,
		Username: "Administrator",
		Password: "P@ssw0rd!",
		Domain:   "CORP",
	}

	configPath, err := writer.WriteConfig("test-session-123", 44400, creds)
	if err != nil {
		t.Fatalf("WriteConfig failed: %v", err)
	}

	// Verify path is correct
	expectedPath := filepath.Join(sessionDir, "test-session-123", "proxy.ini")
	if configPath != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, configPath)
	}

	// Verify file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("config file was not created")
	}

	// Verify file permissions
	info, _ := os.Stat(configPath)
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected permissions 0600, got %o", info.Mode().Perm())
	}

	// Read and verify content
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}

	contentStr := string(content)

	// Check required fields
	checks := []struct {
		name     string
		expected string
	}{
		{"Server Host", "Host = 127.0.0.1"},
		{"Server Port", "Port = 44400"},
		{"FixedTarget", "FixedTarget = true"},
		{"Target Host", "Host = 10.0.1.10"},
		{"Target Port", "Port = 3389"},
		{"User", "User = Administrator"},
		{"Password", "Password = P@ssw0rd!"},
		{"Domain", "Domain = CORP"},
		{"Certificate", "CertificateFile = " + filepath.Join(certDir, "server.crt")},
		{"PrivateKey", "PrivateKeyFile = " + filepath.Join(certDir, "server.key")},
		{"ServerNlaSecurity", "ServerNlaSecurity = false"},
		{"ClientNlaSecurity", "ClientNlaSecurity = true"},
	}

	for _, check := range checks {
		if !strings.Contains(contentStr, check.expected) {
			t.Errorf("%s: expected to contain %q", check.name, check.expected)
		}
	}
}

func TestWriteConfig_CreatesSessionDir(t *testing.T) {
	tmpDir := t.TempDir()
	certDir := filepath.Join(tmpDir, "certs")
	sessionDir := filepath.Join(tmpDir, "sessions")

	// Don't create sessionDir - WriteConfig should create it
	os.MkdirAll(certDir, 0755)

	writer, _ := NewConfigWriter(certDir, sessionDir)

	creds := &credential.TargetCredentials{
		Hostname: "10.0.1.10",
		Port:     3389,
		Username: "admin",
		Password: "pass",
		Domain:   "DOMAIN",
	}

	_, err := writer.WriteConfig("new-session", 44400, creds)
	if err != nil {
		t.Fatalf("WriteConfig failed: %v", err)
	}

	// Verify session directory was created
	sessionPath := filepath.Join(sessionDir, "new-session")
	info, err := os.Stat(sessionPath)
	if err != nil {
		t.Fatalf("session directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("session path is not a directory")
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("expected directory permissions 0700, got %o", info.Mode().Perm())
	}
}

func TestDeleteConfig(t *testing.T) {
	tmpDir := t.TempDir()
	writer, _ := NewConfigWriter(tmpDir, tmpDir)

	creds := &credential.TargetCredentials{
		Hostname: "10.0.1.10",
		Port:     3389,
		Username: "admin",
		Password: "pass",
		Domain:   "DOMAIN",
	}

	// Write a config
	configPath, _ := writer.WriteConfig("delete-test", 44400, creds)

	// Verify it exists
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file doesn't exist: %v", err)
	}

	// Delete it
	err := writer.DeleteConfig("delete-test")
	if err != nil {
		t.Fatalf("DeleteConfig failed: %v", err)
	}

	// Verify it's gone
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Error("config file still exists after deletion")
	}
}

func TestCleanupSession(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "sessions")
	writer, _ := NewConfigWriter(tmpDir, sessionDir)

	creds := &credential.TargetCredentials{
		Hostname: "10.0.1.10",
		Port:     3389,
		Username: "admin",
		Password: "pass",
		Domain:   "DOMAIN",
	}

	// Write a config
	writer.WriteConfig("cleanup-test", 44400, creds)

	// Verify session directory exists
	sessionPath := filepath.Join(sessionDir, "cleanup-test")
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("session directory doesn't exist: %v", err)
	}

	// Cleanup the session
	err := writer.CleanupSession("cleanup-test")
	if err != nil {
		t.Fatalf("CleanupSession failed: %v", err)
	}

	// Verify directory is gone
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Error("session directory still exists after cleanup")
	}
}

func TestGenerateConfigBytes(t *testing.T) {
	writer, _ := NewConfigWriter("/etc/certs", "/tmp/sessions")

	creds := &credential.TargetCredentials{
		Hostname: "192.168.1.100",
		Port:     3390,
		Username: "testuser",
		Password: "testpass",
		Domain:   "TESTDOM",
	}

	content, err := writer.GenerateConfigBytes(44500, creds)
	if err != nil {
		t.Fatalf("GenerateConfigBytes failed: %v", err)
	}

	contentStr := string(content)

	// Verify content
	checks := []struct {
		name     string
		expected string
	}{
		{"Server Port", "Port = 44500"},
		{"Target Host", "Host = 192.168.1.100"},
		{"Target Port", "Port = 3390"},
		{"User", "User = testuser"},
		{"Password", "Password = testpass"},
		{"Domain", "Domain = TESTDOM"},
	}

	for _, check := range checks {
		if !strings.Contains(contentStr, check.expected) {
			t.Errorf("%s: expected to contain %q", check.name, check.expected)
		}
	}
}

func TestWriteConfig_SpecialCharactersInPassword(t *testing.T) {
	tmpDir := t.TempDir()
	writer, _ := NewConfigWriter(tmpDir, tmpDir)

	// Password with special characters
	creds := &credential.TargetCredentials{
		Hostname: "10.0.1.10",
		Port:     3389,
		Username: "admin",
		Password: `P@ss=w0rd!#$%^&*()[]{}|\:";'<>?,./`,
		Domain:   "DOMAIN",
	}

	configPath, err := writer.WriteConfig("special-chars", 44400, creds)
	if err != nil {
		t.Fatalf("WriteConfig failed with special characters: %v", err)
	}

	content, _ := os.ReadFile(configPath)
	if !strings.Contains(string(content), creds.Password) {
		t.Error("password with special characters not written correctly")
	}
}

func TestWriteConfig_EmptyDomain(t *testing.T) {
	tmpDir := t.TempDir()
	writer, _ := NewConfigWriter(tmpDir, tmpDir)

	creds := &credential.TargetCredentials{
		Hostname: "10.0.1.10",
		Port:     3389,
		Username: "localadmin",
		Password: "password",
		Domain:   "", // Empty domain for local accounts
	}

	configPath, err := writer.WriteConfig("empty-domain", 44400, creds)
	if err != nil {
		t.Fatalf("WriteConfig failed with empty domain: %v", err)
	}

	content, _ := os.ReadFile(configPath)
	if !strings.Contains(string(content), "Domain = \n") {
		t.Error("empty domain not written correctly")
	}
}

func TestWriteConfig_OverwritesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	writer, _ := NewConfigWriter(tmpDir, tmpDir)

	sessionID := "overwrite-test"

	creds1 := &credential.TargetCredentials{
		Hostname: "10.0.1.10",
		Port:     3389,
		Username: "user1",
		Password: "pass1",
		Domain:   "DOM1",
	}

	creds2 := &credential.TargetCredentials{
		Hostname: "10.0.2.20",
		Port:     3390,
		Username: "user2",
		Password: "pass2",
		Domain:   "DOM2",
	}

	// Write first config
	configPath, _ := writer.WriteConfig(sessionID, 44400, creds1)

	// Write second config (should overwrite)
	_, err := writer.WriteConfig(sessionID, 44500, creds2)
	if err != nil {
		t.Fatalf("WriteConfig failed to overwrite: %v", err)
	}

	content, _ := os.ReadFile(configPath)
	contentStr := string(content)

	// Should contain new values, not old ones
	if strings.Contains(contentStr, "user1") || strings.Contains(contentStr, "10.0.1.10") {
		t.Error("old config values still present after overwrite")
	}
	if !strings.Contains(contentStr, "user2") || !strings.Contains(contentStr, "10.0.2.20") {
		t.Error("new config values not present after overwrite")
	}
}
