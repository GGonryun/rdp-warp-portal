package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/p0-security/rdp-broker/internal/recording"
)

type RecordingsHandler struct {
	store *recording.Store
}

func NewRecordingsHandler(store *recording.Store) *RecordingsHandler {
	return &RecordingsHandler{
		store: store,
	}
}

func (h *RecordingsHandler) RegisterRoutes(router *Router) {
	router.HandleFunc("POST /api/recordings", h.createRecording, false)
	router.HandleFunc("POST /api/recordings/{id}/chunks", h.uploadChunk, false)
	router.HandleFunc("POST /api/recordings/{id}/events", h.sendEvents, false)
	router.HandleFunc("PUT /api/recordings/{id}/end", h.endRecording, false)

	router.HandleFunc("DELETE /api/recordings/{id}", h.deleteRecording, false)

	router.HandleFunc("GET /api/recordings", h.listRecordings, false)
	router.HandleFunc("GET /api/recordings/{id}", h.getRecording, false)
	router.HandleFunc("GET /api/recordings/{id}/video", h.streamVideo, false)
	router.HandleFunc("GET /api/recordings/{id}/events", h.getEvents, false)
	router.HandleFunc("GET /api/recordings/{id}/hls/playlist.m3u8", h.hlsPlaylist, false)
	router.HandleFunc("GET /api/recordings/{id}/hls/segments/{segment}", h.hlsSegment, false)
}

type CreateRecordingRequest struct {
	SessionID     string `json:"session_id"`
	TargetID      string `json:"target_id"`
	TargetName    string `json:"target_name"`
	WindowsUser   string `json:"windows_user"`
	ProxyUser     string `json:"proxy_user"`
	AgentHostname string `json:"agent_hostname"`
}

func (h *RecordingsHandler) createRecording(w http.ResponseWriter, r *http.Request) {
var req CreateRecordingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	id := fmt.Sprintf("rec-%d-%s", time.Now().UnixMilli(), randomHex(4))

	rec := &recording.Recording{
		ID:            id,
		SessionID:     req.SessionID,
		TargetID:      req.TargetID,
		TargetName:    req.TargetName,
		WindowsUser:   req.WindowsUser,
		ProxyUser:     req.ProxyUser,
		AgentHostname: req.AgentHostname,
		Status:        recording.StatusRecording,
		StartedAt:     time.Now(),
	}

	if err := h.store.Create(rec); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, rec)
}

