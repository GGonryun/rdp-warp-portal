package recording

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store errors.
var ErrRecordingNotFound = errors.New("recording not found")

// Store provides filesystem-backed storage for session recordings.
//
// Directory layout:
//
//	{baseDir}/{id}/
//	  metadata.json     - Recording struct
//	  events.jsonl      - One RecordingEvent per line
//	  chunks/
//	    000.mp4, 001.mp4, ...
//	  video.mp4         - Final concatenated video
type Store struct {
	mu      sync.RWMutex
	baseDir string
}

// NewStore creates a new recording store rooted at baseDir.
func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// Create initializes a new recording directory and writes its metadata.
func (s *Store) Create(rec *Recording) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.recordingDir(rec.ID)
	chunksDir := filepath.Join(dir, "chunks")

	if err := os.MkdirAll(chunksDir, 0o755); err != nil {
		return fmt.Errorf("create recording directory: %w", err)
	}

	if err := s.writeMetadata(rec); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	return nil
}

// Get reads the metadata for a recording by ID.
func (s *Store) Get(id string) (*Recording, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.readMetadata(id)
}

// List returns all recordings, sorted by start time descending.
func (s *Store) List() ([]*Recording, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read base directory: %w", err)
	}

	var recordings []*Recording
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rec, err := s.readMetadata(entry.Name())
		if err != nil {
			// Skip directories without valid metadata.
			continue
		}
		recordings = append(recordings, rec)
	}

	sort.Slice(recordings, func(i, j int) bool {
		return recordings[i].StartedAt.After(recordings[j].StartedAt)
	})

	return recordings, nil
}

// Update overwrites the metadata for an existing recording.
func (s *Store) Update(rec *Recording) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.recordingDir(rec.ID)); errors.Is(err, os.ErrNotExist) {
		return ErrRecordingNotFound
	}

	if err := s.writeMetadata(rec); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	return nil
}

// AppendChunk writes data as the next numbered chunk file and returns the
// chunk number (zero-based). It also updates the recording metadata with the
// new chunk count and total bytes.
func (s *Store) AppendChunk(id string, data io.Reader) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.readMetadata(id)
	if err != nil {
		return 0, err
	}

	chunkNum := rec.ChunkCount
	chunkPath := filepath.Join(s.recordingDir(id), "chunks", fmt.Sprintf("%03d.ts", chunkNum))

	f, err := os.Create(chunkPath)
	if err != nil {
		return 0, fmt.Errorf("create chunk file: %w", err)
	}
	defer f.Close()

	n, err := io.Copy(f, data)
	if err != nil {
		return 0, fmt.Errorf("write chunk data: %w", err)
	}

	rec.ChunkCount++
	rec.TotalBytes += n
	rec.ChunkDurations = append(rec.ChunkDurations, probeChunkDuration(chunkPath))

	if err := s.writeMetadata(rec); err != nil {
		return 0, fmt.Errorf("update metadata: %w", err)
	}

	return chunkNum, nil
}

// probeChunkDuration uses ffprobe to get the actual duration of a .ts chunk file.
// Returns 0 on error (callers should fall back to the configured chunk duration).
func probeChunkDuration(path string) float64 {
	out, err := exec.Command(
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return 0
	}
	dur, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return dur
}

// AppendEvents appends events to the events.jsonl file and updates the event
// count in the recording metadata.
func (s *Store) AppendEvents(id string, events []RecordingEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.readMetadata(id)
	if err != nil {
		return err
	}

	eventsPath := filepath.Join(s.recordingDir(id), "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open events file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, event := range events {
		if err := enc.Encode(event); err != nil {
			return fmt.Errorf("encode event: %w", err)
		}
	}

	rec.EventCount += len(events)

	if err := s.writeMetadata(rec); err != nil {
		return fmt.Errorf("update metadata: %w", err)
	}

	return nil
}

// Finalize concatenates all chunk files into a single video.mp4 using ffmpeg,
// then marks the recording as completed.
func (s *Store) Finalize(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.readMetadata(id)
	if err != nil {
		return err
	}

	// If no chunks were uploaded, just mark as completed without concatenating.
	if rec.ChunkCount == 0 {
		now := time.Now()
		rec.Status = StatusCompleted
		rec.EndedAt = &now
		rec.DurationSecs = now.Sub(rec.StartedAt).Seconds()
		return s.writeMetadata(rec)
	}

	dir := s.recordingDir(id)
	chunksDir := filepath.Join(dir, "chunks")
	videoPath := filepath.Join(dir, "video.mp4")

	// Build the ffmpeg concat demuxer file.
	concatPath := filepath.Join(dir, "concat.txt")
	concatFile, err := os.Create(concatPath)
	if err != nil {
		return fmt.Errorf("create concat file: %w", err)
	}

	for i := 0; i < rec.ChunkCount; i++ {
		chunkPath := filepath.Join(chunksDir, fmt.Sprintf("%03d.ts", i))
		fmt.Fprintf(concatFile, "file '%s'\n", chunkPath)
	}
	concatFile.Close()

	// Run ffmpeg to concatenate chunks.
	cmd := exec.Command(
		"ffmpeg",
		"-f", "concat",
		"-safe", "0",
		"-i", concatPath,
		"-c", "copy",
		"-movflags", "+faststart",
		videoPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Clean up the concat file on failure.
		os.Remove(concatPath)
		return fmt.Errorf("ffmpeg concat failed: %w: %s", err, output)
	}

	// Clean up the concat file.
	os.Remove(concatPath)

	// Update metadata.
	now := time.Now()
	rec.Status = StatusCompleted
	rec.EndedAt = &now
	rec.DurationSecs = now.Sub(rec.StartedAt).Seconds()

	if err := s.writeMetadata(rec); err != nil {
		return fmt.Errorf("update metadata: %w", err)
	}

	return nil
}

