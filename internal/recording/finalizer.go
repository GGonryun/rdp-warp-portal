package recording

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/p0rtal-4/p0rtal/internal/config"
)

// FinalizePlaylistDir appends #EXT-X-ENDLIST to the HLS playlist in the given
// directory so that players treat it as a complete VOD rather than a live
// stream. This is idempotent.
func FinalizePlaylistDir(recordingDir string) error {
	playlist := filepath.Join(recordingDir, "playlist.m3u8")

	data, err := os.ReadFile(playlist)
	if err != nil {
		return fmt.Errorf("playlist not found: %w", err)
	}

	// Already finalized.
	if strings.Contains(string(data), "#EXT-X-ENDLIST") {
		return nil
	}

	f, err := os.OpenFile(playlist, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open playlist for append: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString("\n#EXT-X-ENDLIST\n"); err != nil {
		return fmt.Errorf("write endlist tag: %w", err)
	}
	return nil
}

// FinalizePlaylist finalizes the playlist for a session. It checks for rec_*
// subdirectories first (multi-recording), falling back to the flat session
// directory for backward compatibility.
func FinalizePlaylist(sessionID string, cfg *config.Config) error {
	sessionDir := filepath.Join(cfg.RecordingsDir, sessionID)

	// Try multi-recording structure first.
	recDirs, _ := filepath.Glob(filepath.Join(sessionDir, "rec_*"))
	if len(recDirs) > 0 {
		return FinalizeAllPlaylists(sessionID, cfg)
	}

	// Fall back to flat structure.
	return FinalizePlaylistDir(sessionDir)
}

// FinalizeAllPlaylists finalizes every rec_* subdirectory's playlist within a
// session directory. Errors on individual playlists are collected but do not
// stop processing.
func FinalizeAllPlaylists(sessionID string, cfg *config.Config) error {
	sessionDir := filepath.Join(cfg.RecordingsDir, sessionID)
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return fmt.Errorf("read session dir: %w", err)
	}

	var lastErr error
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "rec_") {
			continue
		}
		recDir := filepath.Join(sessionDir, e.Name())
		if err := FinalizePlaylistDir(recDir); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
