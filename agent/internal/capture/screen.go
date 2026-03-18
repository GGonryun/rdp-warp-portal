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

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	cancel   context.CancelFunc
	nextChunk int // next segment start number across restarts
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

// launchFFmpeg starts a new ffmpeg process with the current segment start number.
func (s *ScreenRecorder) launchFFmpeg(ctx context.Context) error {
	outputPattern := filepath.Join(s.outputDir, "chunk_%03d.ts")

	s.cmd = exec.CommandContext(ctx, s.ffmpegPath,
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

	slog.Info("ffmpeg started", "pid", s.cmd.Process.Pid, "segment_start", s.nextChunk)
	return nil
}

// stopFFmpeg gracefully stops the current ffmpeg process.
// Returns the number of chunks that exist after stopping.
func (s *ScreenRecorder) stopFFmpeg() {
	if s.stdin != nil {
		if _, err := s.stdin.Write([]byte("q")); err != nil {
			slog.Warn("failed to send quit to ffmpeg", "error", err)
		}
		s.stdin.Close()
		s.stdin = nil
	}

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

// monitorResolution polls the screen resolution and restarts ffmpeg when it changes.
func (s *ScreenRecorder) monitorResolution(ctx context.Context) {
	w, h := getScreenResolution()
	slog.Info("initial screen resolution", "width", w, "height", h)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nw, nh := getScreenResolution()
			if nw == 0 || nh == 0 {
				continue
			}
			if nw != w || nh != h {
				slog.Info("screen resolution changed", "old", fmt.Sprintf("%dx%d", w, h), "new", fmt.Sprintf("%dx%d", nw, nh))
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
