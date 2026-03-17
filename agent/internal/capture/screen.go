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
	"time"
)

// ScreenRecorder captures the screen using ffmpeg and produces video chunks.
type ScreenRecorder struct {
	ffmpegPath string
	framerate  int
	chunkSecs  int
	outputDir  string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	onChunk    func(chunkPath string)
	cancel     context.CancelFunc
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

	slog.Info("ffmpeg started", "pid", s.cmd.Process.Pid, "output_dir", s.outputDir)

	// Start chunk watcher goroutine.
	go s.watchChunks(ctx)

	return nil
}

// Stop sends 'q' to ffmpeg stdin for a clean shutdown.
func (s *ScreenRecorder) Stop() error {
	slog.Info("stopping screen recorder")

	if s.stdin != nil {
		// Send 'q' to ffmpeg for graceful shutdown.
		if _, err := s.stdin.Write([]byte("q")); err != nil {
			slog.Warn("failed to send quit to ffmpeg", "error", err)
		}
		s.stdin.Close()
	}

	if s.cancel != nil {
		s.cancel()
	}

	if s.cmd != nil && s.cmd.Process != nil {
		// Wait for ffmpeg to exit gracefully.
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
	}

	return nil
}

// watchChunks monitors the output directory for new chunk files and calls onChunk
// when a chunk is complete (i.e., a newer chunk has appeared and the previous
// chunk's file size has stabilized).
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

// listChunks returns all .mp4 chunk files in the output directory, sorted by name.
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
