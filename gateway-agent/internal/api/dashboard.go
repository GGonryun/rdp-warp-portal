package api

import (
	"fmt"
	"net/http"
)

// handleDashboard serves a self-contained HTML page that lists sessions,
// allows creating new sessions, and provides RDP file download links.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, dashboardHTML)
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>p0rtal Gateway</title>
<script src="https://cdn.jsdelivr.net/npm/hls.js@1"></script>
<style>
  :root {
    --bg-0: #0b1120; --bg-1: #111827; --bg-2: #1e293b; --bg-3: #334155;
    --text-0: #f8fafc; --text-1: #e2e8f0; --text-2: #94a3b8; --text-3: #64748b;
    --blue: #3b82f6; --blue-dim: #1e3a5f; --green: #22c55e; --green-dim: #14532d;
    --red: #ef4444; --red-dim: #7f1d1d; --yellow: #eab308; --orange: #f97316;
    --purple: #8b5cf6; --cyan: #06b6d4;
    --radius: 8px; --radius-sm: 4px;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    background: var(--bg-0); color: var(--text-1);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", sans-serif;
    font-size: 14px; line-height: 1.5;
  }
  a { color: var(--blue); text-decoration: none; }
  a:hover { text-decoration: underline; }

  /* Layout */
  .shell { max-width: 1280px; margin: 0 auto; padding: 1.25rem; }
  .header {
    display: flex; align-items: center; justify-content: space-between;
    margin-bottom: 1rem; flex-wrap: wrap; gap: 0.75rem;
  }
  .header h1 { font-size: 1.3rem; color: var(--text-0); font-weight: 700; letter-spacing: -0.02em; }
  .header h1 span { color: var(--blue); }

  /* Health bar */
  .health {
    display: flex; gap: 1.25rem; font-size: 0.8rem; color: var(--text-2);
    flex-wrap: wrap; align-items: center;
  }
  .health-item { display: flex; align-items: center; gap: 0.3rem; }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
  .dot-ok { background: var(--green); box-shadow: 0 0 6px var(--green); }
  .dot-err { background: var(--red); box-shadow: 0 0 6px var(--red); }

  /* Tabs */
  .tabs {
    display: flex; gap: 0; border-bottom: 1px solid var(--bg-3); margin-bottom: 1.25rem;
  }
  .tab {
    padding: 0.6rem 1.25rem; cursor: pointer; font-weight: 600; font-size: 0.85rem;
    color: var(--text-3); border-bottom: 2px solid transparent;
    transition: color 0.15s, border-color 0.15s; user-select: none;
  }
  .tab:hover { color: var(--text-1); }
  .tab.active { color: var(--blue); border-bottom-color: var(--blue); }
  .tab-panel { display: none; }
  .tab-panel.active { display: block; }

  /* Cards */
  .card {
    background: var(--bg-1); border: 1px solid var(--bg-3); border-radius: var(--radius);
    padding: 1.25rem; margin-bottom: 1.25rem;
  }
  .card-title {
    font-size: 0.95rem; font-weight: 600; color: var(--text-0); margin-bottom: 0.75rem;
    display: flex; align-items: center; justify-content: space-between;
  }

  /* Forms */
  .form-row { display: flex; gap: 0.75rem; align-items: flex-end; flex-wrap: wrap; }
  .form-group { display: flex; flex-direction: column; gap: 0.25rem; flex: 1; min-width: 160px; }
  .form-group label { font-size: 0.75rem; color: var(--text-2); font-weight: 500; text-transform: uppercase; letter-spacing: 0.04em; }
  .form-group.narrow { flex: 0 0 100px; min-width: 80px; }
  input, select {
    background: var(--bg-0); border: 1px solid var(--bg-3); border-radius: var(--radius-sm);
    color: var(--text-1); padding: 0.5rem 0.75rem; font-size: 0.85rem;
    transition: border-color 0.15s; width: 100%;
  }
  input:focus, select:focus { outline: none; border-color: var(--blue); }

  /* Buttons */
  .btn {
    padding: 0.45rem 0.9rem; border: none; border-radius: var(--radius-sm); cursor: pointer;
    font-size: 0.8rem; font-weight: 600; transition: all 0.15s; text-decoration: none;
    display: inline-flex; align-items: center; gap: 0.3rem; white-space: nowrap;
    line-height: 1.4;
  }
  .btn:hover { filter: brightness(1.15); text-decoration: none; }
  .btn:active { transform: scale(0.97); }
  .btn-primary { background: var(--blue); color: #fff; }
  .btn-green  { background: var(--green); color: #000; }
  .btn-red    { background: var(--red); color: #fff; }
  .btn-ghost  { background: var(--bg-3); color: var(--text-1); }
  .btn-sm { padding: 0.3rem 0.6rem; font-size: 0.75rem; }
  .btn-icon { padding: 0.3rem 0.45rem; font-size: 0.85rem; line-height: 1; }
  .btn[disabled] { opacity: 0.5; cursor: not-allowed; }
  .btn-group { display: flex; gap: 0.35rem; flex-wrap: wrap; }

  /* Table */
  .table-wrap { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: 0.82rem; }
  th {
    text-align: left; color: var(--text-2); font-weight: 500; padding: 0.55rem 0.75rem;
    border-bottom: 1px solid var(--bg-3); font-size: 0.75rem; text-transform: uppercase;
    letter-spacing: 0.04em; white-space: nowrap;
  }
  td { padding: 0.55rem 0.75rem; border-bottom: 1px solid rgba(51,65,85,0.4); vertical-align: middle; }
  tr:hover td { background: rgba(30,41,59,0.5); }
  .session-id { font-family: "SF Mono", "Fira Code", monospace; font-size: 0.78rem; cursor: pointer; }
  .session-id:hover { color: var(--cyan); }
  .empty { color: var(--text-3); text-align: center; padding: 2.5rem 1rem; }

  /* Badges */
  .badge {
    display: inline-block; padding: 0.12rem 0.5rem; border-radius: 3px;
    font-size: 0.7rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.03em;
  }
  .badge-pending      { background: rgba(234,179,8,0.15); color: var(--yellow); }
  .badge-ready        { background: rgba(59,130,246,0.15); color: var(--blue); }
  .badge-active       { background: rgba(34,197,94,0.15); color: var(--green); }
  .badge-launching    { background: rgba(139,92,246,0.15); color: var(--purple); }
  .badge-disconnected { background: rgba(249,115,22,0.15); color: var(--orange); }
  .badge-completed    { background: rgba(107,114,128,0.15); color: #9ca3af; }
  .badge-terminated   { background: rgba(239,68,68,0.15); color: var(--red); }
  .badge-failed       { background: rgba(220,38,38,0.2); color: #fca5a5; }

  /* Modal */
  .modal-backdrop {
    position: fixed; inset: 0; background: rgba(0,0,0,0.7); z-index: 1000;
    display: flex; align-items: center; justify-content: center;
    opacity: 0; pointer-events: none; transition: opacity 0.2s;
  }
  .modal-backdrop.open { opacity: 1; pointer-events: auto; }
  .modal {
    background: var(--bg-1); border: 1px solid var(--bg-3); border-radius: var(--radius);
    width: 95%; max-height: 90vh; overflow-y: auto; position: relative;
    transform: translateY(20px); transition: transform 0.2s;
  }
  .modal-backdrop.open .modal { transform: translateY(0); }
  .modal-sm { max-width: 560px; }
  .modal-lg { max-width: 960px; }
  .modal-xl { max-width: 1100px; }
  .modal-head {
    display: flex; align-items: center; justify-content: space-between;
    padding: 1rem 1.25rem; border-bottom: 1px solid var(--bg-3);
  }
  .modal-head h3 { font-size: 1rem; color: var(--text-0); }
  .modal-body { padding: 1.25rem; }
  .modal-close {
    background: none; border: none; color: var(--text-3); font-size: 1.4rem;
    cursor: pointer; padding: 0.25rem; line-height: 1;
  }
  .modal-close:hover { color: var(--text-0); }

  /* Detail grid */
  .detail-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0.5rem 2rem; margin-bottom: 1rem; }
  .detail-item label { display: block; font-size: 0.7rem; color: var(--text-3); text-transform: uppercase; letter-spacing: 0.04em; margin-bottom: 0.1rem; }
  .detail-item .val { font-size: 0.88rem; color: var(--text-0); word-break: break-all; }
  .detail-item .val.mono { font-family: "SF Mono", "Fira Code", monospace; font-size: 0.82rem; }
  .copy-wrap { display: inline-flex; align-items: center; gap: 0.35rem; }
  .copy-btn {
    background: none; border: 1px solid var(--bg-3); color: var(--text-3); border-radius: 3px;
    padding: 0.1rem 0.35rem; font-size: 0.7rem; cursor: pointer; transition: all 0.15s;
  }
  .copy-btn:hover { border-color: var(--blue); color: var(--blue); }
  .secret-toggle {
    background: none; border: none; color: var(--text-3); cursor: pointer; font-size: 0.75rem;
    padding: 0.1rem 0.3rem;
  }
  .secret-toggle:hover { color: var(--text-1); }

  /* Video player in modal */
  .player-wrap { background: #000; border-radius: var(--radius); overflow: hidden; margin-bottom: 1rem; position: relative; }
  .player-wrap video { width: 100%; display: block; max-height: 65vh; }

  /* Recording card */
  .rec-card {
    background: var(--bg-2); border-radius: var(--radius); padding: 1rem;
    margin-bottom: 0.75rem; display: flex; gap: 1rem; align-items: center; flex-wrap: wrap;
  }
  .rec-info { flex: 1; min-width: 200px; }
  .rec-info .rec-title { font-weight: 600; color: var(--text-0); font-size: 0.88rem; margin-bottom: 0.25rem; }
  .rec-info .rec-meta { font-size: 0.78rem; color: var(--text-2); }

  /* Toast */
  .toast-container { position: fixed; bottom: 1.25rem; right: 1.25rem; z-index: 2000; display: flex; flex-direction: column-reverse; gap: 0.5rem; }
  .toast {
    padding: 0.65rem 1rem; border-radius: var(--radius-sm); font-size: 0.82rem; font-weight: 500;
    animation: slideIn 0.2s ease-out; max-width: 400px; display: flex; align-items: center; gap: 0.5rem;
  }
  .toast-success { background: var(--green-dim); color: #bbf7d0; border: 1px solid rgba(34,197,94,0.3); }
  .toast-error   { background: var(--red-dim); color: #fecaca; border: 1px solid rgba(239,68,68,0.3); }
  @keyframes slideIn { from { transform: translateX(100%); opacity: 0; } to { transform: translateX(0); opacity: 1; } }

  /* Filter bar */
  .filter-bar { display: flex; gap: 0.5rem; align-items: center; flex-wrap: wrap; }
  .filter-bar select, .filter-bar input { min-width: auto; width: auto; font-size: 0.8rem; padding: 0.35rem 0.6rem; }

  /* Responsive */
  @media (max-width: 768px) {
    .detail-grid { grid-template-columns: 1fr; }
    .form-group { min-width: 100%; }
    .header { flex-direction: column; align-items: flex-start; }
  }

  /* Separator */
  .sep { border: 0; border-top: 1px solid var(--bg-3); margin: 1rem 0; }
</style>
</head>
<body>
<div class="shell">

  <!-- Header -->
  <div class="header">
    <h1><span>p0rtal</span> Gateway</h1>
    <div class="health" id="health">
      <div class="health-item"><span class="dot dot-ok" id="h-dot"></span> <span id="h-status">connecting...</span></div>
      <div class="health-item">Active: <strong id="h-active">-</strong></div>
      <div class="health-item">Available: <strong id="h-avail">-</strong></div>
      <div class="health-item">Uptime: <strong id="h-uptime">-</strong></div>
      <div class="health-item">Disk: <strong id="h-disk">-</strong></div>
    </div>
  </div>

  <!-- Tabs -->
  <div class="tabs">
    <div class="tab active" data-tab="sessions">Sessions</div>
    <div class="tab" data-tab="recordings">Recordings</div>
  </div>

  <!-- ============ SESSIONS TAB ============ -->
  <div class="tab-panel active" id="panel-sessions">

    <!-- Create Session -->
    <div class="card">
      <div class="card-title">New Session</div>
      <div class="form-row">
        <div class="form-group">
          <label>Target</label>
          <select id="f-target"><option value="">Loading...</option></select>
        </div>
        <div class="form-group narrow">
          <label>Timeout</label>
          <input type="number" id="f-timeout" value="60" min="5" max="480">
        </div>
        <div class="form-group narrow" style="flex:0 0 auto; min-width: auto;">
          <label>&nbsp;</label>
          <button class="btn btn-primary" id="btn-create" onclick="createSession()">Create</button>
        </div>
      </div>
      <div id="create-result" style="margin-top:0.75rem; display:none;"></div>
    </div>

    <!-- Sessions List -->
    <div class="card">
      <div class="card-title">
        <span>Sessions</span>
        <div class="filter-bar">
          <select id="f-status" onchange="loadSessions()">
            <option value="">All</option>
            <option value="pending">Pending</option>
            <option value="ready">Ready</option>
            <option value="active">Active</option>
            <option value="disconnected">Disconnected</option>
            <option value="completed">Completed</option>
            <option value="terminated">Terminated</option>
            <option value="failed">Failed</option>
          </select>
          <button class="btn btn-sm btn-ghost" onclick="loadSessions()">Refresh</button>
        </div>
      </div>
      <div class="table-wrap" id="sessions-container">
        <div class="empty">Loading sessions...</div>
      </div>
    </div>
  </div>

  <!-- ============ RECORDINGS TAB ============ -->
  <div class="tab-panel" id="panel-recordings">
    <div class="card">
      <div class="card-title">
        <span>Recordings</span>
        <div class="filter-bar">
          <input type="date" id="f-rec-date">
          <button class="btn btn-sm btn-ghost" onclick="loadRecordings()">Filter</button>
          <button class="btn btn-sm btn-ghost" onclick="clearRecDate()">Clear</button>
        </div>
      </div>
      <div id="recordings-container">
        <div class="empty">Loading recordings...</div>
      </div>
    </div>
  </div>

</div><!-- .shell -->

<!-- ============ SESSION DETAIL MODAL ============ -->
<div class="modal-backdrop" id="modal-detail">
  <div class="modal modal-sm">
    <div class="modal-head">
      <h3>Session Details</h3>
      <button class="modal-close" onclick="closeModal('modal-detail')">&times;</button>
    </div>
    <div class="modal-body" id="detail-body"></div>
  </div>
</div>

<!-- ============ MONITOR MODAL ============ -->
<div class="modal-backdrop" id="modal-monitor">
  <div class="modal modal-lg">
    <div class="modal-head">
      <h3 id="monitor-title">Live Monitor</h3>
      <button class="modal-close" onclick="closeMonitor()">&times;</button>
    </div>
    <div class="modal-body">
      <div id="monitor-info" style="font-size:0.8rem; color:var(--text-2); margin-bottom:0.75rem;"></div>
      <div class="player-wrap">
        <video id="monitor-video" controls autoplay muted></video>
      </div>
      <div class="btn-group">
        <button class="btn btn-primary btn-sm" onclick="monitorJumpLive()">Jump to Live</button>
        <button class="btn btn-red btn-sm" id="btn-mon-terminate" onclick="monitorTerminate()">Terminate</button>
      </div>
    </div>
  </div>
</div>

<!-- ============ RECORDING PLAYER MODAL ============ -->
<div class="modal-backdrop" id="modal-player">
  <div class="modal modal-lg">
    <div class="modal-head">
      <h3 id="player-title">Recording</h3>
      <button class="modal-close" onclick="closePlayer()">&times;</button>
    </div>
    <div class="modal-body">
      <div class="player-wrap">
        <video id="player-video" controls style="max-height:70vh;"></video>
      </div>
    </div>
  </div>
</div>

<!-- Toast container -->
<div class="toast-container" id="toasts"></div>

<script>
var API = "/api/v1";
var hlsInstance = null;
var monitorSessionId = null;
var monitorPollTimer = null;

// ===================== TABS =====================
document.querySelectorAll(".tab").forEach(function(tab) {
  tab.addEventListener("click", function() {
    document.querySelectorAll(".tab").forEach(function(t) { t.classList.remove("active"); });
    document.querySelectorAll(".tab-panel").forEach(function(p) { p.classList.remove("active"); });
    tab.classList.add("active");
    document.getElementById("panel-" + tab.dataset.tab).classList.add("active");
    if (tab.dataset.tab === "recordings") loadRecordings();
  });
});

// ===================== MODALS =====================
function openModal(id) { document.getElementById(id).classList.add("open"); }
function closeModal(id) { document.getElementById(id).classList.remove("open"); }

// Close on backdrop click
document.querySelectorAll(".modal-backdrop").forEach(function(bd) {
  bd.addEventListener("click", function(e) {
    if (e.target === bd) {
      bd.classList.remove("open");
      if (bd.id === "modal-monitor") destroyMonitorHLS();
      if (bd.id === "modal-player") stopPlayer();
    }
  });
});

// Close on Escape
document.addEventListener("keydown", function(e) {
  if (e.key === "Escape") {
    document.querySelectorAll(".modal-backdrop.open").forEach(function(bd) {
      bd.classList.remove("open");
    });
    destroyMonitorHLS();
    stopPlayer();
  }
});

// ===================== HEALTH =====================
function loadHealth() {
  fetch("/health").then(function(r) { return r.json(); }).then(function(d) {
    var dot = document.getElementById("h-dot");
    dot.className = "dot " + (d.status === "ok" ? "dot-ok" : "dot-err");
    document.getElementById("h-status").textContent = d.status;
    document.getElementById("h-active").textContent = d.active_sessions;
    document.getElementById("h-avail").textContent = d.available_users;
    var m = Math.floor(d.uptime_seconds / 60);
    var h = Math.floor(m / 60);
    document.getElementById("h-uptime").textContent = h > 0 ? h + "h " + (m % 60) + "m" : m + "m";
    document.getElementById("h-disk").textContent = d.recordings_dir_free_gb.toFixed(1) + " GB";
  }).catch(function() {
    document.getElementById("h-dot").className = "dot dot-err";
    document.getElementById("h-status").textContent = "unreachable";
  });
}

// ===================== TARGETS =====================
function loadTargets() {
  fetch(API + "/targets").then(function(r) { return r.json(); }).then(function(d) {
    var sel = document.getElementById("f-target");
    sel.innerHTML = "";
    if (!d.targets || d.targets.length === 0) {
      sel.innerHTML = "<option value=''>No targets</option>";
      return;
    }
    d.targets.forEach(function(t) {
      var o = document.createElement("option");
      o.value = t.id;
      o.textContent = t.name + " (" + t.host + ")";
      sel.appendChild(o);
    });
  });
}

// ===================== SESSIONS =====================
function loadSessions() {
  var status = document.getElementById("f-status").value;
  var url = API + "/sessions" + (status ? "?status=" + status : "");
  fetch(url).then(function(r) { return r.json(); }).then(function(d) {
    var c = document.getElementById("sessions-container");
    if (!d.sessions || d.sessions.length === 0) {
      c.innerHTML = "<div class='empty'>No sessions found</div>";
      return;
    }
    d.sessions.sort(function(a, b) { return new Date(b.started_at) - new Date(a.started_at); });

    var html = "<table><thead><tr>"
      + "<th>Session</th><th>Status</th><th>Target</th>"
      + "<th>Started</th><th>Expires</th><th>Actions</th></tr></thead><tbody>";

    d.sessions.forEach(function(s) {
      var alive = ["pending","ready","active","disconnected","launching"].indexOf(s.status) >= 0;
      var started = fmtTime(s.started_at);
      var expires = fmtTime(s.expires_at);
      var shortId = s.session_id.length > 12 ? s.session_id.substring(0, 12) + "..." : s.session_id;

      var acts = "";
      if (alive) {
        acts += "<button class='btn btn-sm btn-green' onclick=\"dlRDP('" + s.session_id + "')\" title='Download RDP file'>Connect</button>";
        acts += "<button class='btn btn-sm btn-primary' onclick=\"openMonitorModal('" + s.session_id + "')\" title='Live monitor'>Monitor</button>";
        acts += "<button class='btn btn-sm btn-red btn-icon' onclick=\"killSession('" + s.session_id + "')\" title='Terminate'>&times;</button>";
      } else if (s.status === "completed") {
        acts += "<button class='btn btn-sm btn-primary' onclick=\"playRecording('" + s.session_id + "', '" + esc(s.target_name || s.target_id) + "')\" title='Play recording'>Play</button>";
        acts += "<a class='btn btn-sm btn-ghost' href='" + API + "/sessions/" + s.session_id + "/recording' title='Download recording'>Download</a>";
      }

      html += "<tr>"
        + "<td><span class='session-id' onclick=\"showDetail('" + s.session_id + "')\" title='" + s.session_id + "'>" + shortId + "</span></td>"
        + "<td><span class='badge badge-" + s.status + "'>" + s.status + "</span></td>"
        + "<td>" + (s.target_name || s.target_id || "-") + "</td>"
        + "<td>" + started + "</td>"
        + "<td>" + expires + "</td>"
        + "<td><div class='btn-group'>" + acts + "</div></td>"
        + "</tr>";
    });

    html += "</tbody></table>";
    c.innerHTML = html;
  }).catch(function() {
    document.getElementById("sessions-container").innerHTML = "<div class='empty'>Failed to load sessions</div>";
  });
}

// ===================== CREATE SESSION =====================
function createSession() {
  var targetId = document.getElementById("f-target").value;
  var timeout = parseInt(document.getElementById("f-timeout").value) || 60;

  if (!targetId) { toast("Select a target", "error"); return; }

  var btn = document.getElementById("btn-create");
  btn.disabled = true; btn.textContent = "Creating...";

  fetch(API + "/sessions", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({target_id: targetId, timeout_minutes: timeout})
  })
  .then(function(r) {
    if (!r.ok) return r.json().then(function(e) { throw new Error(e.error || "request failed"); });
    return r.json();
  })
  .then(function(d) {
    var res = document.getElementById("create-result");
    res.style.display = "block";
    res.innerHTML = "<div style='background:var(--bg-2);border-radius:var(--radius-sm);padding:0.75rem;'>"
      + "<div style='margin-bottom:0.5rem;font-weight:600;color:var(--text-0);'>Session created: <span class='session-id' onclick=\"showDetail('" + d.session_id + "')\">" + d.session_id + "</span></div>"
      + "<div style='background:var(--bg-0);border:1px solid var(--blue);border-radius:var(--radius-sm);padding:0.75rem;margin:0.5rem 0;'>"
      + "<div style='font-size:0.7rem;color:var(--blue);text-transform:uppercase;letter-spacing:0.05em;margin-bottom:0.25rem;'>Session Token (single-use)</div>"
      + "<div style='display:flex;align-items:center;gap:0.5rem;'>"
      + "<code style='font-size:1.1rem;color:var(--text-0);letter-spacing:0.05em;flex:1;word-break:break-all;'>" + esc(d.gateway_pass) + "</code>"
      + "<button class='btn btn-sm btn-primary' onclick=\"copyText('" + esc(d.gateway_pass) + "')\">Copy</button>"
      + "</div>"
      + "<div style='font-size:0.72rem;color:var(--text-3);margin-top:0.35rem;'>User: <code>" + esc(d.gateway_user) + "</code> &mdash; Enter this token when prompted by the RDP file. It expires after first connection.</div>"
      + "</div>"
      + "<div class='btn-group'>"
      + "<button class='btn btn-sm btn-green' onclick=\"dlLauncher('" + d.session_id + "')\">Launcher (.bat auto-login)</button>"
      + "<button class='btn btn-sm btn-ghost' onclick=\"dlRDP('" + d.session_id + "')\">RDP File</button>"
      + "<button class='btn btn-sm btn-primary' onclick=\"openMonitorModal('" + d.session_id + "')\">Monitor</button>"
      + "</div></div>";
    toast("Session " + d.session_id.substring(0, 12) + " created", "success");
    loadSessions();
    loadHealth();
  })
  .catch(function(err) { toast("Failed: " + err.message, "error"); })
  .finally(function() { btn.disabled = false; btn.textContent = "Create"; });
}

// ===================== SESSION DETAIL =====================
function showDetail(id) {
  fetch(API + "/sessions/" + id).then(function(r) {
    if (!r.ok) throw new Error("not found");
    return r.json();
  }).then(function(d) {
    var alive = ["pending","ready","active","disconnected","launching"].indexOf(d.status) >= 0;
    var passHidden = true;

    var html = "";

    // Show session token prominently if available (pending/ready sessions)
    if (d.token) {
      html += "<div style='background:var(--bg-0);border:1px solid var(--blue);border-radius:var(--radius-sm);padding:0.75rem;margin-bottom:1rem;'>"
        + "<div style='font-size:0.7rem;color:var(--blue);text-transform:uppercase;letter-spacing:0.05em;margin-bottom:0.25rem;'>Session Token (single-use)</div>"
        + "<div style='display:flex;align-items:center;gap:0.5rem;'>"
        + "<code style='font-size:1.1rem;color:var(--text-0);letter-spacing:0.05em;flex:1;word-break:break-all;'>" + esc(d.token) + "</code>"
        + "<button class='btn btn-sm btn-primary' onclick=\"copyText('" + esc(d.token) + "')\">Copy</button>"
        + "</div>"
        + "<div style='font-size:0.72rem;color:var(--text-3);margin-top:0.35rem;'>Enter this when prompted by the RDP file. Expires after first connection.</div>"
        + "</div>";
    }

    html += "<div class='detail-grid'>"
      + detailItem("Session ID", "<span class='mono'>" + d.id + "</span>" + copyBtn(d.id))
      + detailItem("Status", "<span class='badge badge-" + d.status + "'>" + d.status + "</span>")
      + detailItem("Target", (d.target_name || d.target_id) + " (" + (d.target_host || "-") + ")")
      + detailItem("Target User", d.target_user || "-")
      + detailItem("Gateway User", "<span class='mono'>" + esc(d.gateway_user || "-") + "</span>" + copyBtn(d.gateway_user || ""))
      + detailItem("Recording Dir", "<span class='mono' style='font-size:0.75rem;'>" + esc(d.recording_dir || "-") + "</span>")
      + detailItem("RDS Session ID", d.rds_session_id || "-")
      + detailItem("Started", fmtTimeFull(d.started_at))
      + detailItem("Connected", fmtTimeFull(d.connected_at))
      + detailItem("Disconnected", fmtTimeFull(d.disconnected_at))
      + detailItem("Ended", fmtTimeFull(d.ended_at))
      + detailItem("Expires", fmtTimeFull(d.expires_at))
      + "</div>";

    if (d.metadata && Object.keys(d.metadata).length > 0) {
      html += "<div style='margin-bottom:1rem;'><label style='font-size:0.7rem;color:var(--text-3);text-transform:uppercase;'>Metadata</label>";
      Object.keys(d.metadata).forEach(function(k) {
        html += "<div style='font-size:0.82rem;'><strong>" + esc(k) + ":</strong> " + esc(d.metadata[k]) + "</div>";
      });
      html += "</div>";
    }

    html += "<hr class='sep'><div class='btn-group'>";
    if (alive) {
      html += "<button class='btn btn-green' onclick=\"dlLauncher('" + d.id + "')\">Download Launcher (.bat)</button>";
      html += "<button class='btn btn-ghost' onclick=\"dlRDP('" + d.id + "')\">Download RDP File</button>";
      html += "<button class='btn btn-primary' onclick=\"closeModal('modal-detail');openMonitorModal('" + d.id + "')\">Monitor</button>";
      html += "<button class='btn btn-red' onclick=\"killSession('" + d.id + "')\">Terminate</button>";
    } else if (d.status === "completed") {
      html += "<button class='btn btn-primary' onclick=\"closeModal('modal-detail');playRecording('" + d.id + "', '" + esc(d.target_name || d.target_id) + "')\">Play Recording</button>";
      html += "<a class='btn btn-ghost' href='" + API + "/sessions/" + d.id + "/recording'>Download Recording</a>";
    }
    html += "</div>";

    document.getElementById("detail-body").innerHTML = html;
    openModal("modal-detail");
  }).catch(function(err) {
    toast("Failed to load session: " + err.message, "error");
  });
}

function detailItem(label, value) {
  return "<div class='detail-item'><label>" + label + "</label><div class='val'>" + (value || "-") + "</div></div>";
}

// ===================== MONITOR =====================
function openMonitorModal(sessionId) {
  monitorSessionId = sessionId;
  document.getElementById("monitor-title").textContent = "Live Monitor — " + sessionId.substring(0, 12);
  document.getElementById("monitor-info").textContent = "Loading...";
  openModal("modal-monitor");

  // Poll session info
  fetchMonitorInfo();
  monitorPollTimer = setInterval(fetchMonitorInfo, 5000);

  // Start HLS
  var video = document.getElementById("monitor-video");
  var hlsUrl = API + "/sessions/" + sessionId + "/stream/playlist.m3u8";

  if (Hls.isSupported()) {
    hlsInstance = new Hls({
      liveSyncDurationCount: 3,
      liveMaxLatencyDurationCount: 6,
      enableWorker: true
    });
    hlsInstance.loadSource(hlsUrl);
    hlsInstance.attachMedia(video);
    hlsInstance.on(Hls.Events.MANIFEST_PARSED, function() {
      video.play().catch(function(){});
    });
    hlsInstance.on(Hls.Events.ERROR, function(ev, data) {
      if (data.fatal && data.type === Hls.ErrorTypes.NETWORK_ERROR) {
        setTimeout(function() { if (hlsInstance) hlsInstance.startLoad(); }, 3000);
      } else if (data.fatal && data.type === Hls.ErrorTypes.MEDIA_ERROR) {
        if (hlsInstance) hlsInstance.recoverMediaError();
      }
    });
  } else {
    video.src = hlsUrl;
  }
}

function fetchMonitorInfo() {
  if (!monitorSessionId) return;
  fetch(API + "/sessions/" + monitorSessionId).then(function(r) { return r.json(); }).then(function(d) {
    var parts = [];
    parts.push("<span class='badge badge-" + d.status + "'>" + d.status + "</span>");
    if (d.target_name) parts.push("Target: " + d.target_name + " (" + (d.target_host || "") + ")");
    if (d.started_at) parts.push("Started: " + fmtTime(d.started_at));
    document.getElementById("monitor-info").innerHTML = parts.join(" &middot; ");
  }).catch(function(){});
}

function monitorJumpLive() {
  var video = document.getElementById("monitor-video");
  if (hlsInstance && hlsInstance.liveSyncPosition) {
    video.currentTime = hlsInstance.liveSyncPosition;
  } else if (video.duration && isFinite(video.duration)) {
    video.currentTime = video.duration;
  }
  video.play().catch(function(){});
}

function monitorTerminate() {
  if (!monitorSessionId) return;
  if (!confirm("Terminate session " + monitorSessionId.substring(0, 12) + "?")) return;
  killSessionDirect(monitorSessionId);
}

function closeMonitor() {
  closeModal("modal-monitor");
  destroyMonitorHLS();
}

function destroyMonitorHLS() {
  if (hlsInstance) { hlsInstance.destroy(); hlsInstance = null; }
  if (monitorPollTimer) { clearInterval(monitorPollTimer); monitorPollTimer = null; }
  monitorSessionId = null;
  var v = document.getElementById("monitor-video");
  v.pause(); v.removeAttribute("src"); v.load();
}

// ===================== RECORDINGS =====================
function loadRecordings() {
  var dateVal = document.getElementById("f-rec-date").value;
  var url = API + "/recordings" + (dateVal ? "?date=" + dateVal : "");
  fetch(url).then(function(r) { return r.json(); }).then(function(d) {
    var c = document.getElementById("recordings-container");
    if (!d.recordings || d.recordings.length === 0) {
      c.innerHTML = "<div class='empty'>No recordings found" + (dateVal ? " for " + dateVal : "") + "</div>";
      return;
    }

    var html = "<table><thead><tr>"
      + "<th>Session</th><th>Target</th>"
      + "<th>Started</th><th>Ended</th><th>Actions</th></tr></thead><tbody>";

    d.recordings.forEach(function(r) {
      var shortId = r.session_id.length > 12 ? r.session_id.substring(0, 12) + "..." : r.session_id;
      html += "<tr>"
        + "<td><span class='session-id' onclick=\"showDetail('" + r.session_id + "')\" title='" + r.session_id + "'>" + shortId + "</span></td>"
        + "<td>" + (r.target_host || r.target_id || "-") + "</td>"
        + "<td>" + fmtTime(r.started_at) + "</td>"
        + "<td>" + fmtTime(r.ended_at) + "</td>"
        + "<td><div class='btn-group'>"
        + "<button class='btn btn-sm btn-primary' onclick=\"playRecording('" + r.session_id + "', '" + esc(r.target_host || r.target_id || "") + "')\">Play</button>"
        + "<a class='btn btn-sm btn-ghost' href='" + r.recording_url + "'>Download</a>"
        + "</div></td></tr>";
    });

    html += "</tbody></table>";
    c.innerHTML = html;
  }).catch(function() {
    document.getElementById("recordings-container").innerHTML = "<div class='empty'>Failed to load recordings</div>";
  });
}

function clearRecDate() {
  document.getElementById("f-rec-date").value = "";
  loadRecordings();
}

// ===================== RECORDING PLAYER =====================
function playRecording(sessionId, label) {
  document.getElementById("player-title").textContent = "Recording — " + label + " (" + sessionId.substring(0, 12) + ")";
  var video = document.getElementById("player-video");
  video.src = API + "/sessions/" + sessionId + "/recording";
  video.load();
  openModal("modal-player");
  video.play().catch(function(){});
}

function closePlayer() {
  closeModal("modal-player");
  stopPlayer();
}

function stopPlayer() {
  var v = document.getElementById("player-video");
  v.pause(); v.removeAttribute("src"); v.load();
}

// ===================== ACTIONS =====================
function dlRDP(id) { window.location.href = API + "/sessions/" + id + "/rdp-file"; }

function killSession(id) {
  if (!confirm("Terminate session " + id.substring(0, 12) + "?")) return;
  killSessionDirect(id);
}

function killSessionDirect(id) {
  fetch(API + "/sessions/" + id + "/terminate", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({reason: "Terminated via dashboard", notify_user: true})
  }).then(function(r) {
    if (!r.ok) return r.json().then(function(e) { throw new Error(e.error || "failed"); });
    toast("Session terminated", "success");
    loadSessions(); loadHealth();
  }).catch(function(err) { toast("Failed: " + err.message, "error"); });
}

// ===================== HELPERS =====================
function fmtTime(iso) {
  if (!iso) return "-";
  var d = new Date(iso);
  if (isNaN(d.getTime())) return "-";
  return d.toLocaleDateString(undefined, {month:"short", day:"numeric"})
    + " " + d.toLocaleTimeString(undefined, {hour:"2-digit", minute:"2-digit"});
}

function fmtTimeFull(iso) {
  if (!iso) return "-";
  var d = new Date(iso);
  if (isNaN(d.getTime())) return "-";
  return d.toLocaleString();
}

function esc(s) {
  if (!s) return "";
  var d = document.createElement("div");
  d.appendChild(document.createTextNode(s));
  return d.innerHTML;
}

function copyBtn(text) {
  if (!text) return "";
  return " <button class='copy-btn' onclick=\"copyText('" + esc(text).replace(/'/g, "\\'") + "')\">copy</button>";
}

function copyText(text) {
  navigator.clipboard.writeText(text).then(function() {
    toast("Copied", "success");
  }).catch(function() {
    // Fallback
    var ta = document.createElement("textarea");
    ta.value = text; ta.style.position = "fixed"; ta.style.opacity = "0";
    document.body.appendChild(ta); ta.select();
    document.execCommand("copy");
    document.body.removeChild(ta);
    toast("Copied", "success");
  });
}

function toast(msg, type) {
  var el = document.createElement("div");
  el.className = "toast toast-" + (type || "success");
  el.textContent = msg;
  var container = document.getElementById("toasts");
  container.appendChild(el);
  setTimeout(function() { el.remove(); }, 4000);
}

// ===================== INIT =====================
loadHealth();
loadTargets();
loadSessions();
setInterval(loadHealth, 15000);
setInterval(loadSessions, 10000);
</script>
</body>
</html>`
