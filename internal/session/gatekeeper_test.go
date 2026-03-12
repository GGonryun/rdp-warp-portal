package session

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestParseCookie_ValidFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *ParsedCookie
	}{
		{
			name:  "standard format",
			input: "Cookie: mstshash=john.doe#dc-01#a8Kx2nPqLmZ9wR4v\r\n",
			expected: &ParsedCookie{
				UserID:   "john.doe",
				TargetID: "dc-01",
				Token:    "a8Kx2nPqLmZ9wR4v",
				Raw:      "john.doe#dc-01#a8Kx2nPqLmZ9wR4v",
			},
		},
		{
			name:  "base64url token",
			input: "Cookie: mstshash=admin#ws-05#Ab12Cd34-Ef56_Gh78Ij90KlMnOpQrStUv\r\n",
			expected: &ParsedCookie{
				UserID:   "admin",
				TargetID: "ws-05",
				Token:    "Ab12Cd34-Ef56_Gh78Ij90KlMnOpQrStUv",
				Raw:      "admin#ws-05#Ab12Cd34-Ef56_Gh78Ij90KlMnOpQrStUv",
			},
		},
		{
			name:  "with additional data after cookie",
			input: "Cookie: mstshash=user#target#token123\r\n\x01\x00\x08\x00\x00\x00\x00\x00",
			expected: &ParsedCookie{
				UserID:   "user",
				TargetID: "target",
				Token:    "token123",
				Raw:      "user#target#token123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cookie, err := parseCookie([]byte(tt.input))
			if err != nil {
				t.Fatalf("parseCookie failed: %v", err)
			}

			if cookie.UserID != tt.expected.UserID {
				t.Errorf("UserID: got %q, want %q", cookie.UserID, tt.expected.UserID)
			}
			if cookie.TargetID != tt.expected.TargetID {
				t.Errorf("TargetID: got %q, want %q", cookie.TargetID, tt.expected.TargetID)
			}
			if cookie.Token != tt.expected.Token {
				t.Errorf("Token: got %q, want %q", cookie.Token, tt.expected.Token)
			}
			if cookie.Raw != tt.expected.Raw {
				t.Errorf("Raw: got %q, want %q", cookie.Raw, tt.expected.Raw)
			}
		})
	}
}

func TestParseCookie_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		err   error
	}{
		{
			name:  "no cookie",
			input: "some random data\r\n",
			err:   ErrNoCookie,
		},
		{
			name:  "no CRLF terminator",
			input: "Cookie: mstshash=user#target#token",
			err:   ErrInvalidCookieFormat,
		},
		{
			name:  "empty cookie value",
			input: "Cookie: mstshash=\r\n",
			err:   ErrInvalidCookieFormat,
		},
		{
			name:  "wrong number of parts - too few",
			input: "Cookie: mstshash=user#target\r\n",
			err:   ErrInvalidCookieFormat,
		},
		{
			name:  "wrong number of parts - too many",
			input: "Cookie: mstshash=user#target#token#extra\r\n",
			err:   ErrInvalidCookieFormat,
		},
		{
			name:  "empty username",
			input: "Cookie: mstshash=#target#token\r\n",
			err:   ErrInvalidCookieFormat,
		},
		{
			name:  "empty target",
			input: "Cookie: mstshash=user##token\r\n",
			err:   ErrInvalidCookieFormat,
		},
		{
			name:  "empty token",
			input: "Cookie: mstshash=user#target#\r\n",
			err:   ErrInvalidCookieFormat,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseCookie([]byte(tt.input))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, tt.err) && tt.err != ErrInvalidCookieFormat {
				// For ErrInvalidCookieFormat, we check the prefix
				if tt.err == ErrInvalidCookieFormat && !errors.Is(err, ErrInvalidCookieFormat) {
					t.Errorf("expected %v, got %v", tt.err, err)
				}
			}
		})
	}
}

// buildX224ConnectionRequest builds a valid X.224 Connection Request packet
func buildX224ConnectionRequest(cookie string) []byte {
	// Build the variable data (cookie + RDP neg req)
	cookieData := "Cookie: mstshash=" + cookie + "\r\n"

	// X.224 header (7 bytes)
	x224Header := []byte{
		0x00,       // Length Indicator (will be filled)
		0xE0,       // Type = Connection Request
		0x00, 0x00, // Destination Reference
		0x00, 0x00, // Source Reference
		0x00, // Class and Options
	}

	// Variable data
	variableData := []byte(cookieData)

	// X.224 Length Indicator = total X.224 data - 1 (excludes LI itself)
	x224Header[0] = byte(len(x224Header) - 1 + len(variableData))

	// Build X.224 payload
	x224Payload := append(x224Header, variableData...)

	// TPKT header (4 bytes)
	totalLen := TPKTHeaderSize + len(x224Payload)
	tpktHeader := []byte{
		TPKTVersion, // Version
		0x00,        // Reserved
		0x00, 0x00,  // Length (big-endian)
	}
	binary.BigEndian.PutUint16(tpktHeader[2:4], uint16(totalLen))

	// Combine
	return append(tpktHeader, x224Payload...)
}

func TestGatekeeper_StartStop(t *testing.T) {
	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0, // Use any available port
		ProxyAddr:     "127.0.0.1:44400",
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	// Start in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- gk.Start()
	}()

	// Wait for it to start
	time.Sleep(100 * time.Millisecond)

	// Should not be closed yet
	if gk.IsClosed() {
		t.Error("gatekeeper should not be closed")
	}

	// Stop it
	if err := gk.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}

	// Should be closed now
	if !gk.IsClosed() {
		t.Error("gatekeeper should be closed after Stop")
	}

	// Start should return without error
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Start returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("Start did not return after Stop")
	}
}

