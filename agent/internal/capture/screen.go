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
	ffmpegPath string
	framerate  int
	chunkSecs  int
	outputDir  string
	onChunk    func(chunkPath string)

	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	cancel     context.CancelFunc
	chunkIndex int // running chunk counter across ffmpeg restarts
}

// NewScreenRecorder creates a new screen recorder.
func NewScreenRecorder(ffmpegPath string, framerate, chunkSecs int, outputDir string, onChunk func(string)) *ScreenRecorder {
	return &ScreenRecorder{
		ffmpegPath: ffmpegPath,
		framerate:  framerate,
		chunkSecs:  chunkSecs,
		outputDir:  outputDir,
		onChunk:    onChunk,
	}
}

// Start launches the ffmpeg process and begins watching for chunks.
func (s *ScreenRecorder) Start(ctx context.Context) error {
	if err := os.MkdirAll(s.outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	ctx, s.cancel = context.WithCancel(ctx)

	if err := s.startFfmpeg(ctx); err != nil {
		return err
	}

	// Start chunk watcher goroutine.
	go s.watchChunks(ctx)

	// Start resolution monitor goroutine.
	go s.monitorResolution(ctx)

	return nil
}

// startFfmpeg launches a new ffmpeg process using the current chunkIndex for output naming.
func (s *ScreenRecorder) startFfmpeg(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Use segment_start_number so chunk files continue from the current index.
	outputPattern := filepath.Join(s.outputDir, "chunk_%03d.ts")

	s.cmd = exec.CommandContext(ctx, s.ffmpegPath,
		"-f", "gdigrab",
		"-framerate", fmt.Sprintf("%d", s.framerate),
		"-i", "desktop",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-crf", "28",
		"-pix_fmt", "yuv420p",
		"-f", "segment",
		"-segment_time", fmt.Sprintf("%d", s.chunkSecs),
		"-segment_format", "mpegts",
		"-segment_start_number", fmt.Sprintf("%d", s.chunkIndex),
		"-reset_timestamps", "1",
		outputPattern,
	)

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

	slog.Info("ffmpeg started", "pid", s.cmd.Process.Pid, "chunk_start", s.chunkIndex)
	return nil
}

// stopFfmpeg gracefully stops the current ffmpeg process and waits for it to flush.
func (s *ScreenRecorder) stopFfmpeg() {
	s.mu.Lock()
	stdin := s.stdin
	cmd := s.cmd
	s.mu.Unlock()

	if stdin != nil {
		if _, err := stdin.Write([]byte("q")); err != nil {
			slog.Warn("failed to send quit to ffmpeg", "error", err)
		}
		stdin.Close()
	}

	if cmd != nil && cmd.Process != nil {
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		select {
		case err := <-done:
			if err != nil {
				slog.Debug("ffmpeg exited with error", "error", err)
			}
		case <-time.After(10 * time.Second):
			slog.Warn("ffmpeg did not exit in time, killing")
			cmd.Process.Kill()
		}
	}

	// Count chunks on disk to update chunkIndex for the next ffmpeg instance.
	s.mu.Lock()
	s.chunkIndex = len(s.listChunksLocked())
	s.mu.Unlock()
}

// restartFfmpeg gracefully stops the current ffmpeg and starts a new one.
func (s *ScreenRecorder) restartFfmpeg(ctx context.Context) {
	slog.Info("restarting ffmpeg due to resolution change")
	s.stopFfmpeg()

	// Brief pause to let file handles release.
	time.Sleep(500 * time.Millisecond)

	if err := s.startFfmpeg(ctx); err != nil {
		slog.Error("failed to restart ffmpeg", "error", err)
	}
}

// monitorResolution polls the screen resolution and restarts ffmpeg when it changes.
func (s *ScreenRecorder) monitorResolution(ctx context.Context) {
	w, h := getScreenResolution()
	slog.Info("initial screen resolution", "width", w, "height", h)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newW, newH := getScreenResolution()
			if newW != w || newH != h {
				slog.Info("screen resolution changed",
					"old_width", w, "old_height", h,
					"new_width", newW, "new_height", newH,
				)
				w, h = newW, newH
				s.restartFfmpeg(ctx)
			}
		}
	}
}

// Stop sends 'q' to ffmpeg stdin for a clean shutdown.
func (s *ScreenRecorder) Stop() error {
	slog.Info("stopping screen recorder")
	s.stopFfmpeg()

	if s.cancel != nil {
		s.cancel()
	}

	return nil
}

// watchChunks monitors the output directory for new chunk files and calls onChunk
// when a chunk is complete (i.e., a newer chunk has appeared).
func (s *ScreenRecorder) watchChunks(ctx context.Context) {
	var lastReported string

	ticker := time.NewTicker(2 * time.Second)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listChunksLocked()
}

// listChunksLocked returns all .ts chunk files. Caller must hold s.mu.
func (s *ScreenRecorder) listChunksLocked() []string {
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
