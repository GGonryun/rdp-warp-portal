package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/p0rtal-4/p0rtal/internal/session"
)

var recIDPattern = regexp.MustCompile(`^rec_\d{3}$`)

// handleStreamFile serves HLS playlist (.m3u8) and segment (.ts) files for a
// given session's recording directory. For backward compatibility, it serves
// from the latest rec_* subdirectory if one exists, or the flat session dir.
func (s *Server) handleStreamFile(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")
	filename := chi.URLParam(r, "filename")

	if !validateStreamFilename(filename) {
		respondError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if ext != ".m3u8" && ext != ".ts" {
		respondError(w, http.StatusBadRequest, "unsupported file type")
		return
	}

	if _, err := s.mgr.GetSession(sessionID); err != nil {
		respondError(w, http.StatusNotFound, "session not found")
		return
	}

	// Find the latest rec_* subdirectory, or fall back to flat dir.
	sessionDir := filepath.Join(s.cfg.RecordingsDir, sessionID)
	serveDir := latestRecDir(sessionDir)

	serveHLSFile(w, r, serveDir, filename, ext)
}

// handleRecordingStreamFile serves HLS files from a specific rec_NNN subdirectory.
func (s *Server) handleRecordingStreamFile(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")
	recordingID := chi.URLParam(r, "recording_id")
	filename := chi.URLParam(r, "filename")

	if !recIDPattern.MatchString(recordingID) {
		respondError(w, http.StatusBadRequest, "invalid recording id")
		return
	}
	if !validateStreamFilename(filename) {
		respondError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if ext != ".m3u8" && ext != ".ts" {
		respondError(w, http.StatusBadRequest, "unsupported file type")
		return
	}

	if _, err := s.mgr.GetSession(sessionID); err != nil {
		respondError(w, http.StatusNotFound, "session not found")
		return
	}

	serveDir := filepath.Join(s.cfg.RecordingsDir, sessionID, recordingID)
	serveHLSFile(w, r, serveDir, filename, ext)
}

// handleListSessionRecordings returns the list of recordings for a session.
func (s *Server) handleListSessionRecordings(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")

	sess, err := s.mgr.GetSession(sessionID)
	if err != nil {
		respondError(w, http.StatusNotFound, "session not found")
		return
	}

	// Discover rec_* directories on disk.
	sessionDir := filepath.Join(s.cfg.RecordingsDir, sessionID)
	entries, _ := os.ReadDir(sessionDir)

	var diskDirs []string
	for _, e := range entries {
		if e.IsDir() && recIDPattern.MatchString(e.Name()) {
			diskDirs = append(diskDirs, e.Name())
		}
	}
	sort.Strings(diskDirs)

	// Build index from in-memory recordings for status/timestamps.
	recIndex := make(map[string]session.Recording)
	for _, rec := range sess.Recordings {
		recIndex[rec.ID] = rec
	}

	type recEntry struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		StartedAt string `json:"started_at,omitempty"`
		EndedAt   string `json:"ended_at,omitempty"`
		StreamURL string `json:"stream_url"`
	}

	var result []recEntry
	for _, dirName := range diskDirs {
		entry := recEntry{
			ID:        dirName,
			Status:    "completed",
			StreamURL: fmt.Sprintf("/api/v1/sessions/%s/recordings/%s/stream/playlist.m3u8", sessionID, dirName),
		}
		if rec, ok := recIndex[dirName]; ok {
			entry.Status = rec.Status
			entry.StartedAt = rec.StartedAt.Format(time.RFC3339)
			if rec.EndedAt != nil {
				entry.EndedAt = rec.EndedAt.Format(time.RFC3339)
			}
		}
		result = append(result, entry)
	}

	if result == nil {
		result = []recEntry{}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"recordings": result})
}

// handleMonitor serves a self-contained HTML page that uses hls.js to stream
// the live recording of an active session.
func (s *Server) handleMonitor(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")

	if _, err := s.mgr.GetSession(sessionID); err != nil {
		respondError(w, http.StatusNotFound, "session not found")
		return
	}

	page := strings.ReplaceAll(monitorHTML, "{{SESSION_ID}}", sessionID)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, page)
}

// ---------- helpers ----------

func validateStreamFilename(filename string) bool {
	return !strings.Contains(filename, "..") &&
		!strings.Contains(filename, "/") &&
		!strings.Contains(filename, "\\")
}

// latestRecDir returns the path to the latest rec_* subdirectory in sessionDir,
// or sessionDir itself if no rec_* dirs exist (backward compat).
func latestRecDir(sessionDir string) string {
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return sessionDir
	}
	var latest string
	for _, e := range entries {
		if e.IsDir() && recIDPattern.MatchString(e.Name()) {
			if e.Name() > latest {
				latest = e.Name()
			}
		}
	}
	if latest != "" {
		return filepath.Join(sessionDir, latest)
	}
	return sessionDir
}

