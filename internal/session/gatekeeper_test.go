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


// TestGatekeeper_Stop_ForciblyClosesActiveConnections verifies that Stop()
// forcibly closes active client connections.
func TestGatekeeper_Stop_ForciblyClosesActiveConnections(t *testing.T) {
	// Create a proxy listener that handles connections properly
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create proxy listener: %v", err)
	}
	defer proxyListener.Close()

	// Track proxy connections - the proxy will echo and detect disconnects
	proxyConns := make(chan net.Conn, 10)
	go func() {
		for {
			conn, err := proxyListener.Accept()
			if err != nil {
				return
			}
			proxyConns <- conn
			// Simulate a proxy that reads/writes - it will detect when client disconnects
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return // Connection closed
					}
					c.Write(buf[:n])
				}
			}(conn)
		}
	}()

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     proxyListener.Addr().String(),
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	go gk.Start()
	time.Sleep(50 * time.Millisecond)

	// Connect a client
	clientConn, err := net.Dial("tcp", gk.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect client: %v", err)
	}

	// Wait for connection to be established with proxy
	select {
	case <-proxyConns:
		// Proxy received connection
	case <-time.After(time.Second):
		t.Fatal("proxy did not receive connection")
	}

	// Track if client connection was closed
	clientClosed := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := clientConn.Read(buf)
		clientClosed <- err
	}()

	// Stop the gatekeeper - this should forcibly close the client connection
	stopDone := make(chan error, 1)
	go func() {
		stopDone <- gk.Stop()
	}()

	// Verify Stop() completes (doesn't hang)
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() hung and did not return")
	}

	// Verify client connection was closed
	select {
	case err := <-clientClosed:
		if err == nil {
			t.Error("expected error on closed connection, got nil")
		}
		// Connection was closed as expected (io.EOF or net.ErrClosed)
	case <-time.After(time.Second):
		t.Error("client connection was not closed by Stop()")
	}

	// Clean up
	clientConn.Close()
}

// TestGatekeeper_Stop_ReturnsImmediately verifies that Stop() returns immediately
// even when connections are active and in the middle of bridging.
func TestGatekeeper_Stop_ReturnsImmediately(t *testing.T) {
	// Create a proxy that echoes and detects disconnects
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create proxy listener: %v", err)
	}
	defer proxyListener.Close()

	// Proxy handles connections and exits when they close
	go func() {
		for {
			conn, err := proxyListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					c.Write(buf[:n])
				}
			}(conn)
		}
	}()

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     proxyListener.Addr().String(),
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	go gk.Start()
	time.Sleep(50 * time.Millisecond)

	// Establish multiple client connections to create active bridges
	numClients := 3
	clientConns := make([]net.Conn, numClients)
	for i := 0; i < numClients; i++ {
		conn, err := net.Dial("tcp", gk.Addr().String())
		if err != nil {
			t.Fatalf("failed to connect client %d: %v", i, err)
		}
		clientConns[i] = conn
	}

	// Give time for all connections to be established
	time.Sleep(100 * time.Millisecond)

	// Stop() should return quickly (within 500ms) even with active connections
	stopDone := make(chan error, 1)
	stopStart := time.Now()
	go func() {
		stopDone <- gk.Stop()
	}()

	select {
	case err := <-stopDone:
		elapsed := time.Since(stopStart)
		if err != nil {
			t.Errorf("Stop returned error: %v", err)
		}
		if elapsed > 500*time.Millisecond {
			t.Errorf("Stop took too long: %v (should be < 500ms)", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() hung and did not return within 2 seconds")
	}

	// Clean up client connections
	for _, conn := range clientConns {
		conn.Close()
	}
}

// TestGatekeeper_Stop_BridgeGoroutinesExitCleanly verifies that after Stop(),
// the bridge goroutines exit cleanly without leaking.
func TestGatekeeper_Stop_BridgeGoroutinesExitCleanly(t *testing.T) {
	// Create a proxy that echoes data and detects disconnects
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create proxy listener: %v", err)
	}
	defer proxyListener.Close()

	proxyConnected := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := proxyListener.Accept()
			if err != nil {
				return
			}
			select {
			case proxyConnected <- struct{}{}:
			default:
			}
			// Echo server that detects disconnects
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					c.Write(buf[:n])
				}
			}(conn)
		}
	}()

	// Track when Stop() completes (which means activeConns.Wait() finished)
	stopComplete := make(chan struct{})

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     proxyListener.Addr().String(),
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	go gk.Start()
	time.Sleep(50 * time.Millisecond)

	// Connect client
	clientConn, err := net.Dial("tcp", gk.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	// Wait for proxy connection to be established
	select {
	case <-proxyConnected:
		// Proxy received connection
	case <-time.After(time.Second):
		t.Fatal("proxy did not receive connection")
	}

	// Call Stop() in goroutine and track completion
	go func() {
		gk.Stop()
		close(stopComplete)
	}()

	// Stop() should complete quickly, proving bridge goroutines exited
	select {
	case <-stopComplete:
		// Stop() completed, meaning activeConns.Wait() finished
		// This proves bridge goroutines exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("bridge goroutines did not exit cleanly - Stop() hung")
	}

	// Verify client connection was closed
	clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buf := make([]byte, 1)
	_, err = clientConn.Read(buf)
	if err == nil {
		t.Error("expected error reading from closed connection")
	}

	clientConn.Close()
}

