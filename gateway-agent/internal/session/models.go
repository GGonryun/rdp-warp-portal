package session

import "time"

// Session status constants.
const (
	StatusPending      = "pending"
	StatusReady        = "ready"
	StatusActive       = "active"
	StatusDisconnected = "disconnected"
	StatusCompleted    = "completed"
	StatusFailed       = "failed"
	StatusTerminated   = "terminated"
)

// Session represents an RDP bastion session.
type Session struct {
	ID             string            `json:"id"`
	Status         string            `json:"status"`
	TargetID       string            `json:"target_id"`
	TargetHost     string            `json:"target_host"`
	TargetName     string            `json:"target_name"`
	TargetUser     string            `json:"target_user"`
	RequestedBy    string            `json:"requested_by"`
	GatewayUser    string            `json:"gateway_user"`
	GatewayPass    string            `json:"gateway_pass,omitempty"`
	RDSSessionID   int               `json:"rds_session_id,omitempty"`
	RecordingDir   string            `json:"recording_dir"`
	StartedAt      time.Time         `json:"started_at"`
	ConnectedAt    *time.Time        `json:"connected_at,omitempty"`
	DisconnectedAt *time.Time        `json:"disconnected_at,omitempty"`
	EndedAt        *time.Time        `json:"ended_at,omitempty"`
	ExpiresAt      time.Time         `json:"expires_at"`
	RecordingPath  string            `json:"recording_path,omitempty"`
	AlternateShell string            `json:"-"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	FFmpegPID      int               `json:"-"`
}

// CreateSessionRequest is the body for POST /api/v1/sessions.
type CreateSessionRequest struct {
	TargetID       string            `json:"target_id"`
	RequestedBy    string            `json:"requested_by"`
	TimeoutMinutes int               `json:"timeout_minutes,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// CreateSessionResponse is returned from POST /api/v1/sessions.
type CreateSessionResponse struct {
	SessionID   string `json:"session_id"`
	Status      string `json:"status"`
	TargetID    string `json:"target_id"`
	TargetHost  string `json:"target_host"`
	TargetName  string `json:"target_name"`
	Token       string `json:"token"`
	GatewayHost string `json:"gateway_host"`
	GatewayPort int    `json:"gateway_port"`
	GatewayUser string `json:"gateway_user"`
	GatewayPass string `json:"gateway_pass"`
	ExpiresAt   string `json:"expires_at"`
	ConnectURL  string `json:"connect_url"`
	RDPFileURL  string `json:"rdp_file_url"`
	MonitorURL  string `json:"monitor_url"`
}

// SessionListResponse wraps a list of sessions.
type SessionListResponse struct {
	Sessions []SessionSummary `json:"sessions"`
}

// SessionSummary is a condensed session view for list responses.
type SessionSummary struct {
	SessionID   string `json:"session_id"`
	Status      string `json:"status"`
	TargetID    string `json:"target_id"`
	TargetHost  string `json:"target_host"`
	TargetName  string `json:"target_name"`
	RequestedBy string `json:"requested_by"`
	StartedAt   string `json:"started_at"`
	ExpiresAt   string `json:"expires_at"`
}

// TerminateRequest is the body for POST /api/v1/sessions/{id}/terminate.
type TerminateRequest struct {
	Reason     string `json:"reason"`
	NotifyUser bool   `json:"notify_user"`
}

// StatusCallback is the body from the PowerShell script's internal callback.
type StatusCallback struct {
	Status        string `json:"status"`
	FFmpegPID     int    `json:"ffmpeg_pid,omitempty"`
	RecordingPath string `json:"recording_path,omitempty"`
}

// RDSSession represents a parsed qwinsta entry.
type RDSSession struct {
	SessionName string
	Username    string
	ID          int
	State       string
}

// UserPoolConfig represents the user-pool.json file.
type UserPoolConfig struct {
	Users  []string `json:"users"`
	Prefix string   `json:"prefix"`
	Count  int      `json:"count"`
}

// SessionConfig is the JSON written for the PowerShell launch script.
type SessionConfig struct {
	SessionID    string `json:"session_id"`
	TargetHost   string `json:"target_host"`
	TargetPort   int    `json:"target_port"`
	TargetUser   string `json:"target_user"`
	TargetPass   string `json:"target_pass"`
	TargetDomain string `json:"target_domain"`
	RecordingDir string `json:"recording_dir"`
	FFmpegPath   string `json:"ffmpeg_path"`
	CallbackURL  string `json:"callback_url"`
}
