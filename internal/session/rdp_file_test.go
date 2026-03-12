package session

import (
	"strings"
	"testing"
)

func TestGenerateRDPFile(t *testing.T) {
	params := RDPFileParams{
		BrokerHost: "broker.example.com",
		Port:       33400,
		UserID:     "john.doe",
		TargetID:   "dc-01",
		Token:      "a8Kx2nPqLmZ9wR4vXyZ123AbC456dEf789gHi",
		Domain:     "YOURORG",
	}

	content := GenerateRDPFile(params)
	contentStr := string(content)

	// Verify required fields
	tests := []struct {
		name     string
		expected string
	}{
		{"full address", "full address:s:broker.example.com:33400"},
		{"username with token", "username:s:john.doe#dc-01#a8Kx2nPqLmZ9wR4vXyZ123AbC456dEf789gHi"},
		{"domain", "domain:s:YOURORG"},
		{"no credential prompt", "prompt for credentials:i:0"},
		{"authentication level", "authentication level:i:0"},
		{"negotiate security", "negotiate security layer:i:1"},
		{"no credSSP", "enablecredsspsupport:i:0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(contentStr, tt.expected) {
				t.Errorf("expected to contain %q", tt.expected)
			}
		})
	}
}

func TestGenerateRDPFile_CRLF(t *testing.T) {
	params := RDPFileParams{
		BrokerHost: "broker.example.com",
		Port:       33400,
		UserID:     "user",
		TargetID:   "target",
		Token:      "token123",
		Domain:     "DOMAIN",
	}

	content := GenerateRDPFile(params)
	contentStr := string(content)

	// Verify CRLF line endings (Windows standard for RDP files)
	if !strings.Contains(contentStr, "\r\n") {
		t.Error("RDP file should use CRLF line endings")
	}

	// Should end with CRLF
	if !strings.HasSuffix(contentStr, "\r\n") {
		t.Error("RDP file should end with CRLF")
	}

	// Should not have LF without CR (Unix line endings)
	lines := strings.Split(contentStr, "\r\n")
	for i, line := range lines[:len(lines)-1] { // Ignore last empty element
		if strings.Contains(line, "\n") {
			t.Errorf("line %d contains bare LF", i)
		}
	}
}

func TestGenerateRDPFile_SpecialCharactersInToken(t *testing.T) {
	// Base64url encoding uses A-Za-z0-9-_
	params := RDPFileParams{
		BrokerHost: "broker.example.com",
		Port:       33400,
		UserID:     "user",
		TargetID:   "target",
		Token:      "Ab12Cd34-Ef56_Gh78Ij90KlMnOpQrStUvWxYz",
		Domain:     "DOMAIN",
	}

	content := GenerateRDPFile(params)
	contentStr := string(content)

	expectedUsername := "username:s:user#target#Ab12Cd34-Ef56_Gh78Ij90KlMnOpQrStUvWxYz"
	if !strings.Contains(contentStr, expectedUsername) {
		t.Errorf("expected to contain %q", expectedUsername)
	}
}

func TestGenerateRDPFile_DifferentPorts(t *testing.T) {
	ports := []int{33400, 33450, 33500, 44100}

	for _, port := range ports {
		params := RDPFileParams{
			BrokerHost: "broker.example.com",
			Port:       port,
			UserID:     "user",
			TargetID:   "target",
			Token:      "token",
			Domain:     "DOMAIN",
		}

		content := GenerateRDPFile(params)
		expected := "full address:s:broker.example.com:" + strings.TrimLeft(string(rune(port)), "0")

		// Better port check
		expectedFull := "full address:s:broker.example.com:" + itoa(port)
		if !strings.Contains(string(content), expectedFull) {
			t.Errorf("port %d: expected to contain %q", port, expected)
		}
	}
}

// Simple int to string for testing
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var s string
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestGenerateRDPFile_IPAddress(t *testing.T) {
	params := RDPFileParams{
		BrokerHost: "192.168.1.100",
		Port:       33400,
		UserID:     "user",
		TargetID:   "target",
		Token:      "token",
		Domain:     "DOMAIN",
	}

	content := GenerateRDPFile(params)
	if !strings.Contains(string(content), "full address:s:192.168.1.100:33400") {
		t.Error("expected IP address in full address")
	}
}

func TestGenerateRDPFile_LocalhostDevelopment(t *testing.T) {
	params := RDPFileParams{
		BrokerHost: "localhost",
		Port:       33400,
		UserID:     "devuser",
		TargetID:   "dev-vm",
		Token:      "devtoken123",
		Domain:     "DEV",
	}

	content := GenerateRDPFile(params)
	if !strings.Contains(string(content), "full address:s:localhost:33400") {
		t.Error("expected localhost in full address")
	}
}

func TestRDPFilename(t *testing.T) {
	tests := []struct {
		targetID string
		expected string
	}{
		{"dc-01", "dc-01.rdp"},
		{"workstation_5", "workstation_5.rdp"},
		{"server123", "server123.rdp"},
		{"DC-ALPHA-01", "DC-ALPHA-01.rdp"},
		// Special characters should be replaced with underscore
		{"server/bad", "server_bad.rdp"},
		{"server:bad", "server_bad.rdp"},
		{"server bad", "server_bad.rdp"},
		{"server.bad", "server_bad.rdp"},
		{"server\\bad", "server_bad.rdp"},
		// Edge cases
		{"", "connection.rdp"},
		{"...", "___.rdp"},
	}

	for _, tt := range tests {
		t.Run(tt.targetID, func(t *testing.T) {
			got := RDPFilename(tt.targetID)
			if got != tt.expected {
				t.Errorf("RDPFilename(%q) = %q, want %q", tt.targetID, got, tt.expected)
			}
		})
	}
}

func TestGenerateRDPFile_DisplaySettings(t *testing.T) {
	params := RDPFileParams{
		BrokerHost: "broker.example.com",
		Port:       33400,
		UserID:     "user",
		TargetID:   "target",
		Token:      "token",
		Domain:     "DOMAIN",
	}

	content := GenerateRDPFile(params)
	contentStr := string(content)

	// Check display-related settings are present
	displaySettings := []string{
		"screen mode id:i:",
		"desktopwidth:i:",
		"desktopheight:i:",
		"session bpp:i:",
	}

	for _, setting := range displaySettings {
		if !strings.Contains(contentStr, setting) {
			t.Errorf("expected display setting %q", setting)
		}
	}
}

func TestGenerateRDPFile_ClipboardEnabled(t *testing.T) {
	params := RDPFileParams{
		BrokerHost: "broker.example.com",
		Port:       33400,
		UserID:     "user",
		TargetID:   "target",
		Token:      "token",
		Domain:     "DOMAIN",
	}

	content := GenerateRDPFile(params)
	if !strings.Contains(string(content), "redirectclipboard:i:1") {
		t.Error("expected clipboard to be enabled")
	}
}

func TestGenerateRDPFile_AutoReconnectDisabled(t *testing.T) {
	params := RDPFileParams{
		BrokerHost: "broker.example.com",
		Port:       33400,
		UserID:     "user",
		TargetID:   "target",
		Token:      "token",
		Domain:     "DOMAIN",
	}

	content := GenerateRDPFile(params)
	// Auto-reconnect should be disabled to prevent token reuse issues
	if !strings.Contains(string(content), "autoreconnection enabled:i:0") {
		t.Error("expected auto-reconnect to be disabled")
	}
}