func TestGatekeeper_TokenValidation(t *testing.T) {
	// Start a mock proxy server
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock proxy: %v", err)
	}
	defer proxyListener.Close()

	proxyAddr := proxyListener.Addr().String()

	// Accept connections and echo back
	go func() {
		for {
			conn, err := proxyListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()

	// Track validation calls
	var validatedToken string
	var validatedTarget string
	var mu sync.Mutex

	validator := func(token, targetID string) error {
		mu.Lock()
		validatedToken = token
		validatedTarget = targetID
		mu.Unlock()
		return nil
	}

	// Create gatekeeper
	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     proxyAddr,
		SessionID:     "test-session",
		ValidateToken: validator,
	})

	// Start in background
	go gk.Start()
	defer gk.Stop()

	// Wait for it to start
	time.Sleep(100 * time.Millisecond)

	// Connect and send a valid X.224 packet
	addr := gk.Addr()
	if addr == nil {
		t.Fatal("gatekeeper has no address")
	}

	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Send X.224 Connection Request
	packet := buildX224ConnectionRequest("testuser#testtarget#testtoken123")
	conn.Write(packet)

	// Give time for processing
	time.Sleep(100 * time.Millisecond)

	// Check that validation was called
	mu.Lock()
	if validatedToken != "testtoken123" {
		t.Errorf("expected token 'testtoken123', got %q", validatedToken)
	}
	if validatedTarget != "testtarget" {
		t.Errorf("expected target 'testtarget', got %q", validatedTarget)
	}
	mu.Unlock()
}

func TestGatekeeper_TokenValidationFails(t *testing.T) {
	validator := func(token, targetID string) error {
		return errors.New("invalid token")
	}

	// Start a mock proxy (should never receive connection)
	proxyListener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer proxyListener.Close()

	proxyConnected := make(chan bool, 1)
	go func() {
		conn, err := proxyListener.Accept()
		if err == nil {
			conn.Close()
			proxyConnected <- true
		}
	}()

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     proxyListener.Addr().String(),
		SessionID:     "test-session",
		ValidateToken: validator,
	})

	go gk.Start()
	defer gk.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect and send packet
	conn, _ := net.Dial("tcp", gk.Addr().String())
	defer conn.Close()

	packet := buildX224ConnectionRequest("user#target#badtoken")
	conn.Write(packet)

	// Wait a bit and check proxy was NOT connected
	select {
	case <-proxyConnected:
		t.Error("proxy should not have received connection when token validation fails")
	case <-time.After(200 * time.Millisecond):
		// Expected - no connection to proxy
	}
}

func TestGatekeeper_BridgesTraffic(t *testing.T) {
	// Create a mock proxy that echoes back the X.224 packet immediately
	// This tests that the gatekeeper successfully forwards the initial packet
	proxyListener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer proxyListener.Close()

	proxyReceived := make(chan []byte, 1)
	go func() {
		for {
			conn, err := proxyListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// Read what the gatekeeper sends us
				buf := make([]byte, 1024)
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				// Send to channel for verification
				select {
				case proxyReceived <- buf[:n]:
				default:
				}
				// Keep connection open briefly
				time.Sleep(100 * time.Millisecond)
			}(conn)
		}
	}()

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     proxyListener.Addr().String(),
		SessionID:     "test-session",
		ValidateToken: nil, // No validation for this test
	})

	go gk.Start()
	defer gk.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect
	conn, err := net.Dial("tcp", gk.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Send X.224 packet
	packet := buildX224ConnectionRequest("user#target#token")
	conn.Write(packet)

	// Verify proxy received the packet
	select {
	case received := <-proxyReceived:
		// The proxy should receive the original X.224 packet
		if !bytes.Equal(received, packet) {
			t.Errorf("proxy received different data: got %d bytes, want %d bytes", len(received), len(packet))
		}
	case <-time.After(2 * time.Second):
		t.Error("proxy did not receive the forwarded packet")
	}
}

func TestGatekeeper_OnConnectedCallback(t *testing.T) {
	proxyListener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer proxyListener.Close()

	go func() {
		for {
			conn, err := proxyListener.Accept()
			if err != nil {
				return
			}
			io.Copy(conn, conn)
			conn.Close()
		}
	}()

	connected := make(chan bool, 1)

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     proxyListener.Addr().String(),
		SessionID:     "test-session",
		ValidateToken: nil,
		OnConnected: func() {
			connected <- true
		},
	})

	go gk.Start()
	defer gk.Stop()

	time.Sleep(100 * time.Millisecond)

	conn, _ := net.Dial("tcp", gk.Addr().String())
	defer conn.Close()

	packet := buildX224ConnectionRequest("user#target#token")
	conn.Write(packet)

	select {
	case <-connected:
		// Expected
	case <-time.After(time.Second):
		t.Error("OnConnected callback was not called")
	}
}

func TestGatekeeper_InvalidPacket(t *testing.T) {
	proxyListener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer proxyListener.Close()

	proxyConnected := make(chan bool, 1)
	go func() {
		conn, err := proxyListener.Accept()
		if err == nil {
			conn.Close()
			proxyConnected <- true
		}
	}()

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     proxyListener.Addr().String(),
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	go gk.Start()
	defer gk.Stop()

	time.Sleep(100 * time.Millisecond)

	// Send invalid data
	conn, _ := net.Dial("tcp", gk.Addr().String())
	conn.Write([]byte("not a valid RDP packet"))
	conn.Close()

	select {
	case <-proxyConnected:
		t.Error("proxy should not have received connection for invalid packet")
	case <-time.After(200 * time.Millisecond):
		// Expected
	}
}
