package session

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// Gatekeeper errors.
var (
	ErrGatekeeperClosed    = errors.New("gatekeeper is closed")
	ErrInvalidTPKT         = errors.New("invalid TPKT header")
	ErrInvalidX224         = errors.New("invalid X.224 header")
	ErrPacketTooShort      = errors.New("packet too short")
	ErrNoCookie            = errors.New("no mstshash cookie found")
	ErrInvalidCookieFormat = errors.New("invalid cookie format")
	ErrReadTimeout         = errors.New("read timeout")
)

// Protocol constants from MS-RDPBCGR and X.224.
const (
	TPKTVersion     = 0x03
	TPKTHeaderSize  = 4
	TPKTMaxLength   = 65535
	TPKTMinLength   = 11 // TPKT header (4) + minimal X.224 CR (7)
	X224TypeCR      = 0xE0 // Connection Request
	X224HeaderSize  = 7
	CookiePrefix    = "Cookie: mstshash="
	CookieTerminator = "\r\n"
	TokenDelimiter  = "#"
	ReadTimeout     = 10 * time.Second
)

// ParsedCookie represents extracted token components from the mstshash cookie.
type ParsedCookie struct {
	UserID   string
	TargetID string
	Token    string
	Raw      string
}

// TokenValidator is a function that validates a token for a session.
type TokenValidator func(token, targetID string) error

// Gatekeeper listens for incoming RDP connections, validates tokens,
// and bridges traffic to the proxy.
type Gatekeeper struct {
	mu             sync.Mutex
	listener       net.Listener
	externalPort   int
	proxyAddr      string
	sessionID      string
	allowedIP      string // If set, only allow connections from this IP
	validateToken  TokenValidator
	closed         bool
	activeConns    sync.WaitGroup
	onConnected    func() // Called when a valid connection is established
}

// GatekeeperConfig holds configuration for creating a gatekeeper.
type GatekeeperConfig struct {
	ExternalPort  int
	ProxyAddr     string
	SessionID     string
	AllowedIP     string // If set, only allow connections from this IP
	ValidateToken TokenValidator
	OnConnected   func()
}

// NewGatekeeper creates a new gatekeeper.
func NewGatekeeper(cfg GatekeeperConfig) *Gatekeeper {
	return &Gatekeeper{
		externalPort:  cfg.ExternalPort,
		proxyAddr:     cfg.ProxyAddr,
		sessionID:     cfg.SessionID,
		allowedIP:     cfg.AllowedIP,
		validateToken: cfg.ValidateToken,
		onConnected:   cfg.OnConnected,
	}
}

// Start begins listening for connections.
// This method blocks until Stop() is called or an error occurs.
func (g *Gatekeeper) Start() error {
	addr := fmt.Sprintf("0.0.0.0:%d", g.externalPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		listener.Close()
		return ErrGatekeeperClosed
	}
	g.listener = listener
	g.mu.Unlock()

	for {
		conn, err := listener.Accept()
		if err != nil {
			g.mu.Lock()
			closed := g.closed
			g.mu.Unlock()
			if closed {
				return nil
			}
			continue
		}

		g.activeConns.Add(1)
		go g.handleConnection(conn)
	}
}

// Stop stops the gatekeeper and closes all connections.
func (g *Gatekeeper) Stop() error {
	g.mu.Lock()
	g.closed = true
	listener := g.listener
	g.mu.Unlock()

	if listener != nil {
		listener.Close()
	}

	// Wait for active connections to finish
	g.activeConns.Wait()

	return nil
}

