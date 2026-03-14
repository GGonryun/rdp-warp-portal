package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// handleStreamFile serves HLS playlist (.m3u8) and segment (.ts) files for a
// given session's recording directory.
func (s *Server) handleStreamFile(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "session_id")
	filename := chi.URLParam(r, "filename")

	// ---- path traversal protection ----
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		respondError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	// ---- extension whitelist ----
	ext := strings.ToLower(filepath.Ext(filename))
	if ext != ".m3u8" && ext != ".ts" {
		respondError(w, http.StatusBadRequest, "unsupported file type")
		return
	}

	// ---- session existence check ----
	if _, err := s.mgr.GetSession(sessionID); err != nil {
		respondError(w, http.StatusNotFound, "session not found")
		return
	}

	// ---- resolve and serve ----
	filePath := filepath.Join(s.cfg.RecordingsDir, sessionID, filename)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		respondError(w, http.StatusNotFound, "file not found")
		return
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

// monitorHTML is the self-contained HTML page served by handleMonitor.
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
  var HLS_URL    = BASE + "/stream/playlist.m3u8";
  var video      = document.getElementById("video");
  var statusEl   = document.getElementById("status");
  var infoEl     = document.getElementById("session-info");
  var errorBox   = document.getElementById("error-box");
  var hls;

  var sessionEnded = false;

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

        // When session ends, reload the HLS playlist (now finalized as VOD)
        if (!sessionEnded && (st === "completed" || st === "terminated")) {
          sessionEnded = true;
          document.getElementById("btn-terminate").style.display = "none";

          // Reload the playlist after a short delay for finalization
          setTimeout(function() {
            if (hls) {
              hls.stopLoad();
              hls.loadSource(HLS_URL);
              hls.startLoad();
            } else {
              video.src = HLS_URL;
              video.load();
            }
            // Change "Jump to Live" to "Restart"
            var btn = document.getElementById("btn-live");
            btn.textContent = "Restart";
          }, 2000);
        }
      })
      .catch(function() {
        statusEl.textContent = "error";
        statusEl.className = "";
      });
  }
  fetchSessionInfo();
  setInterval(fetchSessionInfo, 5000);

  // ---- HLS player ----
  function startHLS() {
    if (!Hls.isSupported()) {
      // Safari native HLS
      video.src = HLS_URL;
      return;
    }
    hls = new Hls({
      liveSyncDurationCount: 1,
      liveMaxLatencyDurationCount: 2,
      enableWorker: true
    });
    hls.loadSource(HLS_URL);
    hls.attachMedia(video);
    hls.on(Hls.Events.MANIFEST_PARSED, function() {
      video.play().catch(function(){});
    });
    hls.on(Hls.Events.ERROR, function(event, data) {
      if (data.fatal) {
        if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
          showError("Network error — retrying…");
          setTimeout(function() { hls.startLoad(); }, 3000);
        } else if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
          showError("Media error — recovering…");
          hls.recoverMediaError();
        } else {
          showError("Fatal playback error.");
        }
      }
    });
  }
  startHLS();

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
