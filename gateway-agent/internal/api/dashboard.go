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
<title>RDP Bastion Gateway</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    background: #0f172a; color: #e2e8f0;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    padding: 1.5rem; max-width: 1100px; margin: 0 auto;
  }
  h1 { font-size: 1.4rem; margin-bottom: 1.5rem; color: #f8fafc; }
  h2 { font-size: 1.1rem; margin-bottom: 0.75rem; color: #94a3b8; font-weight: 500; }

  /* Status badges */
  .badge {
    display: inline-block; padding: 0.15rem 0.5rem; border-radius: 4px;
    font-size: 0.75rem; font-weight: 600; text-transform: uppercase;
  }
  .badge-pending      { background: #eab308; color: #000; }
  .badge-ready        { background: #3b82f6; color: #fff; }
  .badge-active       { background: #22c55e; color: #000; }
  .badge-launching    { background: #8b5cf6; color: #fff; }
  .badge-disconnected { background: #f97316; color: #000; }
  .badge-completed    { background: #6b7280; color: #fff; }
  .badge-terminated   { background: #ef4444; color: #fff; }
  .badge-failed       { background: #dc2626; color: #fff; }

  /* Cards */
  .card {
    background: #1e293b; border-radius: 8px; padding: 1.25rem;
    margin-bottom: 1.25rem; border: 1px solid #334155;
  }

  /* Table */
  table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
  th { text-align: left; color: #94a3b8; font-weight: 500; padding: 0.5rem 0.75rem; border-bottom: 1px solid #334155; }
  td { padding: 0.5rem 0.75rem; border-bottom: 1px solid #1e293b; }
  tr:hover td { background: #1e293b; }

  /* Form */
  .form-row { display: flex; gap: 0.75rem; align-items: flex-end; flex-wrap: wrap; }
  .form-group { display: flex; flex-direction: column; gap: 0.25rem; }
  .form-group label { font-size: 0.8rem; color: #94a3b8; }
  select, input[type="text"], input[type="number"] {
    background: #0f172a; border: 1px solid #475569; border-radius: 4px;
    color: #e2e8f0; padding: 0.5rem 0.75rem; font-size: 0.85rem; min-width: 180px;
  }
  select:focus, input:focus { outline: none; border-color: #3b82f6; }

  /* Buttons */
  .btn {
    padding: 0.5rem 1rem; border: none; border-radius: 4px; cursor: pointer;
    font-size: 0.85rem; font-weight: 600; transition: opacity 0.15s; text-decoration: none;
    display: inline-flex; align-items: center; gap: 0.35rem;
  }
  .btn:hover { opacity: 0.85; }
  .btn-primary { background: #3b82f6; color: #fff; }
  .btn-green   { background: #22c55e; color: #000; }
  .btn-red     { background: #ef4444; color: #fff; }
  .btn-sm      { padding: 0.3rem 0.6rem; font-size: 0.75rem; }

  /* Links */
  a { color: #60a5fa; text-decoration: none; }
  a:hover { text-decoration: underline; }

  /* Toast */
  #toast {
    position: fixed; bottom: 1.5rem; right: 1.5rem; padding: 0.75rem 1.25rem;
    border-radius: 6px; font-size: 0.85rem; font-weight: 500;
    display: none; z-index: 100; max-width: 400px;
  }
  .toast-success { background: #166534; color: #bbf7d0; }
  .toast-error   { background: #7f1d1d; color: #fecaca; }

  /* Health bar */
  .health-bar {
    display: flex; gap: 1.5rem; font-size: 0.8rem; color: #94a3b8;
    margin-bottom: 1.25rem; flex-wrap: wrap;
  }
  .health-bar span { display: flex; align-items: center; gap: 0.35rem; }
  .dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; }
  .dot-green  { background: #22c55e; }
  .dot-yellow { background: #eab308; }
  .dot-red    { background: #ef4444; }

  .empty-state { color: #64748b; text-align: center; padding: 2rem; }
</style>
</head>
<body>

<h1>RDP Bastion Gateway</h1>

<div class="health-bar" id="health-bar">
  <span><span class="dot dot-green" id="health-dot"></span> <span id="health-status">loading...</span></span>
  <span>Active: <strong id="h-active">-</strong></span>
  <span>Available users: <strong id="h-avail">-</strong></span>
  <span>Uptime: <strong id="h-uptime">-</strong></span>
  <span>Disk free: <strong id="h-disk">-</strong></span>
</div>

<!-- Create Session -->
<div class="card">
  <h2>New Session</h2>
  <div class="form-row">
    <div class="form-group">
      <label>Target</label>
      <select id="target-select"><option value="">Loading targets...</option></select>
    </div>
    <div class="form-group">
      <label>Requested By</label>
      <input type="text" id="requested-by" placeholder="your name" value="">
    </div>
    <div class="form-group">
      <label>Timeout (min)</label>
      <input type="number" id="timeout-min" value="60" min="5" max="480" style="min-width:80px;">
    </div>
    <button class="btn btn-primary" id="btn-create" onclick="createSession()">Create Session</button>
  </div>
  <div id="create-result" style="margin-top:0.75rem; font-size:0.85rem; display:none;"></div>
</div>

<!-- Sessions Table -->
<div class="card">
  <h2>Sessions</h2>
  <div style="display:flex; gap:0.5rem; margin-bottom:0.75rem; flex-wrap:wrap;">
    <button class="btn btn-sm btn-primary" onclick="loadSessions()">Refresh</button>
    <select id="filter-status" onchange="loadSessions()" style="min-width:120px; font-size:0.8rem; padding:0.3rem;">
      <option value="">All statuses</option>
      <option value="pending">Pending</option>
      <option value="ready">Ready</option>
      <option value="active">Active</option>
      <option value="disconnected">Disconnected</option>
      <option value="completed">Completed</option>
      <option value="terminated">Terminated</option>
      <option value="failed">Failed</option>
    </select>
  </div>
  <div id="sessions-container">
    <div class="empty-state">Loading sessions...</div>
  </div>
</div>

<div id="toast"></div>

<script>
var API = "/api/v1";

// ---- Health ----
function loadHealth() {
  fetch("/health")
    .then(function(r) { return r.json(); })
    .then(function(d) {
      var dot = document.getElementById("health-dot");
      document.getElementById("health-status").textContent = d.status;
      dot.className = "dot " + (d.status === "ok" ? "dot-green" : "dot-red");
      document.getElementById("h-active").textContent = d.active_sessions;
      document.getElementById("h-avail").textContent = d.available_users;
      var mins = Math.floor(d.uptime_seconds / 60);
      var hrs = Math.floor(mins / 60);
      document.getElementById("h-uptime").textContent = hrs > 0 ? hrs + "h " + (mins % 60) + "m" : mins + "m";
      document.getElementById("h-disk").textContent = d.recordings_dir_free_gb.toFixed(1) + " GB";
    })
    .catch(function() {
      document.getElementById("health-status").textContent = "unreachable";
      document.getElementById("health-dot").className = "dot dot-red";
    });
}

// ---- Targets ----
function loadTargets() {
  fetch(API + "/targets")
    .then(function(r) { return r.json(); })
    .then(function(d) {
      var sel = document.getElementById("target-select");
      sel.innerHTML = "";
      if (!d.targets || d.targets.length === 0) {
        sel.innerHTML = '<option value="">No targets configured</option>';
        return;
      }
      d.targets.forEach(function(t) {
        var opt = document.createElement("option");
        opt.value = t.id;
        opt.textContent = t.name + " (" + t.host + ")";
        sel.appendChild(opt);
      });
    });
}

// ---- Sessions ----
function loadSessions() {
  var status = document.getElementById("filter-status").value;
  var url = API + "/sessions";
  if (status) url += "?status=" + status;
  fetch(url)
    .then(function(r) { return r.json(); })
    .then(function(d) {
      var container = document.getElementById("sessions-container");
      if (!d.sessions || d.sessions.length === 0) {
        container.innerHTML = '<div class="empty-state">No sessions found</div>';
        return;
      }
      // Sort newest first
      d.sessions.sort(function(a, b) { return new Date(b.started_at) - new Date(a.started_at); });
      var html = '<table><thead><tr><th>ID</th><th>Status</th><th>Target</th><th>Requested By</th><th>Started</th><th>Expires</th><th>Actions</th></tr></thead><tbody>';
      d.sessions.forEach(function(s) {
        var started = new Date(s.started_at).toLocaleString();
        var expires = new Date(s.expires_at).toLocaleString();
        var isAlive = ["pending","ready","active","disconnected","launching"].indexOf(s.status) >= 0;
        var actions = '';
        if (isAlive) {
          actions += '<a class="btn btn-sm btn-green" href="' + API + '/sessions/' + s.session_id + '/rdp-file" title="Download RDP file">RDP</a> ';
          actions += '<a class="btn btn-sm btn-primary" href="' + API + '/sessions/' + s.session_id + '/monitor" title="Monitor session">Monitor</a> ';
          actions += '<button class="btn btn-sm btn-red" onclick="terminateSession(\'' + s.session_id + '\')">Kill</button>';
        } else {
          actions += '<a class="btn btn-sm btn-primary" href="' + API + '/sessions/' + s.session_id + '/recording" title="Download recording">Recording</a>';
        }
        html += '<tr>';
        html += '<td><a href="' + API + '/sessions/' + s.session_id + '" target="_blank">' + s.session_id + '</a></td>';
        html += '<td><span class="badge badge-' + s.status + '">' + s.status + '</span></td>';
        html += '<td>' + (s.target_name || s.target_id) + '</td>';
        html += '<td>' + (s.requested_by || '-') + '</td>';
        html += '<td>' + started + '</td>';
        html += '<td>' + expires + '</td>';
        html += '<td>' + actions + '</td>';
        html += '</tr>';
      });
      html += '</tbody></table>';
      container.innerHTML = html;
    })
    .catch(function(err) {
      document.getElementById("sessions-container").innerHTML = '<div class="empty-state">Failed to load sessions</div>';
    });
}

// ---- Create Session ----
function createSession() {
  var targetId = document.getElementById("target-select").value;
  var requestedBy = document.getElementById("requested-by").value.trim();
  var timeout = parseInt(document.getElementById("timeout-min").value) || 60;

  if (!targetId) { toast("Select a target", "error"); return; }
  if (!requestedBy) { toast("Enter your name", "error"); return; }

  var btn = document.getElementById("btn-create");
  btn.disabled = true;
  btn.textContent = "Creating...";

  fetch(API + "/sessions", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({target_id: targetId, requested_by: requestedBy, timeout_minutes: timeout})
  })
  .then(function(r) {
    if (!r.ok) return r.json().then(function(e) { throw new Error(e.error || "request failed"); });
    return r.json();
  })
  .then(function(d) {
    var result = document.getElementById("create-result");
    result.style.display = "block";
    result.innerHTML = 'Session <strong>' + d.session_id + '</strong> created. '
      + '<a class="btn btn-sm btn-green" href="' + API + '/sessions/' + d.session_id + '/rdp-file">Download RDP File</a> '
      + '<a class="btn btn-sm btn-primary" href="' + API + '/sessions/' + d.session_id + '/monitor">Monitor</a>'
      + '<br><span style="color:#94a3b8; font-size:0.8rem;">User: ' + d.gateway_user + ' | Pass: ' + d.gateway_pass + '</span>';
    toast("Session " + d.session_id + " created", "success");
    loadSessions();
    loadHealth();
  })
  .catch(function(err) {
    toast("Failed: " + err.message, "error");
  })
  .finally(function() {
    btn.disabled = false;
    btn.textContent = "Create Session";
  });
}

// ---- Terminate ----
function terminateSession(id) {
  if (!confirm("Terminate session " + id + "?")) return;
  fetch(API + "/sessions/" + id + "/terminate", {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({reason: "Terminated via dashboard", notify_user: true})
  })
  .then(function(r) {
    if (!r.ok) return r.json().then(function(e) { throw new Error(e.error || "request failed"); });
    toast("Session " + id + " terminated", "success");
    loadSessions();
    loadHealth();
  })
  .catch(function(err) { toast("Failed: " + err.message, "error"); });
}

// ---- Toast ----
function toast(msg, type) {
  var el = document.getElementById("toast");
  el.textContent = msg;
  el.className = type === "error" ? "toast-error" : "toast-success";
  el.style.display = "block";
  setTimeout(function() { el.style.display = "none"; }, 5000);
}

// ---- Init ----
loadHealth();
loadTargets();
loadSessions();
setInterval(loadHealth, 15000);
setInterval(loadSessions, 10000);
</script>
</body>
</html>`
