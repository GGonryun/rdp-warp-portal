package capture

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	input       *InputTracker
	clipboard   *ClipboardTracker
	winlog      *WinLogCapture
	eventBuffer []client.RecordingEvent
	mu          sync.Mutex
	gone        bool // true when broker returned 404 for this recording
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

	// Create output directory for video chunks under C:\p0rtal\recordings.
	// This must be accessible by any user session (ffmpeg runs as the logged-in user).
	recordingsBase := filepath.Join(filepath.Dir(r.config.FfmpegPath), "recordings")
	if err := os.MkdirAll(recordingsBase, 0o777); err != nil {
		return fmt.Errorf("create recordings base dir: %w", err)
	}
	r.outputDir = filepath.Join(recordingsBase, r.recordingID)
	if err := os.MkdirAll(r.outputDir, 0o777); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	// Grant all users write access (Windows ignores Unix mode bits).
	exec.Command("icacls", r.outputDir, "/grant", "Everyone:(OI)(CI)F", "/T").Run()
	exec.Command("icacls", recordingsBase, "/grant", "Everyone:(OI)(CI)F", "/T").Run()

	ctx, r.cancel = context.WithCancel(ctx)

	// Start screen recorder.
	r.screen = NewScreenRecorder(
		r.config.FfmpegPath,
		r.config.Framerate,
		r.config.ChunkSecs,
		r.outputDir,
		sessionInfo.ID,
		r.config.ResizePollMs,
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

	// Start input tracker (keyboard + mouse hooks).
	r.input = NewInputTracker(r.handleInputEvent)
	go r.input.Run(ctx)

	// Start clipboard tracker.
	r.clipboard = NewClipboardTracker(1*time.Second, r.handleClipboardEvent)
	go r.clipboard.Run(ctx)

	// Start Windows Event Log capture.
	r.winlog = NewWinLogCapture(r.handleWinLogEvent)
	if err := r.winlog.Start(ctx); err != nil {
		slog.Warn("failed to start windows event log capture", "error", err)
	}

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

	// Stop Windows Event Log capture.
	if r.winlog != nil {
		if err := r.winlog.Stop(); err != nil {
			slog.Warn("error stopping winlog capture", "error", err)
		}
	}


	// Cancel context (stops window tracker and flush loop).
	if r.cancel != nil {
		r.cancel()
	}

	// Flush remaining events.
	r.flushEvents()

	// End recording on the server (skip if already gone).
	r.mu.Lock()
	gone := r.gone
	r.mu.Unlock()

	if !gone {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := r.client.EndRecording(ctx, r.recordingID); err != nil {
			if errors.Is(err, client.ErrRecordingGone) {
				slog.Warn("recording already gone, skipping end", "recording_id", r.recordingID)
			} else {
				slog.Error("failed to end recording", "error", err)
			}
		}
	}

	// Clean up temp directory. Retry briefly in case ffmpeg still holds a file handle.
	if r.outputDir != "" {
		var removeErr error
		for i := 0; i < 5; i++ {
			removeErr = os.RemoveAll(r.outputDir)
			if removeErr == nil {
				break
			}
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
		}
		if removeErr != nil {
			slog.Warn("failed to remove temp dir", "error", removeErr, "dir", r.outputDir)
		}
	}

	slog.Info("recording stopped", "recording_id", r.recordingID)
	return nil
}

// handleChunk uploads a video chunk to the server in a background goroutine.
func (r *Recorder) handleChunk(chunkPath string) {
	go func() {
		r.mu.Lock()
		if r.gone {
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()

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
			if errors.Is(err, client.ErrRecordingGone) {
				slog.Warn("recording gone, stopping chunk uploads", "recording_id", r.recordingID)
				r.markGone()
				return
			}
			slog.Error("failed to upload chunk", "path", chunkPath, "error", err)
			return
		}

		slog.Info("chunk uploaded", "path", chunkPath)
	}()
}

// markGone sets the gone flag, indicating the broker no longer has this recording.
func (r *Recorder) markGone() {
	r.mu.Lock()
	r.gone = true
	r.eventBuffer = nil
	r.mu.Unlock()
}

// isAgentProcess returns true if the process is part of the agent itself and should be filtered.
func isAgentProcess(name, commandLine string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "ffmpeg.exe", "ffmpeg", "powershell.exe", "agent.exe", "p0rtal-agent.exe":
		return true
	}
	// Also filter by command line containing our paths.
	cmdLower := strings.ToLower(commandLine)
	if strings.Contains(cmdLower, "p0rtal") && (strings.Contains(cmdLower, "ffmpeg") || strings.Contains(cmdLower, "agent")) {
		return true
	}
	// Filter the WMI subscription powershell scripts.
	if lower == "powershell.exe" || strings.Contains(cmdLower, "register-wmievent") || strings.Contains(cmdLower, "get-winevent") {
		return true
	}
	return false
}

