//go:build windows

package session

import (
	"context"
	"fmt"
	"log/slog"
	"syscall"
	"time"
	"unsafe"
)

const (
	WTSActive       = 0
	WTSConnected    = 1
	WTSConnectQuery = 2
	WTSShadow       = 3
	WTSDisconnected = 4
	WTSIdle         = 5
	WTSListen       = 6
	WTSReset        = 7
	WTSDown         = 8
	WTSInit         = 9

	WTSUserName         = 5
	WTSClientAddress    = 14
	WTSSessionAddressV4 = 29
)

var (
	wtsapi32                     = syscall.NewLazyDLL("wtsapi32.dll")
	procWTSEnumerateSessions     = wtsapi32.NewProc("WTSEnumerateSessionsW")
	procWTSQuerySessionInformation = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSFreeMemory            = wtsapi32.NewProc("WTSFreeMemory")
)

// WTS_SESSION_INFO represents the Windows WTS_SESSION_INFOW structure.
type wtsSessionInfo struct {
	SessionID      uint32
	WinStationName *uint16
	State          uint32
}

// SessionInfo holds information about an RDP session.
type SessionInfo struct {
	ID       uint32
	User     string
	State    uint32
	ClientIP string
}

// Detector polls WTS to detect RDP session logon and logoff events.
type Detector struct {
	pollInterval time.Duration
	onLogon      func(SessionInfo)
	onLogoff     func(SessionInfo)
}

// NewDetector creates a new session detector.
func NewDetector(pollInterval time.Duration, onLogon, onLogoff func(SessionInfo)) *Detector {
	return &Detector{
		pollInterval: pollInterval,
		onLogon:      onLogon,
		onLogoff:     onLogoff,
	}
}

// Run polls WTS for session changes and calls the appropriate callbacks.
// It blocks until the context is cancelled.
func (d *Detector) Run(ctx context.Context) {
	slog.Info("starting WTS session detector", "poll_interval", d.pollInterval)

	activeSessions := make(map[uint32]SessionInfo)

	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("session detector stopped")
			return
		case <-ticker.C:
			current, err := enumerateSessions()
			if err != nil {
				slog.Error("failed to enumerate sessions", "error", err)
				continue
			}

			currentMap := make(map[uint32]SessionInfo)
			for _, s := range current {
				if s.State == WTSActive && s.User != "" {
					currentMap[s.ID] = s
				}
			}

			// Detect new sessions (logon).
			for id, info := range currentMap {
				if _, exists := activeSessions[id]; !exists {
					slog.Info("session logon detected", "session_id", id, "user", info.User, "client_ip", info.ClientIP)
					if d.onLogon != nil {
						d.onLogon(info)
					}
				}
			}

			// Detect removed sessions (logoff).
			for id, info := range activeSessions {
				if _, exists := currentMap[id]; !exists {
					slog.Info("session logoff detected", "session_id", id, "user", info.User)
					if d.onLogoff != nil {
						d.onLogoff(info)
					}
				}
			}

			activeSessions = currentMap
		}
	}
}

func enumerateSessions() ([]SessionInfo, error) {
	var (
		sessionInfoPtr unsafe.Pointer
		count          uint32
	)

	ret, _, err := procWTSEnumerateSessions.Call(
		0, // WTS_CURRENT_SERVER_HANDLE
		0, // reserved
		1, // version
		uintptr(unsafe.Pointer(&sessionInfoPtr)),
		uintptr(unsafe.Pointer(&count)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("WTSEnumerateSessions failed: %w", err)
	}
	defer procWTSFreeMemory.Call(uintptr(sessionInfoPtr))

	sessions := make([]SessionInfo, 0, count)
	size := unsafe.Sizeof(wtsSessionInfo{})

	for i := uint32(0); i < count; i++ {
		entry := (*wtsSessionInfo)(unsafe.Add(sessionInfoPtr, int(uintptr(i)*size)))

		info := SessionInfo{
			ID:    entry.SessionID,
			State: entry.State,
		}

		// Query username.
		if user, err := querySessionString(entry.SessionID, WTSUserName); err == nil {
			info.User = user
		}

		// Query client IP address.
		if ip, err := querySessionClientIP(entry.SessionID); err == nil {
			info.ClientIP = ip
		}

		sessions = append(sessions, info)
	}

	return sessions, nil
}

func querySessionString(sessionID uint32, infoClass uint32) (string, error) {
	var (
		buffer unsafe.Pointer
		bytes  uint32
	)

	ret, _, err := procWTSQuerySessionInformation.Call(
		0, // WTS_CURRENT_SERVER_HANDLE
		uintptr(sessionID),
		uintptr(infoClass),
		uintptr(unsafe.Pointer(&buffer)),
		uintptr(unsafe.Pointer(&bytes)),
	)
	if ret == 0 {
		return "", fmt.Errorf("WTSQuerySessionInformation failed: %w", err)
	}
	defer procWTSFreeMemory.Call(uintptr(buffer))

	return syscall.UTF16ToString((*[4096]uint16)(buffer)[:]), nil
}

func querySessionClientIP(sessionID uint32) (string, error) {
	var (
		buffer unsafe.Pointer
		bytes  uint32
	)

	ret, _, err := procWTSQuerySessionInformation.Call(
		0,
		uintptr(sessionID),
		uintptr(WTSClientAddress),
		uintptr(unsafe.Pointer(&buffer)),
		uintptr(unsafe.Pointer(&bytes)),
	)
	if ret == 0 {
		return "", fmt.Errorf("WTSQuerySessionInformation for client address failed: %w", err)
	}
	defer procWTSFreeMemory.Call(uintptr(buffer))

	// WTS_CLIENT_ADDRESS structure: first 4 bytes are address family, then address bytes.
	// For AF_INET (2), the IPv4 address is at offset 4, 4 bytes.
	addrFamily := *(*uint32)(buffer)
	if addrFamily == 2 { // AF_INET
		addrBytes := (*[4]byte)(unsafe.Add(buffer, 4))
		return fmt.Sprintf("%d.%d.%d.%d", addrBytes[0], addrBytes[1], addrBytes[2], addrBytes[3]), nil
	}

	return "", nil
}