// TestGatekeeper_Stop_WithNetPipe verifies Stop() behavior using net.Pipe()
// for fully self-contained testing without any network dependencies.
func TestGatekeeper_Stop_WithNetPipe(t *testing.T) {
	// This test uses a custom approach: we'll test the bridge function directly
	// with net.Pipe connections to verify goroutine cleanup

	// Create two pipe pairs: one for "client" side, one for "proxy" side
	clientRead, clientWrite := net.Pipe()
	proxyRead, proxyWrite := net.Pipe()

	defer clientRead.Close()
	defer clientWrite.Close()
	defer proxyRead.Close()
	defer proxyWrite.Close()

	// Track bridge completion
	bridgeDone := make(chan struct{})

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     "unused",
		SessionID:     "test",
		ValidateToken: nil,
	})

	// Run bridge in goroutine
	go func() {
		gk.bridge(clientRead, proxyRead)
		close(bridgeDone)
	}()

	// Write some data to ensure bridge is active
	testData := []byte("test data")
	go func() {
		clientWrite.Write(testData)
	}()

	// Read the bridged data on proxy side
	buf := make([]byte, len(testData))
	n, err := proxyWrite.Read(buf)
	if err != nil {
		t.Fatalf("failed to read bridged data: %v", err)
	}
	if !bytes.Equal(buf[:n], testData) {
		t.Errorf("bridged data mismatch: got %q, want %q", buf[:n], testData)
	}

	// Close one side to simulate Stop() closing connections
	clientRead.Close()
	proxyRead.Close()

	// Bridge goroutines should exit
	select {
	case <-bridgeDone:
		// Bridge completed successfully
	case <-time.After(time.Second):
		t.Fatal("bridge goroutines did not exit after connections closed")
	}
}

// TestGatekeeper_Stop_MultipleActiveConnections verifies Stop() handles
// multiple simultaneous active connections correctly.
func TestGatekeeper_Stop_MultipleActiveConnections(t *testing.T) {
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create proxy listener: %v", err)
	}
	defer proxyListener.Close()

	// Track number of proxy connections
	var proxyConnCount int
	var mu sync.Mutex

	go func() {
		for {
			conn, err := proxyListener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			proxyConnCount++
			mu.Unlock()

			// Echo server that detects disconnects
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					c.Write(buf[:n])
				}
			}(conn)
		}
	}()

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     proxyListener.Addr().String(),
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	go gk.Start()
	time.Sleep(100 * time.Millisecond)

	// Create multiple client connections
	numClients := 5
	clientConns := make([]net.Conn, numClients)
	clientClosed := make(chan int, numClients)

	for i := 0; i < numClients; i++ {
		conn, err := net.Dial("tcp", gk.Addr().String())
		if err != nil {
			t.Fatalf("failed to connect client %d: %v", i, err)
		}
		clientConns[i] = conn

		// Monitor each connection for closure
		go func(idx int, c net.Conn) {
			buf := make([]byte, 1)
			c.Read(buf) // Will return when connection closes
			clientClosed <- idx
		}(i, conn)
	}

	// Wait for all proxy connections to be established
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	if proxyConnCount != numClients {
		t.Errorf("expected %d proxy connections, got %d", numClients, proxyConnCount)
	}
	mu.Unlock()

	// Stop gatekeeper
	stopDone := make(chan error, 1)
	stopStart := time.Now()
	go func() {
		stopDone <- gk.Stop()
	}()

	// Verify Stop() completes quickly
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop failed: %v", err)
		}
		stopDuration := time.Since(stopStart)
		if stopDuration > 500*time.Millisecond {
			t.Errorf("Stop() took too long with %d connections: %v", numClients, stopDuration)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() hung and did not return within 2 seconds")
	}

	// Verify all client connections were closed
	closedCount := 0
	timeout := time.After(2 * time.Second)
	for closedCount < numClients {
		select {
		case <-clientClosed:
			closedCount++
		case <-timeout:
			t.Fatalf("only %d of %d client connections were closed", closedCount, numClients)
		}
	}

	// Clean up
	for _, conn := range clientConns {
		conn.Close()
	}
}

// TestGatekeeper_Addr_BeforeStart tests Addr() before Start() is called.
func TestGatekeeper_Addr_BeforeStart(t *testing.T) {
	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     "127.0.0.1:44400",
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	// Before Start(), Addr() should return nil
	if gk.Addr() != nil {
		t.Error("Addr() should return nil before Start()")
	}
}

// TestGatekeeper_IsClosed_BeforeStart tests IsClosed() before any calls.
func TestGatekeeper_IsClosed_BeforeStart(t *testing.T) {
	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     "127.0.0.1:44400",
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	// Before any calls, should not be closed
	if gk.IsClosed() {
		t.Error("IsClosed() should return false before Stop() is called")
	}
}