func (h *RecordingsHandler) uploadChunk(w http.ResponseWriter, r *http.Request) {
id := r.PathValue("id")

	n, err := h.store.AppendChunk(id, r.Body)
	if err != nil {
		if errors.Is(err, recording.ErrRecordingNotFound) {
			writeError(w, http.StatusNotFound, "recording not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]int{"chunk": n})
}

func (h *RecordingsHandler) sendEvents(w http.ResponseWriter, r *http.Request) {
id := r.PathValue("id")

	var events []recording.RecordingEvent
	if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.store.AppendEvents(id, events); err != nil {
		if errors.Is(err, recording.ErrRecordingNotFound) {
			writeError(w, http.StatusNotFound, "recording not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *RecordingsHandler) endRecording(w http.ResponseWriter, r *http.Request) {
id := r.PathValue("id")

	if err := h.store.Finalize(id); err != nil {
		if errors.Is(err, recording.ErrRecordingNotFound) {
			writeError(w, http.StatusNotFound, "recording not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rec, err := h.store.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, rec)
}

func (h *RecordingsHandler) deleteRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.store.Delete(id); err != nil {
		if errors.Is(err, recording.ErrRecordingNotFound) {
			writeError(w, http.StatusNotFound, "recording not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *RecordingsHandler) listRecordings(w http.ResponseWriter, r *http.Request) {
	recordings, err := h.store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if recordings == nil {
		recordings = []*recording.Recording{}
	}

	q := r.URL.Query()

	// Filter by target name.
	if target := q.Get("target"); target != "" {
		filtered := make([]*recording.Recording, 0)
		for _, rec := range recordings {
			if strings.EqualFold(rec.TargetName, target) || strings.EqualFold(rec.AgentHostname, target) {
				filtered = append(filtered, rec)
			}
		}
		recordings = filtered
	}

	// Filter by windows user. If the filter is an email address, also match
	// against just the local part (e.g. "golden.marmot@p0lab1.internal"
	// matches a WindowsUser of "golden.marmot").
	if user := q.Get("user"); user != "" {
		localPart := user
		if i := strings.Index(user, "@"); i > 0 {
			localPart = user[:i]
		}
		filtered := make([]*recording.Recording, 0)
		for _, rec := range recordings {
			if strings.EqualFold(rec.WindowsUser, user) || strings.EqualFold(rec.WindowsUser, localPart) {
				filtered = append(filtered, rec)
			}
		}
		recordings = filtered
	}

	// Filter by time range — recordings must be contained within [start, end].
	// Try RFC3339Nano first (handles milliseconds from JS toISOString), fall back to RFC3339.
	parseTime := func(s string) (time.Time, error) {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, nil
		}
		return time.Parse(time.RFC3339, s)
	}

	if startStr := q.Get("start"); startStr != "" {
		if start, err := parseTime(startStr); err == nil {
			filtered := make([]*recording.Recording, 0)
			for _, rec := range recordings {
				if !rec.StartedAt.Before(start) {
					filtered = append(filtered, rec)
				}
			}
			recordings = filtered
		}
	}
	if endStr := q.Get("end"); endStr != "" {
		if end, err := parseTime(endStr); err == nil {
			filtered := make([]*recording.Recording, 0)
			for _, rec := range recordings {
				recEnd := rec.StartedAt.Add(time.Duration(rec.DurationSecs * float64(time.Second)))
				if rec.EndedAt != nil {
					recEnd = *rec.EndedAt
				}
				if !recEnd.After(end) {
					filtered = append(filtered, rec)
				}
			}
			recordings = filtered
		}
	}

	// Filter by status.
	if status := q.Get("status"); status != "" {
		filtered := make([]*recording.Recording, 0)
		for _, rec := range recordings {
			if strings.EqualFold(string(rec.Status), status) {
				filtered = append(filtered, rec)
			}
		}
		recordings = filtered
	}

	writeJSON(w, http.StatusOK, recordings)
}

func (h *RecordingsHandler) getRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	rec, err := h.store.Get(id)
	if err != nil {
		if errors.Is(err, recording.ErrRecordingNotFound) {
			writeError(w, http.StatusNotFound, "recording not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, rec)
}

func (h *RecordingsHandler) streamVideo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Verify recording exists
	if _, err := h.store.Get(id); err != nil {
		if errors.Is(err, recording.ErrRecordingNotFound) {
			writeError(w, http.StatusNotFound, "recording not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	path := h.store.VideoPath(id)

	http.ServeFile(w, r, path)
}

func (h *RecordingsHandler) getEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	events, err := h.store.GetEvents(id)
	if err != nil {
		if errors.Is(err, recording.ErrRecordingNotFound) {
			writeError(w, http.StatusNotFound, "recording not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, events)
}

func (h *RecordingsHandler) hlsPlaylist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	playlist, err := h.store.GeneratePlaylist(id, 30)
	if err != nil {
		if errors.Is(err, recording.ErrRecordingNotFound) {
			writeError(w, http.StatusNotFound, "recording not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(playlist)
}

func (h *RecordingsHandler) hlsSegment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	segment := r.PathValue("segment")

	// Parse segment index from filename like "000.ts"
	name := strings.TrimSuffix(segment, ".ts")
	index, err := strconv.Atoi(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid segment name")
		return
	}

	path := h.store.ChunkPath(id, index)
	w.Header().Set("Content-Type", "video/mp2t")
	http.ServeFile(w, r, path)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
