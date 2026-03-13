package recording

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/p0rtal-4/gateway-agent/internal/config"
)

// RecordingInfo holds metadata about a completed recording.
type RecordingInfo struct {
	SessionID     string
	RecordingPath string
	Size          int64
	ModTime       time.Time
}

// Watcher provides helpers to inspect the state of session recordings on disk.
type Watcher struct {
	cfg *config.Config
}

// NewWatcher creates a new Watcher backed by the given configuration.
func NewWatcher(cfg *config.Config) *Watcher {
	return &Watcher{cfg: cfg}
}

// HasPlaylist reports whether a playlist.m3u8 file exists for the given session.
func (w *Watcher) HasPlaylist(sessionID string) bool {
	p := filepath.Join(w.cfg.RecordingsDir, sessionID, "playlist.m3u8")
	_, err := os.Stat(p)
	return err == nil
}

// GetRecordingPath returns the absolute path to recording.mp4 for the session
// if the file exists on disk. It returns an empty string otherwise.
func (w *Watcher) GetRecordingPath(sessionID string) string {
	p := filepath.Join(w.cfg.RecordingsDir, sessionID, "recording.mp4")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// GetRecordingDir returns the directory that holds (or will hold) recording
// artefacts for the given session.
func (w *Watcher) GetRecordingDir(sessionID string) string {
	return filepath.Join(w.cfg.RecordingsDir, sessionID)
}

// SegmentCount returns the number of .ts segment files present in the session
// recording directory. It returns 0 if the directory cannot be read.
func (w *Watcher) SegmentCount(sessionID string) int {
	dir := filepath.Join(w.cfg.RecordingsDir, sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ts") {
			count++
		}
	}
	return count
}

// ListCompletedRecordings scans every sub-directory of RecordingsDir and
// returns metadata for each session that already has a recording.mp4 file.
func (w *Watcher) ListCompletedRecordings() []RecordingInfo {
	entries, err := os.ReadDir(w.cfg.RecordingsDir)
	if err != nil {
		return nil
	}

	var results []RecordingInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mp4 := filepath.Join(w.cfg.RecordingsDir, e.Name(), "recording.mp4")
		info, err := os.Stat(mp4)
		if err != nil {
			continue
		}
		results = append(results, RecordingInfo{
			SessionID:     e.Name(),
			RecordingPath: mp4,
			Size:          info.Size(),
			ModTime:       info.ModTime(),
		})
	}
	return results
}
