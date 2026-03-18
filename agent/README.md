# p0rtal Agent

Windows agent that records RDP sessions (screen capture, process events, window focus, mouse clicks, clipboard, Windows Event Log) and uploads them to the p0rtal broker.

## Prerequisites

- Windows Server with RDP enabled
- `config.json` in the same directory as `agent.exe`
- Network access to the p0rtal broker
- ffmpeg (auto-downloaded if not present)

## Configuration

Create `config.json` next to `agent.exe`:

```json
{
  "proxy_url": "http://your-broker:8080",
  "api_key": "your-secret-api-key",
  "ffmpeg_path": "C:\\ffmpeg\\bin\\ffmpeg.exe",
  "framerate": 10,
  "chunk_secs": 4,
  "poll_interval": 5
}
```

| Field | Description | Default |
|-------|-------------|---------|
| `proxy_url` | Broker URL (required) | — |
| `api_key` | Bearer token for broker API auth | — |
| `ffmpeg_path` | Path to ffmpeg binary | Auto-detected or downloaded |
| `framerate` | Screen capture framerate | `5` |
| `chunk_secs` | Video segment duration in seconds | `30` |
| `poll_interval` | Session polling interval in seconds | `5` |

## Commands

All service commands require **Administrator** privileges.

### Install as a Windows Service

```powershell
.\agent.exe install
```

Installs the agent as a Windows service named `p0rtal` with automatic startup, then starts it immediately. The service will start on every reboot.

### Uninstall

```powershell
.\agent.exe uninstall
```

Stops the service (if running) and removes it.

### Reinstall

```powershell
.\agent.exe reinstall
```

Stops, uninstalls, re-installs, and starts the service. Use this after updating `agent.exe` to a new version.

### Start / Stop

```powershell
.\agent.exe start
.\agent.exe stop
```

### Check Status

```powershell
.\agent.exe status
```

Prints the service state (`RUNNING`, `STOPPED`, etc.) and PID.

### Tail Logs

```powershell
.\agent.exe log
```

Streams service logs in real time. Press `Ctrl+C` to stop.

### Run Interactively

```powershell
.\agent.exe
```

Runs the agent in the foreground (not as a service). Useful for debugging. Stops with `Ctrl+C`.

You can specify a custom config path:

```powershell
.\agent.exe -config C:\path\to\config.json
```

## What Gets Captured

| Event Type | Source | Data |
|------------|--------|------|
| Screen recording | ffmpeg (gdigrab) | Video chunks (.ts segments) |
| `process_start` | WMI Win32_ProcessStartTrace | PID, name, command line, parent PID |
| `process_end` | WMI Win32_ProcessStopTrace | PID, name, exit code |
| `window_focus` | user32.dll polling (500ms) | Window title, PID |
| `mouse_click` | Low-level mouse hook | Button, x/y coordinates, active window |
| `clipboard` | Clipboard polling (1s) | Text content (truncated to 512 chars) |
| `winlog` | Windows Event Log polling (3s) | Security, System, and PowerShell events |

### Windows Event Log Events

| Log | Event IDs | Description |
|-----|-----------|-------------|
| Security | 4624, 4625 | Logon success / failure |
| Security | 4634, 4647 | Logoff |
| Security | 4648 | Explicit credential logon (RunAs) |
| Security | 4672 | Elevated privileges assigned |
| Security | 4688, 4689 | Process creation / termination (audit) |
| Security | 4698, 4699, 4702 | Scheduled task created / deleted / updated |
| Security | 4720, 4722, 4725, 4726 | User account changes |
| Security | 4732, 4733, 4740, 4756 | Group membership changes, account lockout |
| Security | 4946, 4947, 4948 | Firewall rule changes |
| System | 7036, 7040, 7045 | Service state changes, new service installed |
| PowerShell | 4104 | Script block execution |

Agent-internal processes (`ffmpeg.exe`, `powershell.exe`, `agent.exe`) are automatically filtered from event logging.

## Building

From the repo root:

```bash
./build.sh
```

Or manually:

```bash
cd agent
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o agent.exe ./cmd/p0rtal-agent
```
