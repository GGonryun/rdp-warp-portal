package session

import (
	"fmt"
	"strings"
)

// RDPFileParams contains parameters for generating an RDP file.
type RDPFileParams struct {
	// BrokerHost is the public hostname for RDP connections (e.g., "broker.example.com")
	BrokerHost string

	// Port is the gatekeeper port for this session
	Port int

	// UserID is the user's identity for the cookie (e.g., "john.doe")
	UserID string

	// TargetID is the target machine identifier (e.g., "dc-01")
	TargetID string

	// Token is the one-time session token
	Token string

	// Domain is the cosmetic domain shown in the RDP file (e.g., "YOURORG")
	Domain string
}

// GenerateRDPFile creates an RDP file content with embedded session token.
//
// The username field embeds three values delimited by '#':
//   - User identity (for audit logging)
//   - Target ID (for session lookup)
//   - One-time session token
//
// The RDP file is configured to:
//   - Connect to the broker's gatekeeper port (not directly to the proxy)
//   - Disable credential prompts (prompt for credentials:i:0)
//   - Disable CredSSP/NLA on the client-to-gatekeeper leg
//   - Use standard RDP security negotiation
func GenerateRDPFile(params RDPFileParams) []byte {
	// Build the compound username: username#target_id#token
	// Replace '@' in UserID to prevent RDP clients from interpreting it as a UPN
	// (user@domain), which splits the username and strips the token.
	safeUserID := strings.ReplaceAll(params.UserID, "@", "%40")
	compoundUsername := fmt.Sprintf("%s#%s#%s", safeUserID, params.TargetID, params.Token)

	// Build loadbalanceinfo — mstsc sends this verbatim as bytes in the X.224
	// Connection Request. The username:s: field alone is NOT sent as a cookie.
	// mstsc treats the :s: value as raw bytes to embed in the routing token.
	lbInfo := "Cookie: mstshash=" + compoundUsername + "\r\n"

	// Build the RDP file content
	lines := []string{
		// Connection settings
		fmt.Sprintf("full address:s:%s:%d", params.BrokerHost, params.Port),
		fmt.Sprintf("username:s:%s", compoundUsername),
		fmt.Sprintf("domain:s:%s", params.Domain),
		fmt.Sprintf("loadbalanceinfo:s:%s", lbInfo),

		// Disable credential prompts - we don't want users entering passwords
		"prompt for credentials:i:0",

		// Security settings
		// authentication level 0: Connect even without server cert verification
		"authentication level:i:0",

		// negotiate security layer: Use standard RDP security negotiation
		"negotiate security layer:i:1",

		// Disable CredSSP/NLA on the client-to-broker leg
		// The proxy handles NLA to the backend
		"enablecredsspsupport:i:0",

		// Additional security settings
		"autoreconnection enabled:i:0",

		// Display settings - reasonable defaults
		"screen mode id:i:2",
		"desktopwidth:i:1920",
		"desktopheight:i:1080",
		"session bpp:i:32",
		"smart sizing:i:1",

		// Compression for better performance
		"compression:i:1",

		// Disable connection bar for cleaner experience
		"displayconnectionbar:i:0",

		// Allow font smoothing
		"allow font smoothing:i:1",

		// Disable auto-reconnect to prevent token reuse issues
		"autoreconnection enabled:i:0",

		// Redirect settings - enable common redirections
		"redirectclipboard:i:1",
		"redirectprinters:i:0",
		"redirectcomports:i:0",
		"redirectsmartcards:i:0",
		"redirectposdevices:i:0",
		"redirectdrives:i:0",

		// Audio settings
		"audiomode:i:0",
		"audiocapturemode:i:0",
	}

	// Join with CRLF as per RDP file spec
	content := strings.Join(lines, "\r\n") + "\r\n"

	return []byte(content)
}

// RDPFilename generates an appropriate filename for the RDP file.
func RDPFilename(targetID string) string {
	// Sanitize targetID for use as filename
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, targetID)

	if safe == "" {
		safe = "connection"
	}

	return safe + ".rdp"
}