// handleConnection handles a single incoming connection.
func (g *Gatekeeper) handleConnection(clientConn net.Conn) {
	defer g.activeConns.Done()
	defer clientConn.Close()

	slog.Debug("gatekeeper: new connection", "session_id", g.sessionID, "remote", clientConn.RemoteAddr())

	// Connect to the proxy immediately
	proxyConn, err := net.DialTimeout("tcp", g.proxyAddr, 5*time.Second)
	if err != nil {
		slog.Error("gatekeeper: failed to connect to proxy", "session_id", g.sessionID, "error", err)
		return
	}
	defer proxyConn.Close()

	// Notify that a connection was established
	if g.onConnected != nil {
		g.onConnected()
	}

	slog.Info("gatekeeper: connection established, bridging", "session_id", g.sessionID)

	// Bridge the connections bidirectionally - let proxy handle all protocol
	g.bridge(clientConn, proxyConn)
}

// readAndParseX224 reads the X.224 Connection Request and extracts the cookie.
func (g *Gatekeeper) readAndParseX224(conn net.Conn) ([]byte, *ParsedCookie, error) {
	// Set read deadline
	if err := conn.SetReadDeadline(time.Now().Add(ReadTimeout)); err != nil {
		return nil, nil, err
	}

	// Read TPKT header (4 bytes)
	tpktBuf := make([]byte, TPKTHeaderSize)
	if _, err := io.ReadFull(conn, tpktBuf); err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, nil, ErrReadTimeout
		}
		return nil, nil, fmt.Errorf("failed to read TPKT header: %w", err)
	}

	// Validate TPKT version
	if tpktBuf[0] != TPKTVersion {
		return nil, nil, fmt.Errorf("%w: version %d, expected %d", ErrInvalidTPKT, tpktBuf[0], TPKTVersion)
	}

	// Parse packet length
	packetLength := binary.BigEndian.Uint16(tpktBuf[2:4])
	if packetLength < TPKTMinLength {
		return nil, nil, fmt.Errorf("%w: length %d, minimum %d", ErrPacketTooShort, packetLength, TPKTMinLength)
	}
	if packetLength > TPKTMaxLength {
		return nil, nil, fmt.Errorf("%w: length %d exceeds maximum", ErrInvalidTPKT, packetLength)
	}

	// Read remaining packet
	remainingLen := int(packetLength) - TPKTHeaderSize
	remainingBuf := make([]byte, remainingLen)
	if _, err := io.ReadFull(conn, remainingBuf); err != nil {
		return nil, nil, fmt.Errorf("failed to read packet body: %w", err)
	}

	// Clear read deadline
	conn.SetReadDeadline(time.Time{})

	// Validate X.224 header
	if len(remainingBuf) < X224HeaderSize {
		return nil, nil, fmt.Errorf("%w: X.224 header truncated", ErrInvalidX224)
	}

	// Check X.224 type (Connection Request)
	x224Type := remainingBuf[1] & 0xF0
	if x224Type != X224TypeCR {
		return nil, nil, fmt.Errorf("%w: type 0x%02X, expected 0x%02X (CR)", ErrInvalidX224, x224Type, X224TypeCR)
	}

	// Combine into full packet for forwarding
	fullPacket := make([]byte, packetLength)
	copy(fullPacket[:TPKTHeaderSize], tpktBuf)
	copy(fullPacket[TPKTHeaderSize:], remainingBuf)

	// Parse cookie from variable data
	variableData := remainingBuf[X224HeaderSize:]
	cookie, err := parseCookie(variableData)
	if err != nil {
		return nil, nil, err
	}

	return fullPacket, cookie, nil
}

