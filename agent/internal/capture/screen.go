package capture

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ScreenRecorder captures the screen using ffmpeg and produces video chunks.
type ScreenRecorder struct {
	ffmpegPath   string
	framerate    int
	chunkSecs    int
	outputDir    string
	sessionID    uint32 // Windows session ID (0 = current session)
	resizePollMs int    // resolution poll interval in milliseconds
	onChunk      func(chunkPath string)

	mu           sync.Mutex
	cmd          *exec.Cmd
	processPID   int      // PID when launched via CreateProcessAsUser
	processStdin *os.File // stdin pipe for cross-session ffmpeg
	stdin        io.WriteCloser
	cancel       context.CancelFunc
	nextChunk    int // next segment start number across restarts
}

// NewScreenRecorder creates a new screen recorder.
func NewScreenRecorder(ffmpegPath string, framerate, chunkSecs int, outputDir string, sessionID uint32, resizePollMs int, onChunk func(string)) *ScreenRecorder {
	if resizePollMs <= 0 {
		resizePollMs = 1000
	}
	return &ScreenRecorder{
		ffmpegPath:   ffmpegPath,
		framerate:    framerate,
		chunkSecs:    chunkSecs,
		outputDir:    outputDir,
		sessionID:    sessionID,
		resizePollMs: resizePollMs,
		onChunk:      onChunk,
	}
}

// fixedVF is the video filter that scales to 854x480 with black bar padding.
// This keeps the output resolution constant regardless of input resolution,
// which is required for seamless HLS segment stitching.
const fixedVF = "scale=854:480:force_original_aspect_ratio=decrease," +
	"pad=854:480:(ow-iw)/2:(oh-ih)/2:black,setsar=1"

// Start launches the ffmpeg process and begins watching for chunks.
func (s *ScreenRecorder) Start(ctx context.Context) error {
	if err := os.MkdirAll(s.outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	ctx, s.cancel = context.WithCancel(ctx)

	if err := s.launchFFmpeg(ctx); err != nil {
		return err
	}

	// Start chunk watcher goroutine.
	go s.watchChunks(ctx)

	// Start resolution monitor goroutine.
	go s.monitorResolution(ctx)

	return nil
}

// ffmpegArgs builds the ffmpeg command-line arguments.
func (s *ScreenRecorder) ffmpegArgs() []string {
	outputPattern := filepath.Join(s.outputDir, "chunk_%03d.ts")
	return []string{
		"-f", "gdigrab",
		"-framerate", fmt.Sprintf("%d", s.framerate),
		"-i", "desktop",
		"-vf", fixedVF,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-crf", "28",
		"-pix_fmt", "yuv420p",
		"-f", "segment",
		"-segment_time", fmt.Sprintf("%d", s.chunkSecs),
		"-segment_format", "mpegts",
		"-segment_start_number", fmt.Sprintf("%d", s.nextChunk),
		"-reset_timestamps", "1",
		outputPattern,
	}
}

// launchFFmpeg starts a new ffmpeg process with the current segment start number.
// If sessionID > 0, it launches ffmpeg in the specified Windows session using
// CreateProcessAsUser so it can access that session's desktop.
func (s *ScreenRecorder) launchFFmpeg(ctx context.Context) error {
	args := s.ffmpegArgs()

	if s.sessionID > 0 {
		// Launch in the target user's session with a stdin pipe for graceful shutdown.
		stderrLog := filepath.Join(s.outputDir, "ffmpeg.log")
		pid, stdinPipe, err := launchInSession(s.sessionID, s.ffmpegPath, args, stderrLog)
		if err != nil {
			return fmt.Errorf("launch ffmpeg in session %d: %w", s.sessionID, err)
		}
		s.processPID = pid
		s.processStdin = stdinPipe
		slog.Info("ffmpeg started in user session", "pid", pid, "session_id", s.sessionID, "segment_start", s.nextChunk)
		return nil
	}

	// Launch in current session (interactive mode).
	s.cmd = exec.CommandContext(ctx, s.ffmpegPath, args...)

	var err error
	s.stdin, err = s.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("get stdin pipe: %w", err)
	}

	s.cmd.Stdout = os.Stdout
	s.cmd.Stderr = os.Stderr

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	slog.Info("ffmpeg started", "pid", s.cmd.Process.Pid, "segment_start", s.nextChunk)
	return nil
}

