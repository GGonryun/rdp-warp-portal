package recording

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/p0rtal-4/p0rtal/internal/config"
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

// HasPlaylist reports whether a playlist.m3u8 file exists for the given session
// in any rec_* subdirectory (or the flat session directory for backward compat).
func (w *Watcher) HasPlaylist(sessionID string) bool {
	sessionDir := filepath.Join(w.cfg.RecordingsDir, sessionID)

	// Check rec_* subdirectories first.
	matches, _ := filepath.Glob(filepath.Join(sessionDir, "rec_*", "playlist.m3u8"))
	if len(matches) > 0 {
		return true
	}

	// Fall back to flat structure.
	p := filepath.Join(sessionDir, "playlist.m3u8")
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

// SegmentCount returns the total number of .ts segment files across all rec_*
// subdirectories for the session. Falls back to the flat directory.
func (w *Watcher) SegmentCount(sessionID string) int {
	sessionDir := filepath.Join(w.cfg.RecordingsDir, sessionID)
	count := 0

	// Count in rec_* subdirectories.
	recDirs := w.ListRecordingDirs(sessionID)
	if len(recDirs) > 0 {
		for _, recName := range recDirs {
			recDir := filepath.Join(sessionDir, recName)
			entries, err := os.ReadDir(recDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".ts") {
					count++
				}
			}
		}
		return count
	}

	// Fall back to flat structure.
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ts") {
			count++
		}
	}
	return count
}

// ListRecordingDirs returns the sorted list of rec_* subdirectory names for a
// session. Returns nil if no rec_* directories exist.
func (w *Watcher) ListRecordingDirs(sessionID string) []string {
	sessionDir := filepath.Join(w.cfg.RecordingsDir, sessionID)
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return nil
	}

	var dirs []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "rec_") {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs
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