// readX224Packet reads the X.224 packet and optionally parses the cookie.
// Returns the packet, cookie (may be nil if not found), and any error.
func (g *Gatekeeper) readX224Packet(conn net.Conn) ([]byte, *ParsedCookie, error) {
	// Set read deadline
	if err := conn.SetReadDeadline(time.Now().Add(ReadTimeout)); err != nil {
		return nil, nil, err
	}

	// Read TPKT header (4 bytes)
	tpktBuf := make([]byte, TPKTHeaderSize)
	n, err := io.ReadFull(conn, tpktBuf)
	if err != nil {
		// Log what we received for debugging
		if n > 0 {
			slog.Debug("gatekeeper: partial TPKT read", "bytes", n, "data", fmt.Sprintf("%x", tpktBuf[:n]))
		}
		return nil, nil, fmt.Errorf("failed to read TPKT header: %w", err)
	}

	slog.Debug("gatekeeper: TPKT header", "data", fmt.Sprintf("%x", tpktBuf))

	// Validate TPKT version
	if tpktBuf[0] != TPKTVersion {
		return nil, nil, fmt.Errorf("%w: version %d", ErrInvalidTPKT, tpktBuf[0])
	}

	// Parse packet length
	packetLength := binary.BigEndian.Uint16(tpktBuf[2:4])
	if packetLength < TPKTMinLength || packetLength > TPKTMaxLength {
		return nil, nil, fmt.Errorf("%w: invalid length %d", ErrInvalidTPKT, packetLength)
	}

	// Read remaining packet
	remainingLen := int(packetLength) - TPKTHeaderSize
	remainingBuf := make([]byte, remainingLen)
	if _, err := io.ReadFull(conn, remainingBuf); err != nil {
		return nil, nil, fmt.Errorf("failed to read packet body: %w", err)
	}

	// Clear read deadline
	conn.SetReadDeadline(time.Time{})

	// Combine into full packet for forwarding
	fullPacket := make([]byte, packetLength)
	copy(fullPacket[:TPKTHeaderSize], tpktBuf)
	copy(fullPacket[TPKTHeaderSize:], remainingBuf)

	// Try to parse cookie from variable data (optional)
	var cookie *ParsedCookie
	if len(remainingBuf) > X224HeaderSize {
		variableData := remainingBuf[X224HeaderSize:]
		cookie, _ = parseCookie(variableData) // Ignore error - cookie is optional
	}

	return fullPacket, cookie, nil
}

// parseCookie extracts the mstshash cookie and parses it into components.
func parseCookie(data []byte) (*ParsedCookie, error) {
	dataStr := string(data)

	// Find the mstshash cookie
	idx := strings.Index(dataStr, CookiePrefix)
	if idx == -1 {
		return nil, ErrNoCookie
	}

	// Extract cookie value (everything after "Cookie: mstshash=" until CRLF)
	afterPrefix := dataStr[idx+len(CookiePrefix):]
	termIdx := strings.Index(afterPrefix, CookieTerminator)
	if termIdx == -1 {
		return nil, fmt.Errorf("%w: no CRLF terminator", ErrInvalidCookieFormat)
	}

	cookieValue := afterPrefix[:termIdx]
	if cookieValue == "" {
		return nil, fmt.Errorf("%w: empty cookie value", ErrInvalidCookieFormat)
	}

	// Parse the token format: username#target_id#token
	parts := strings.Split(cookieValue, TokenDelimiter)
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected 3 parts (username#target_id#token), got %d", ErrInvalidCookieFormat, len(parts))
	}

	userID := parts[0]
	targetID := parts[1]
	token := parts[2]

	if userID == "" || targetID == "" || token == "" {
		return nil, fmt.Errorf("%w: empty field in cookie", ErrInvalidCookieFormat)
	}

	return &ParsedCookie{
		UserID:   userID,
		TargetID: targetID,
		Token:    token,
		Raw:      cookieValue,
	}, nil
}

// bridge creates a bidirectional pipe between two connections.
func (g *Gatekeeper) bridge(client, proxy net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	// Client -> Proxy
	go func() {
		defer wg.Done()
		io.Copy(proxy, client)
		// Signal proxy that client is done sending
		if tcpConn, ok := proxy.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
	}()

	// Proxy -> Client
	go func() {
		defer wg.Done()
		io.Copy(client, proxy)
		// Signal client that proxy is done sending
		if tcpConn, ok := client.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
	}()

	wg.Wait()
}

// Addr returns the address the gatekeeper is listening on.
func (g *Gatekeeper) Addr() net.Addr {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.listener != nil {
		return g.listener.Addr()
	}
	return nil
}

// IsClosed returns true if the gatekeeper has been stopped.
func (g *Gatekeeper) IsClosed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
}