// stopFFmpeg gracefully stops the current ffmpeg process.
func (s *ScreenRecorder) stopFFmpeg() {
	if s.stdin != nil {
		if _, err := s.stdin.Write([]byte("q")); err != nil {
			slog.Warn("failed to send quit to ffmpeg", "error", err)
		}
		s.stdin.Close()
		s.stdin = nil
	}

	// Stop process launched via exec.Command.
	if s.cmd != nil && s.cmd.Process != nil {
		done := make(chan error, 1)
		go func() {
			done <- s.cmd.Wait()
		}()

		select {
		case err := <-done:
			if err != nil {
				slog.Warn("ffmpeg exited with error", "error", err)
			}
		case <-time.After(10 * time.Second):
			slog.Warn("ffmpeg did not exit in time, killing")
			s.cmd.Process.Kill()
		}
		s.cmd = nil
	}

	// Stop process launched via CreateProcessAsUser.
	if s.processPID > 0 {
		// Send 'q' to stdin pipe for graceful shutdown — ffmpeg finishes the
		// current segment and exits cleanly, just like interactive mode.
		if s.processStdin != nil {
			slog.Info("sending quit to ffmpeg via stdin pipe", "pid", s.processPID)
			s.processStdin.Write([]byte("q"))
			s.processStdin.Close()
			s.processStdin = nil
		}

		// Wait for ffmpeg to exit gracefully (up to 10 seconds).
		exited := false
		for i := 0; i < 20; i++ {
			time.Sleep(500 * time.Millisecond)
			checkCmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", s.processPID), "/NH")
			out, _ := checkCmd.Output()
			if !strings.Contains(string(out), fmt.Sprintf("%d", s.processPID)) {
				exited = true
				break
			}
		}
		if !exited {
			slog.Warn("ffmpeg did not exit in time, force killing", "pid", s.processPID)
			exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", s.processPID)).Run()
			time.Sleep(500 * time.Millisecond)
		} else {
			slog.Info("ffmpeg exited gracefully", "pid", s.processPID)
		}
		s.processPID = 0
	}

	// Update nextChunk based on files on disk.
	s.nextChunk = len(s.listChunks())
}

// restartFFmpeg stops and relaunches ffmpeg to pick up the new screen resolution.
func (s *ScreenRecorder) restartFFmpeg(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	slog.Info("restarting ffmpeg for resolution change", "current_chunk", s.nextChunk)

	s.stopFFmpeg()

	if err := s.launchFFmpeg(ctx); err != nil {
		slog.Error("failed to restart ffmpeg", "error", err)
	}
}

// currentResolution returns the screen resolution.
// When running as a service (sessionID > 0), it launches a helper process
// in the user's session to query GetSystemMetrics, since Session 0 can't
// see the user's desktop resolution.
func (s *ScreenRecorder) currentResolution() (int, int) {
	if s.sessionID > 0 {
		return probeSessionResolution(s.sessionID, s.outputDir)
	}
	return getScreenResolution()
}

