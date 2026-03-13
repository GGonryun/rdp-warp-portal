package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/p0rtal-4/gateway-agent/internal/session"
)

// recordingEntry is a single item in the recordings list response.
type recordingEntry struct {
	SessionID    string `json:"session_id"`
	TargetID     string `json:"target_id"`
	TargetHost   string `json:"target_host"`
	StartedAt    string `json:"started_at"`
	EndedAt      string `json:"ended_at"`
	RecordingURL string `json:"recording_url"`
}

// recordingListResponse wraps the list of recording entries.
type recordingListResponse struct {
	Recordings []recordingEntry `json:"recordings"`
}

// handleRecording serves the recording.mp4 file for a completed session.
// It supports Range requests via http.ServeFile.
func (s *Server) handleRecording(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")

	sess, err := s.mgr.GetSession(sessionID)
	if err != nil {
		respondError(w, http.StatusNotFound, fmt.Sprintf("session not found: %s", sessionID))
		return
	}

	// Only serve recordings for completed sessions or sessions that have a
	// recording path set.
	if sess.Status != session.StatusCompleted && sess.RecordingPath == "" {
		respondError(w, http.StatusBadRequest, "session is not completed and has no recording")
		return
	}

	path := filepath.Join(s.cfg.RecordingsDir, sessionID, "recording.mp4")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		respondError(w, http.StatusNotFound, "recording not found")
		return
	}

	// Clear the JSON content-type set by middleware; ServeFile will set the
	// correct content-type for the video file.
	w.Header().Del("Content-Type")
	http.ServeFile(w, r, path)
}

// handleListRecordings returns metadata for all completed sessions that have
// a recording on disk. An optional "date" query parameter (format 2006-01-02)
// filters results to sessions that started on that date.
func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	dateFilter := r.URL.Query().Get("date")
	var filterDate time.Time
	var hasDateFilter bool

	if dateFilter != "" {
		parsed, err := time.Parse("2006-01-02", dateFilter)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid date format, expected YYYY-MM-DD")
			return
		}
		filterDate = parsed
		hasDateFilter = true
	}

	// Get all completed sessions.
	completedSessions := s.mgr.ListSessions(session.StatusCompleted)

	var entries []recordingEntry
	for _, sess := range completedSessions {
		// Only include sessions that actually have a recording file on disk.
		recPath := filepath.Join(s.cfg.RecordingsDir, sess.ID, "recording.mp4")
		if _, err := os.Stat(recPath); err != nil {
			continue
		}

		// Apply date filter if provided.
		if hasDateFilter {
			sessionDate := sess.StartedAt.Truncate(24 * time.Hour)
			if !sessionDate.Equal(filterDate) {
				continue
			}
		}

		endedAt := ""
		if sess.EndedAt != nil {
			endedAt = sess.EndedAt.Format(time.RFC3339)
		}

		entries = append(entries, recordingEntry{
			SessionID:    sess.ID,
			TargetID:     sess.TargetID,
			TargetHost:   sess.TargetHost,
			StartedAt:    sess.StartedAt.Format(time.RFC3339),
			EndedAt:      endedAt,
			RecordingURL: fmt.Sprintf("/api/v1/sessions/%s/recording", sess.ID),
		})
	}

	if entries == nil {
		entries = []recordingEntry{}
	}

	respondJSON(w, http.StatusOK, recordingListResponse{Recordings: entries})
}
