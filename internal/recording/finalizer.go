package recording

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/p0rtal-4/p0rtal/internal/config"
)

// FinalizePlaylist appends #EXT-X-ENDLIST to the HLS playlist so that players
// treat it as a complete VOD rather than a live stream. This is idempotent.
func FinalizePlaylist(sessionID string, cfg *config.Config) error {
	dir := filepath.Join(cfg.RecordingsDir, sessionID)
	playlist := filepath.Join(dir, "playlist.m3u8")

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