// probeSessionResolution launches a helper process in the target session
// that calls GetSystemMetrics and writes the resolution to a temp file.
func probeSessionResolution(sessionID uint32, outputDir string) (int, int) {
	resFile := filepath.Join(outputDir, "resolution.txt")

	// Write a small PowerShell script to disk, then execute it in the user's session.
	// This avoids quoting issues with inline -Command strings.
	scriptFile := filepath.Join(outputDir, "rescheck.ps1")
	script := fmt.Sprintf(`Add-Type -MemberDefinition '[DllImport("user32.dll")] public static extern int GetSystemMetrics(int nIndex);' -Name NM -Namespace W32
$w = [W32.NM]::GetSystemMetrics(0)
$h = [W32.NM]::GetSystemMetrics(1)
Set-Content -Path '%s' -Value "$w $h"
`, resFile)
	if err := os.WriteFile(scriptFile, []byte(script), 0644); err != nil {
		return 0, 0
	}

	pid, psStdin, err := launchInSession(sessionID, "powershell.exe",
		[]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", scriptFile}, "")
	if psStdin != nil {
		psStdin.Close()
	}
	if err != nil {
		slog.Debug("resolution probe launch failed", "error", err, "session_id", sessionID)
		return 0, 0
	}

	// Wait for the process to finish.
	for i := 0; i < 20; i++ {
		time.Sleep(200 * time.Millisecond)
		// Check if the output file was updated.
		if info, err := os.Stat(resFile); err == nil && info.Size() > 0 {
			break
		}
		// Check if process is still running.
		if proc, err := os.FindProcess(pid); err == nil {
			// On Windows, FindProcess always succeeds. Try to see if done.
			_ = proc
		}
	}

	// Read the result.
	data, err := os.ReadFile(resFile)
	if err != nil {
		return 0, 0
	}

	var w, h int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d %d", &w, &h); err != nil {
		return 0, 0
	}
	return w, h
}

// monitorResolution polls the screen resolution and restarts ffmpeg when it changes.
func (s *ScreenRecorder) monitorResolution(ctx context.Context) {
	w, h := s.currentResolution()
	slog.Info("initial screen resolution", "width", w, "height", h, "session_id", s.sessionID)

	ticker := time.NewTicker(time.Duration(s.resizePollMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nw, nh := s.currentResolution()
			if nw == 0 || nh == 0 {
				continue
			}
			if nw != w || nh != h {
				slog.Info("screen resolution changed", "old", fmt.Sprintf("%dx%d", w, h), "new", fmt.Sprintf("%dx%d", nw, nh), "session_id", s.sessionID)
				w, h = nw, nh
				s.restartFFmpeg(ctx)
			}
		}
	}
}

// Stop sends 'q' to ffmpeg stdin for a clean shutdown.
func (s *ScreenRecorder) Stop() error {
	slog.Info("stopping screen recorder")

	s.mu.Lock()
	defer s.mu.Unlock()

	s.stopFFmpeg()

	if s.cancel != nil {
		s.cancel()
	}

	return nil
}

// watchChunks monitors the output directory for new chunk files and calls onChunk
// when a chunk is complete (i.e., a newer chunk has appeared).
func (s *ScreenRecorder) watchChunks(ctx context.Context) {
	var lastReported string

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Report any remaining chunks on shutdown.
			s.reportRemainingChunks(lastReported)
			return
		case <-ticker.C:
			chunks := s.listChunks()
			if len(chunks) < 2 {
				continue
			}

			// The last chunk in the sorted list is still being written to.
			// Report all completed chunks (all except the last).
			for _, chunk := range chunks[:len(chunks)-1] {
				if chunk > lastReported {
					slog.Info("chunk ready", "path", chunk)
					if s.onChunk != nil {
						s.onChunk(chunk)
					}
					lastReported = chunk
				}
			}
		}
	}
}

// reportRemainingChunks reports any chunks that haven't been reported yet.
func (s *ScreenRecorder) reportRemainingChunks(lastReported string) {
	chunks := s.listChunks()
	for _, chunk := range chunks {
		if chunk > lastReported {
			slog.Info("chunk ready (final)", "path", chunk)
			if s.onChunk != nil {
				s.onChunk(chunk)
			}
		}
	}
}

// listChunks returns all .ts chunk files in the output directory, sorted by name.
func (s *ScreenRecorder) listChunks() []string {
	entries, err := os.ReadDir(s.outputDir)
	if err != nil {
		return nil
	}

	var chunks []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ts") {
			chunks = append(chunks, filepath.Join(s.outputDir, e.Name()))
		}
	}
	sort.Strings(chunks)
	return chunks
}
