package capture

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/p0-security/p0rtal-agent/internal/client"
	"github.com/p0-security/p0rtal-agent/internal/config"
	"github.com/p0-security/p0rtal-agent/internal/session"
)

// Recorder orchestrates screen recording and event capture for a single session.
type Recorder struct {
	client      *client.Client
	config      *config.Config
	recordingID string
	screen      *ScreenRecorder
	events      *EventCapture
	windows     *WindowTracker
	eventBuffer []client.RecordingEvent
	mu          sync.Mutex
	cancel      context.CancelFunc
	outputDir   string
}

// NewRecorder creates a new recorder.
func NewRecorder(apiClient *client.Client, cfg *config.Config) *Recorder {
	return &Recorder{
		client: apiClient,
		config: cfg,
	}
}

// Start begins recording for the given session.
func (r *Recorder) Start(ctx context.Context, sessionInfo session.SessionInfo) error {
	hostname, _ := os.Hostname()

	// Create recording on the server.
	rec, err := r.client.CreateRecording(ctx, client.CreateRecordingRequest{
		SessionID:     fmt.Sprintf("%d", sessionInfo.ID),
		TargetName:    hostname,
		WindowsUser:   sessionInfo.User,
		ProxyUser:     sessionInfo.User,
		AgentHostname: hostname,
	})
	if err != nil {
		return fmt.Errorf("create recording: %w", err)
	}

	r.recordingID = rec.ID
	slog.Info("recording created", "recording_id", r.recordingID, "session_id", sessionInfo.ID)

	// Create temp directory for video chunks.
	r.outputDir, err = os.MkdirTemp("", fmt.Sprintf("p0rtal-recording-%s-", r.recordingID))
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}

	ctx, r.cancel = context.WithCancel(ctx)

	// Start screen recorder.
	r.screen = NewScreenRecorder(
		r.config.FfmpegPath,
		r.config.Framerate,
		r.config.ChunkSecs,
		r.outputDir,
		r.handleChunk,
	)
	if err := r.screen.Start(ctx); err != nil {
		return fmt.Errorf("start screen recorder: %w", err)
	}

	// Start process event capture.
	r.events = NewEventCapture(r.handleProcessEvent)
	if err := r.events.Start(ctx); err != nil {
		slog.Warn("failed to start process event capture", "error", err)
		// Non-fatal: continue without process events.
	}

	// Start window tracker.
	r.windows = NewWindowTracker(500*time.Millisecond, r.handleWindowEvent)
	go r.windows.Run(ctx)

	// Start event flush goroutine.
	go r.flushLoop(ctx)

	slog.Info("recording started", "recording_id", r.recordingID, "output_dir", r.outputDir)
	return nil
}

// Stop terminates all capture processes and uploads remaining data.
func (r *Recorder) Stop() error {
	slog.Info("stopping recording", "recording_id", r.recordingID)

	// Stop screen recorder.
	if r.screen != nil {
		if err := r.screen.Stop(); err != nil {
			slog.Warn("error stopping screen recorder", "error", err)
		}
	}

	// Stop event capture.
	if r.events != nil {
		if err := r.events.Stop(); err != nil {
			slog.Warn("error stopping event capture", "error", err)
		}
	}

	// Cancel context (stops window tracker and flush loop).
	if r.cancel != nil {
		r.cancel()
	}

	// Flush remaining events.
	r.flushEvents()

	// End recording on the server.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := r.client.EndRecording(ctx, r.recordingID); err != nil {
		slog.Error("failed to end recording", "error", err)
	}

	// Clean up temp directory.
	if r.outputDir != "" {
		if err := os.RemoveAll(r.outputDir); err != nil {
			slog.Warn("failed to remove temp dir", "error", err, "dir", r.outputDir)
		}
	}

	slog.Info("recording stopped", "recording_id", r.recordingID)
	return nil
}

// handleChunk uploads a video chunk to the server.
func (r *Recorder) handleChunk(chunkPath string) {
	slog.Info("uploading chunk", "path", chunkPath, "recording_id", r.recordingID)

	f, err := os.Open(chunkPath)
	if err != nil {
		slog.Error("failed to open chunk", "path", chunkPath, "error", err)
		return
	}
	defer f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := r.client.UploadChunk(ctx, r.recordingID, f); err != nil {
		slog.Error("failed to upload chunk", "path", chunkPath, "error", err)
		return
	}

	slog.Info("chunk uploaded", "path", chunkPath)
}

// handleProcessEvent converts a ProcessEvent to a RecordingEvent and buffers it.
func (r *Recorder) handleProcessEvent(pe ProcessEvent) {
	evt := client.RecordingEvent{
		Timestamp: pe.Timestamp,
		Type:      pe.Type,
		Data: map[string]any{
			"pid":          pe.PID,
			"name":         pe.Name,
			"command_line": pe.CommandLine,
			"user":         pe.User,
			"parent_pid":   pe.ParentPID,
		},
	}

	r.mu.Lock()
	r.eventBuffer = append(r.eventBuffer, evt)
	r.mu.Unlock()
}

// handleWindowEvent converts a WindowEvent to a RecordingEvent and buffers it.
func (r *Recorder) handleWindowEvent(we WindowEvent) {
	evt := client.RecordingEvent{
		Timestamp: we.Timestamp,
		Type:      "window_focus",
		Data: map[string]any{
			"title":   we.Title,
			"process": we.Process,
			"pid":     we.PID,
		},
	}

	r.mu.Lock()
	r.eventBuffer = append(r.eventBuffer, evt)
	r.mu.Unlock()
}

// flushLoop periodically flushes buffered events to the server.
func (r *Recorder) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.flushEvents()
		}
	}
}

// flushEvents sends all buffered events to the server.
func (r *Recorder) flushEvents() {
	r.mu.Lock()
	if len(r.eventBuffer) == 0 {
		r.mu.Unlock()
		return
	}
	events := r.eventBuffer
	r.eventBuffer = nil
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := r.client.SendEvents(ctx, r.recordingID, events); err != nil {
		slog.Error("failed to send events", "error", err, "count", len(events))
		// Put events back in the buffer for retry.
		r.mu.Lock()
		r.eventBuffer = append(events, r.eventBuffer...)
		r.mu.Unlock()
	} else {
		slog.Debug("events flushed", "count", len(events))
	}
}