// handleProcessEvent converts a ProcessEvent to a RecordingEvent and buffers it.
func (r *Recorder) handleProcessEvent(pe ProcessEvent) {
	if isAgentProcess(pe.Name, pe.CommandLine) {
		return
	}

	data := map[string]any{
		"pid":          pe.PID,
		"name":         pe.Name,
		"command_line": pe.CommandLine,
		"user":         pe.User,
		"parent_pid":   pe.ParentPID,
	}
	if pe.Type == "process_end" {
		data["exit_code"] = pe.ExitCode
	}

	evt := client.RecordingEvent{
		Timestamp: pe.Timestamp,
		Type:      pe.Type,
		Data:      data,
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

// handleInputEvent converts an InputEvent to a RecordingEvent and buffers it.
func (r *Recorder) handleInputEvent(ie InputEvent) {
	evt := client.RecordingEvent{
		Timestamp: ie.Timestamp,
		Type:      ie.Type,
		Data: map[string]any{
			"button": ie.Button,
			"x":      ie.X,
			"y":      ie.Y,
			"window": ie.Window,
			"pid":    ie.PID,
		},
	}

	r.mu.Lock()
	r.eventBuffer = append(r.eventBuffer, evt)
	r.mu.Unlock()
}

// handleClipboardEvent converts a ClipboardEvent to a RecordingEvent and buffers it.
func (r *Recorder) handleClipboardEvent(ce ClipboardEvent) {
	evt := client.RecordingEvent{
		Timestamp: ce.Timestamp,
		Type:      "clipboard",
		Data: map[string]any{
			"content": ce.Content,
			"window":  ce.Window,
			"pid":     ce.PID,
		},
	}

	r.mu.Lock()
	r.eventBuffer = append(r.eventBuffer, evt)
	r.mu.Unlock()
}

// isAgentWinLogEvent returns true if the Windows Event Log entry is about the agent itself
// or noisy system processes with low security value.
func isAgentWinLogEvent(message string) bool {
	lower := strings.ToLower(message)

	// Agent infrastructure
	if strings.Contains(lower, "agent.exe") || strings.Contains(lower, "p0rtal-agent") || strings.Contains(lower, "p0rtal") || strings.Contains(lower, "rdp-warp-portal") {
		return true
	}
	if strings.Contains(lower, "ffmpeg.exe") || strings.Contains(lower, "ffmpeg") {
		return true
	}
	if strings.Contains(lower, "icacls") {
		return true
	}
	if strings.Contains(lower, "c:\\p0rtal") || strings.Contains(lower, `c:\p0rtal`) {
		return true
	}

	// System processes (noisy, low security value)
	noisyProcesses := []string{
		"svchost.exe",
		"wmiprvse.exe",
		"searchindexer.exe",
		"runtimebroker.exe",
		"backgroundtaskhost.exe",
		"compattelrunner.exe",
		"diagtrack",
		"searchprotocolhost.exe",
		"searchfilterhost.exe",
		"taskhostw.exe",
	}
	for _, proc := range noisyProcesses {
		if strings.Contains(lower, proc) {
			return true
		}
	}

	return false
}

// isAgentScriptBlock returns true if the PowerShell script block is from the agent's
// winlog polling infrastructure.
func isAgentScriptBlock(scriptBlock string) bool {
	lower := strings.ToLower(scriptBlock)
	// Agent's winlog polling script patterns
	if strings.Contains(lower, "get-winevent -filterhashtable") {
		return true
	}
	if strings.Contains(lower, "register-wmievent") {
		return true
	}
	if strings.Contains(lower, "$eventids = @(4624") {
		return true
	}
	if strings.Contains(lower, "$lastcheck = (get-date)") {
		return true
	}
	return false
}

// isSystemAccountLogon returns true if the event is a logon/logoff event for a system account.
func isSystemAccountLogon(eventID int, user string) bool {
	// Only filter for logon-related events
	if eventID != 4624 && eventID != 4634 && eventID != 4647 {
		return false
	}
	lower := strings.ToLower(user)
	systemAccounts := []string{
		"system", "nt authority\\system",
		"local service", "nt authority\\local service",
		"network service", "nt authority\\network service",
		"dwm-", "umfd-",
	}
	for _, acct := range systemAccounts {
		if strings.Contains(lower, acct) {
			return true
		}
	}
	return false
}

// handleWinLogEvent converts a WinLogEvent to a RecordingEvent and buffers it.
func (r *Recorder) handleWinLogEvent(we WinLogEvent) {
	if isAgentWinLogEvent(we.Message) {
		return
	}
	if isSystemAccountLogon(we.EventID, we.User) {
		return
	}
	if we.ScriptBlock != "" && isAgentScriptBlock(we.ScriptBlock) {
		return
	}
	data := map[string]any{
		"event_id": we.EventID,
		"log":      we.Log,
		"source":   we.Source,
		"message":  we.Message,
		"level":    we.Level,
	}
	if we.User != "" {
		data["user"] = we.User
	}
	if we.ScriptBlock != "" {
		data["script_block"] = we.ScriptBlock
	}

	evt := client.RecordingEvent{
		Timestamp: we.Timestamp,
		Type:      "winlog",
		Data:      data,
	}

	r.mu.Lock()
	r.eventBuffer = append(r.eventBuffer, evt)
	r.mu.Unlock()
}

// flushLoop periodically flushes buffered events to the server.
func (r *Recorder) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
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
	if r.gone || len(r.eventBuffer) == 0 {
		r.mu.Unlock()
		return
	}
	events := r.eventBuffer
	r.eventBuffer = nil
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := r.client.SendEvents(ctx, r.recordingID, events); err != nil {
		if errors.Is(err, client.ErrRecordingGone) {
			slog.Warn("recording gone, discarding events", "recording_id", r.recordingID, "count", len(events))
			r.markGone()
			return
		}
		slog.Error("failed to send events", "error", err, "count", len(events))
		// Put events back in the buffer for retry.
		r.mu.Lock()
		r.eventBuffer = append(events, r.eventBuffer...)
		r.mu.Unlock()
	} else {
		slog.Debug("events flushed", "count", len(events))
	}
}