// GetEvents reads all events from the events.jsonl file.
func (s *Store) GetEvents(id string) ([]RecordingEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, err := os.Stat(s.recordingDir(id)); errors.Is(err, os.ErrNotExist) {
		return nil, ErrRecordingNotFound
	}

	eventsPath := filepath.Join(s.recordingDir(id), "events.jsonl")
	f, err := os.Open(eventsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open events file: %w", err)
	}
	defer f.Close()

	var events []RecordingEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event RecordingEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("decode event: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read events file: %w", err)
	}

	return events, nil
}

// Delete removes the recording directory and all its contents.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.recordingDir(id)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return ErrRecordingNotFound
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove recording directory: %w", err)
	}

	return nil
}

// VideoPath returns the absolute path to the final concatenated video.
func (s *Store) VideoPath(id string) string {
	return filepath.Join(s.recordingDir(id), "video.mp4")
}

// ChunkPath returns the absolute path to a specific chunk file.
func (s *Store) ChunkPath(id string, chunkIndex int) string {
	return filepath.Join(s.recordingDir(id), "chunks", fmt.Sprintf("%03d.ts", chunkIndex))
}

// GeneratePlaylist returns an HLS .m3u8 playlist for the recording.
// For live (still recording) sessions, it includes EXT-X-TARGETDURATION
// but omits EXT-X-ENDLIST so players keep polling for new segments.
func (s *Store) GeneratePlaylist(id string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, err := s.readMetadata(id)
	if err != nil {
		return nil, err
	}

	// Use the chunk duration the agent reported; fall back to 30 for old recordings.
	chunkSecs := rec.ChunkSecs
	if chunkSecs <= 0 {
		chunkSecs = 30
	}
	defaultDur := float64(chunkSecs)

	// Resolve per-segment durations and compute max for EXT-X-TARGETDURATION.
	maxDur := defaultDur
	durations := make([]float64, rec.ChunkCount)
	for i := 0; i < rec.ChunkCount; i++ {
		dur := defaultDur
		if i < len(rec.ChunkDurations) && rec.ChunkDurations[i] > 0 {
			dur = rec.ChunkDurations[i]
		}
		durations[i] = dur
		if dur > maxDur {
			maxDur = dur
		}
	}

	var b []byte
	b = append(b, "#EXTM3U\n"...)
	b = append(b, "#EXT-X-VERSION:3\n"...)
	b = append(b, fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", int(math.Ceil(maxDur)))...)
	b = append(b, "#EXT-X-MEDIA-SEQUENCE:0\n"...)

	for i := 0; i < rec.ChunkCount; i++ {
		// Insert a discontinuity marker after a short chunk (ffmpeg restart boundary).
		// A chunk shorter than half the target duration indicates ffmpeg was restarted.
		if i > 0 && durations[i-1] > 0 && durations[i-1] < defaultDur*0.5 {
			b = append(b, "#EXT-X-DISCONTINUITY\n"...)
		}
		b = append(b, fmt.Sprintf("#EXTINF:%.3f,\n", durations[i])...)
		b = append(b, fmt.Sprintf("segments/%03d.ts\n", i)...)
	}

	if rec.Status == StatusCompleted {
		b = append(b, "#EXT-X-ENDLIST\n"...)
	}

	return b, nil
}

// recordingDir returns the directory path for a recording.
func (s *Store) recordingDir(id string) string {
	return filepath.Join(s.baseDir, id)
}

// writeMetadata serializes the recording to metadata.json.
func (s *Store) writeMetadata(rec *Recording) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	path := filepath.Join(s.recordingDir(rec.ID), "metadata.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write metadata file: %w", err)
	}

	return nil
}

// readMetadata deserializes the recording from metadata.json.
func (s *Store) readMetadata(id string) (*Recording, error) {
	path := filepath.Join(s.recordingDir(id), "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrRecordingNotFound
		}
		return nil, fmt.Errorf("read metadata file: %w", err)
	}

	var rec Recording
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}

	return &rec, nil
}