// serveHLSFile serves a single HLS file from the given directory.
func serveHLSFile(w http.ResponseWriter, r *http.Request, dir, filename, ext string) {
	filePath := filepath.Join(dir, filename)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		respondError(w, http.StatusNotFound, "file not found")
		return
	}

	// For playlists, don't serve until at least one segment exists.
	if ext == ".m3u8" {
		tsFiles, _ := filepath.Glob(filepath.Join(dir, "*.ts"))
		if len(tsFiles) == 0 {
			respondError(w, http.StatusNotFound, "stream not ready")
			return
		}
	}

	switch ext {
	case ".m3u8":
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	case ".ts":
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "max-age=3600")
	}

	http.ServeFile(w, r, filePath)
}

// monitorHTML is the self-contained HTML page served by handleMonitor.
// It shows a list of recordings for the session and plays the selected one.
const monitorHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Session Monitor — {{SESSION_ID}}</title>
<script src="https://cdn.jsdelivr.net/npm/hls.js@1"></script>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    background: #111; color: #eee; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    display: flex; flex-direction: column; align-items: center; min-height: 100vh; padding: 1rem;
  }
  h1 { font-size: 1.2rem; margin-bottom: 0.5rem; }
  #status {
    display: inline-block; padding: 0.25rem 0.75rem; border-radius: 4px;
    font-size: 0.85rem; margin-bottom: 0.75rem; font-weight: 600;
  }
  .status-active     { background: #22c55e; color: #000; }
  .status-pending    { background: #eab308; color: #000; }
  .status-ready      { background: #3b82f6; color: #fff; }
  .status-disconnected { background: #f97316; color: #000; }
  .status-completed  { background: #6b7280; color: #fff; }
  .status-terminated { background: #ef4444; color: #fff; }
  .status-failed     { background: #dc2626; color: #fff; }
  #session-info {
    font-size: 0.8rem; color: #999; margin-bottom: 0.75rem; text-align: center; line-height: 1.6;
  }
  #recordings-list {
    display: flex; gap: 0.5rem; flex-wrap: wrap; justify-content: center;
    margin-bottom: 0.75rem;
  }
  .rec-tab {
    padding: 0.4rem 0.8rem; border: 1px solid #444; border-radius: 4px;
    cursor: pointer; font-size: 0.8rem; background: #222; color: #ccc;
    transition: all 0.15s;
  }
  .rec-tab:hover { border-color: #888; }
  .rec-tab.selected { background: #3b82f6; color: #fff; border-color: #3b82f6; }
  .rec-tab .live-dot {
    display: inline-block; width: 8px; height: 8px; background: #22c55e;
    border-radius: 50%; margin-left: 0.4rem; animation: pulse 1.5s infinite;
  }
  @keyframes pulse { 0%,100% { opacity: 1; } 50% { opacity: 0.3; } }
  .rec-tab .completed-dot {
    display: inline-block; width: 8px; height: 8px; background: #6b7280;
    border-radius: 50%; margin-left: 0.4rem;
  }
  #no-recordings {
    font-size: 0.85rem; color: #666; margin-bottom: 0.75rem;
  }
  video {
    width: 100%; max-width: 960px; background: #000; border-radius: 6px;
  }
  .controls {
    margin-top: 0.75rem; display: flex; gap: 0.75rem; flex-wrap: wrap; justify-content: center;
  }
  button {
    padding: 0.5rem 1rem; border: none; border-radius: 4px; cursor: pointer;
    font-size: 0.9rem; font-weight: 600; transition: opacity 0.15s;
  }
  button:hover { opacity: 0.85; }
  #btn-live { background: #3b82f6; color: #fff; }
  #btn-terminate { background: #ef4444; color: #fff; }
  #error-box {
    margin-top: 1rem; padding: 0.5rem 1rem; background: #7f1d1d; border-radius: 4px;
    display: none; font-size: 0.85rem;
  }
</style>
</head>
<body>
<h1>Session Monitor</h1>
<div id="status" class="status-pending">loading…</div>
<div id="session-info"></div>
<div id="recordings-list"></div>
<div id="no-recordings" style="display:none">No recordings yet — waiting for connection…</div>
<video id="video" controls autoplay muted></video>
<div class="controls">
  <button id="btn-live">Jump to Live</button>
  <button id="btn-terminate">Terminate Session</button>
</div>
<div id="error-box"></div>

<script>
(function() {
  var SESSION_ID = "{{SESSION_ID}}";
  var BASE       = "/api/v1/sessions/" + SESSION_ID;
  var video      = document.getElementById("video");
  var statusEl   = document.getElementById("status");
  var infoEl     = document.getElementById("session-info");
  var recListEl  = document.getElementById("recordings-list");
  var noRecEl    = document.getElementById("no-recordings");
  var errorBox   = document.getElementById("error-box");
  var hls        = null;

  var sessionEnded = false;
  var selectedRecID = null;
  var recordings = [];

  // ---- session info polling ----
  function fetchSessionInfo() {
    fetch(BASE)
      .then(function(r) { return r.json(); })
      .then(function(data) {
        var st = data.status || "unknown";
        statusEl.textContent = st;
        statusEl.className = "status-" + st;

        var lines = [];
        if (data.target_name)  lines.push("Target: " + data.target_name + " (" + data.target_host + ")");
        if (data.started_at)   lines.push("Started: " + new Date(data.started_at).toLocaleString());
        if (data.expires_at)   lines.push("Expires: " + new Date(data.expires_at).toLocaleString());
        infoEl.innerHTML = lines.join("<br>");

        if (!sessionEnded && (st === "completed" || st === "terminated")) {
          sessionEnded = true;
          document.getElementById("btn-terminate").style.display = "none";
          document.getElementById("btn-live").textContent = "Restart";
          // Reload current recording after finalization
          setTimeout(function() { if (selectedRecID) loadRecording(selectedRecID); }, 2000);
        }
      })
      .catch(function() {
        statusEl.textContent = "error";
        statusEl.className = "";
      });
  }
  fetchSessionInfo();
  setInterval(fetchSessionInfo, 5000);

  // ---- recordings polling ----
  function fetchRecordings() {
    fetch(BASE + "/recordings")
      .then(function(r) { return r.json(); })
      .then(function(data) {
        recordings = data.recordings || [];
        renderRecordingTabs();

        // Auto-select: prefer active recording, or latest if none selected
        if (recordings.length > 0) {
          noRecEl.style.display = "none";
          var activeRec = null;
          for (var i = 0; i < recordings.length; i++) {
            if (recordings[i].status === "active") { activeRec = recordings[i]; break; }
          }
          // Auto-switch to a new active recording
          if (activeRec && selectedRecID !== activeRec.id) {
            selectRecording(activeRec.id);
          } else if (!selectedRecID) {
            selectRecording(recordings[recordings.length - 1].id);
          }
        } else {
          noRecEl.style.display = "block";
        }
      })
      .catch(function() {});
  }
  fetchRecordings();
  setInterval(fetchRecordings, 5000);

  function renderRecordingTabs() {
    recListEl.innerHTML = "";
    for (var i = 0; i < recordings.length; i++) {
      var rec = recordings[i];
      var tab = document.createElement("div");
      tab.className = "rec-tab" + (rec.id === selectedRecID ? " selected" : "");
      tab.setAttribute("data-id", rec.id);

      var label = "Recording " + (i + 1);
      if (rec.started_at) {
        var d = new Date(rec.started_at);
        label += " (" + d.toLocaleTimeString() + ")";
      }
      tab.textContent = label;

      var dot = document.createElement("span");
      dot.className = rec.status === "active" ? "live-dot" : "completed-dot";
      tab.appendChild(dot);

      tab.addEventListener("click", (function(id) {
        return function() { selectRecording(id); };
      })(rec.id));

      recListEl.appendChild(tab);
    }
  }

  function selectRecording(recID) {
    selectedRecID = recID;
    renderRecordingTabs();
    loadRecording(recID);
  }

  function loadRecording(recID) {
    var url = BASE + "/recordings/" + recID + "/stream/playlist.m3u8";
    destroyHLS();
    startHLS(url);
  }

  // ---- HLS player ----
  function startHLS(url) {
    if (!Hls.isSupported()) {
      video.src = url;
      return;
    }
    hls = new Hls({
      liveSyncDurationCount: 1,
      liveMaxLatencyDurationCount: 2,
      enableWorker: true
    });
    hls.loadSource(url);
    hls.attachMedia(video);
    hls.on(Hls.Events.MANIFEST_PARSED, function() {
      video.play().catch(function(){});
    });
    hls.on(Hls.Events.ERROR, function(event, data) {
      if (data.fatal) {
        if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
          showError("Waiting for stream…");
          hls.destroy();
          hls = null;
          setTimeout(function() { startHLS(url); }, 3000);
        } else if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
          showError("Media error — recovering…");
          hls.recoverMediaError();
        } else {
          showError("Fatal playback error.");
        }
      }
    });
  }

  function destroyHLS() {
    if (hls) { hls.destroy(); hls = null; }
    video.removeAttribute("src");
    video.load();
  }

  // ---- Jump to Live ----
  document.getElementById("btn-live").addEventListener("click", function() {
    if (hls && hls.liveSyncPosition) {
      video.currentTime = hls.liveSyncPosition;
    } else if (video.duration && isFinite(video.duration)) {
      video.currentTime = video.duration;
    }
    video.play().catch(function(){});
  });

  // ---- Terminate Session ----
  document.getElementById("btn-terminate").addEventListener("click", function() {
    if (!confirm("Are you sure you want to terminate this session?")) return;
    fetch(BASE + "/terminate", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({reason: "Terminated via monitor UI", notify_user: true})
    })
    .then(function(r) {
      if (!r.ok) return r.json().then(function(e) { throw new Error(e.error || "request failed"); });
      statusEl.textContent = "terminated";
      statusEl.className = "status-terminated";
    })
    .catch(function(err) {
      showError("Terminate failed: " + err.message);
    });
  });

  function showError(msg) {
    errorBox.textContent = msg;
    errorBox.style.display = "block";
    setTimeout(function() { errorBox.style.display = "none"; }, 8000);
  }
})();
</script>
</body>
</html>`
