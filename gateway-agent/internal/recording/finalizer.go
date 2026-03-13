package recording

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/p0rtal-4/gateway-agent/internal/config"
)

// Finalize converts the HLS segments for the given session into a single MP4
// file using ffmpeg. If recording.mp4 already exists the call is a no-op.
//
// The command executed is equivalent to:
//
//	ffmpeg -y -i playlist.m3u8 -c copy recording.mp4
func Finalize(sessionID string, cfg *config.Config) error {
	dir := filepath.Join(cfg.RecordingsDir, sessionID)
	mp4 := filepath.Join(dir, "recording.mp4")

	// Skip if the MP4 already exists.
	if _, err := os.Stat(mp4); err == nil {
		return nil
	}

	playlist := filepath.Join(dir, "playlist.m3u8")
	if _, err := os.Stat(playlist); err != nil {
		return fmt.Errorf("playlist not found: %w", err)
	}

	ffmpeg := cfg.FFmpegPath
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}

	cmd := exec.Command(ffmpeg, "-y", "-i", playlist, "-c", "copy", mp4)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w: %s", err, string(output))
	}
	return nil
}

// CleanupHLSSegments removes the .ts segment files and the playlist.m3u8 from
// the session recording directory. It only proceeds when recording.mp4 already
// exists so that raw segments are never deleted before a successful mux.
func CleanupHLSSegments(sessionID string, cfg *config.Config) error {
	dir := filepath.Join(cfg.RecordingsDir, sessionID)

	// Refuse to clean up unless the MP4 is present.
	mp4 := filepath.Join(dir, "recording.mp4")
	if _, err := os.Stat(mp4); err != nil {
		return fmt.Errorf("recording.mp4 does not exist, refusing cleanup: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read session dir: %w", err)
	}

	var firstErr error
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".ts") || name == "playlist.m3u8" {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
