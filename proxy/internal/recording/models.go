// Package recording provides session recording infrastructure for the RDP broker.
package recording

import "time"

// RecordingStatus represents the current state of a recording.
type RecordingStatus string

const (
	StatusRecording RecordingStatus = "recording"
	StatusCompleted RecordingStatus = "completed"
	StatusFailed    RecordingStatus = "failed"
)

// Recording holds metadata for a session recording.
type Recording struct {
	ID            string          `json:"id"`
	SessionID     string          `json:"session_id"`
	TargetID      string          `json:"target_id"`
	TargetName    string          `json:"target_name"`
	WindowsUser   string          `json:"windows_user"`
	ProxyUser     string          `json:"proxy_user"`
	AgentHostname string          `json:"agent_hostname"`
	Status        RecordingStatus `json:"status"`
	StartedAt     time.Time       `json:"started_at"`
	EndedAt       *time.Time      `json:"ended_at,omitempty"`
	DurationSecs  float64         `json:"duration_secs"`
	ChunkCount    int             `json:"chunk_count"`
	TotalBytes    int64           `json:"total_bytes"`
	EventCount    int             `json:"event_count"`
}

// RecordingEvent represents a discrete event that occurred during a recording.
type RecordingEvent struct {
	Timestamp time.Time      `json:"ts"`
	Type      string         `json:"type"` // process_start, process_end, window_focus, session_start, session_end
	Data      map[string]any `json:"data"`
}