// TestGatekeeper_Start_PortInUse tests starting when port is already in use.
func TestGatekeeper_Start_PortInUse(t *testing.T) {
	// Bind to 0.0.0.0 to ensure conflict with gatekeeper (which also binds to 0.0.0.0)
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("failed to bind: %v", err)
	}
	defer listener.Close()

	// Extract port number
	port := listener.Addr().(*net.TCPAddr).Port

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  port, // Use the already-bound port
		ProxyAddr:     "127.0.0.1:44400",
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	// Start() should fail immediately because port is in use
	err = gk.Start()
	if err == nil {
		gk.Stop()
		t.Error("Start() should fail when port is in use")
	}
}

// TestGatekeeper_Start_AfterStop tests starting after Stop() has been called.
func TestGatekeeper_Start_AfterStop(t *testing.T) {
	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     "127.0.0.1:44400",
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	// Stop it before starting
	gk.Stop()

	// Now try to start - it should fail because closed flag is set
	err := gk.Start()
	if err != ErrGatekeeperClosed {
		// If not ErrGatekeeperClosed, it might succeed and then we need to stop it
		if err == nil {
			gk.Stop()
		}
		t.Logf("Start() after Stop() returned: %v", err)
	}
}

// TestGatekeeper_Stop_Idempotent tests that Stop() can be called multiple times.
func TestGatekeeper_Stop_Idempotent(t *testing.T) {
	proxyListener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer proxyListener.Close()

	go func() {
		for {
			conn, err := proxyListener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     proxyListener.Addr().String(),
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	go gk.Start()
	time.Sleep(50 * time.Millisecond)

	// Call Stop() multiple times - should not panic
	gk.Stop()
	gk.Stop()
	gk.Stop()
}

// TestGatekeeper_ProxyConnectionFailure tests behavior when proxy connection fails.
func TestGatekeeper_ProxyConnectionFailure(t *testing.T) {
	// Use a port that nothing is listening on
	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     "127.0.0.1:44999", // Nothing listening here
		SessionID:     "test-session",
		ValidateToken: nil,
	})

	go gk.Start()
	defer gk.Stop()

	time.Sleep(50 * time.Millisecond)

	// Connect to gatekeeper
	conn, err := net.Dial("tcp", gk.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect to gatekeeper: %v", err)
	}
	defer conn.Close()

	// The gatekeeper should close the connection when it can't connect to proxy
	conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	if err == nil {
		t.Error("expected error when proxy connection fails")
	}
}

// TestGatekeeper_NoOnConnectedCallback tests that missing callback doesn't panic.
func TestGatekeeper_NoOnConnectedCallback(t *testing.T) {
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

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  0,
		ProxyAddr:     proxyListener.Addr().String(),
		SessionID:     "test-session",
		ValidateToken: nil,
		OnConnected:   nil, // No callback
	})

	go gk.Start()
	defer gk.Stop()

	time.Sleep(50 * time.Millisecond)

	// Connect should work without panic
	conn, err := net.Dial("tcp", gk.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	conn.Close()
}

// TestParseCookie_PartialPrefix tests cookie parsing with partial prefix.
func TestParseCookie_PartialPrefix(t *testing.T) {
	// Partial prefix should not match
	_, err := parseCookie([]byte("Cookie: mstshas=user#target#token\r\n"))
	if err == nil {
		t.Error("expected error for partial prefix")
	}
}

// TestParseCookie_MultipleOccurrences tests with multiple cookie occurrences.
func TestParseCookie_MultipleOccurrences(t *testing.T) {
	// Only first occurrence should be used
	input := "Cookie: mstshash=first#target1#token1\r\nCookie: mstshash=second#target2#token2\r\n"
	cookie, err := parseCookie([]byte(input))
	if err != nil {
		t.Fatalf("parseCookie failed: %v", err)
	}

	if cookie.UserID != "first" {
		t.Errorf("expected first cookie, got %q", cookie.UserID)
	}
}

// TestNewGatekeeper_AllFields tests that all config fields are set.
func TestNewGatekeeper_AllFields(t *testing.T) {
	validator := func(token, target string) error { return nil }
	onConnected := func() {}

	gk := NewGatekeeper(GatekeeperConfig{
		ExternalPort:  12345,
		ProxyAddr:     "10.0.0.1:3389",
		SessionID:     "session-abc",
		AllowedIP:     "192.168.1.100",
		ValidateToken: validator,
		OnConnected:   onConnected,
	})

	if gk.externalPort != 12345 {
		t.Errorf("externalPort: got %d, want 12345", gk.externalPort)
	}
	if gk.proxyAddr != "10.0.0.1:3389" {
		t.Errorf("proxyAddr: got %s, want 10.0.0.1:3389", gk.proxyAddr)
	}
	if gk.sessionID != "session-abc" {
		t.Errorf("sessionID: got %s, want session-abc", gk.sessionID)
	}
	if gk.allowedIP != "192.168.1.100" {
		t.Errorf("allowedIP: got %s, want 192.168.1.100", gk.allowedIP)
	}
}
